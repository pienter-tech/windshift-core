package scm

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"testing"

	"windshift/internal/models"
)

func TestGiteaCombinedCIStatusLinksTheRunThatExplainsIt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		body      string
		wantState string
		wantURL   string
	}{
		{
			name: "failure links the latest failing run",
			body: `{"state":"failure","statuses":[
				{"status":"success","target_url":"https://git.example/pienter/app/actions/runs/3/jobs/0","context":"ci / lint","updated_at":"2026-10-08T10:05:00Z"},
				{"status":"failure","target_url":"https://git.example/pienter/app/actions/runs/3/jobs/1","context":"ci / test","updated_at":"2026-10-08T10:03:00Z"},
				{"status":"error","target_url":"https://git.example/pienter/app/actions/runs/3/jobs/2","context":"ci / build","updated_at":"2026-10-08T10:04:00Z"}
			]}`,
			wantState: CIStateFailure,
			wantURL:   "https://git.example/pienter/app/actions/runs/3/jobs/2",
		},
		{
			name: "pending links the running job",
			body: `{"state":"pending","statuses":[
				{"status":"success","target_url":"https://git.example/pienter/app/actions/runs/4/jobs/0","updated_at":"2026-10-08T10:05:00Z"},
				{"status":"pending","target_url":"https://git.example/pienter/app/actions/runs/4/jobs/1","updated_at":"2026-10-08T10:01:00Z"}
			]}`,
			wantState: CIStatePending,
			wantURL:   "https://git.example/pienter/app/actions/runs/4/jobs/1",
		},
		{
			name: "success links the latest run and skips non-web targets",
			body: `{"state":"success","statuses":[
				{"status":"success","target_url":"https://git.example/pienter/app/actions/runs/5/jobs/0","updated_at":"2026-10-08T10:01:00Z"},
				{"status":"success","target_url":"javascript:alert(1)","updated_at":"2026-10-08T10:09:00Z"}
			]}`,
			wantState: CIStateSuccess,
			wantURL:   "https://git.example/pienter/app/actions/runs/5/jobs/0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			provider := newTestGiteaProvider(t, func(request *http.Request) (*http.Response, error) {
				if got := request.URL.EscapedPath(); got != "/api/v1/repos/pienter/app/commits/abc123/status" {
					t.Fatalf("unexpected path: %s", got)
				}
				return jsonResponse(http.StatusOK, tc.body), nil
			})
			status, err := provider.GetCombinedCIStatus(context.Background(), "pienter", "app", "abc123")
			if err != nil {
				t.Fatal(err)
			}
			if status == nil || status.State != tc.wantState || status.URL != tc.wantURL {
				t.Fatalf("status = %+v, want state %q url %q", status, tc.wantState, tc.wantURL)
			}
		})
	}
}

func TestGiteaCombinedCIStatusWithoutStatusesIsNone(t *testing.T) {
	t.Parallel()
	provider := newTestGiteaProvider(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"state":"","sha":"","total_count":0,"statuses":null}`), nil
	})
	status, err := provider.GetCombinedCIStatus(context.Background(), "pienter", "app", "abc123")
	if err != nil || status != nil {
		t.Fatalf("status = %+v, err = %v; want none", status, err)
	}
}

func newTestGiteaProvider(t *testing.T, roundTrip roundTripFunc) *GiteaProvider {
	t.Helper()
	provider, err := NewGiteaProvider(ProviderConfig{BaseURL: "https://git.example", AuthMethod: models.SCMAuthMethodPAT, PersonalAccessToken: "pat-token"})
	if err != nil {
		t.Fatal(err)
	}
	provider.httpClient = &http.Client{Transport: roundTrip}
	return provider
}

// ciFakeProvider adds the CI status capability to the PR fake and records the
// commits it was asked about.
type ciFakeProvider struct {
	*prMergedFakeProvider
	mu       sync.Mutex
	statuses map[string]*CIStatus
	asked    []string
}

func (p *ciFakeProvider) GetCombinedCIStatus(_ context.Context, _, _ string, sha string) (*CIStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, sha)
	return p.statuses[sha], nil
}

func (p *ciFakeProvider) askedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.asked)
}

func newCIFixture(t *testing.T) (*prMergedFixture, *ciFakeProvider, *recordedActionEvents) {
	t.Helper()
	f := newPRMergedFixture(t)
	f.provider.pr.HeadSHA = "head-1"
	ci := &ciFakeProvider{prMergedFakeProvider: f.provider, statuses: map[string]*CIStatus{
		"head-1": {State: CIStatePending, URL: "https://git.example/pienter/app/actions/runs/1"},
	}}
	f.sync.resolveProviderOverride = func(context.Context, int) (Provider, error) { return ci, nil }
	events := &recordedActionEvents{}
	f.sync.SetActionEvents(events)
	f.sync.SetDurableActionEvents(events)
	return f, ci, events
}

func (f *prMergedFixture) linkCI(t *testing.T) (state, url, head string) {
	t.Helper()
	var s, u, h sql.NullString
	if err := f.db.QueryRow(`SELECT ci_state, ci_url, ci_head_sha FROM item_scm_links WHERE id = ?`, f.linkID).Scan(&s, &u, &h); err != nil {
		t.Fatal(err)
	}
	return s.String, u.String, h.String
}

func (f *prMergedFixture) syncWith(t *testing.T, provider Provider) {
	t.Helper()
	if err := f.sync.processPullRequest(context.Background(), provider, "pienter", "app", f.provider.pr,
		f.repoID, f.workspaceID, "PRM", "", f.provider.pr.UpdatedAt.Add(-1)); err != nil {
		t.Fatalf("repository sync: %v", err)
	}
}

func TestPollingKeepsPullRequestCIStatusCurrent(t *testing.T) {
	f, ci, events := newCIFixture(t)

	f.syncWith(t, ci)
	if state, url, head := f.linkCI(t); state != CIStatePending || url != "https://git.example/pienter/app/actions/runs/1" || head != "head-1" {
		t.Fatalf("after repository sync: ci = %q %q %q", state, url, head)
	}

	ci.statuses["head-1"] = &CIStatus{State: CIStateFailure, URL: "https://git.example/pienter/app/actions/runs/1/jobs/2"}
	f.refresh(t)
	if state, url, _ := f.linkCI(t); state != CIStateFailure || url != "https://git.example/pienter/app/actions/runs/1/jobs/2" {
		t.Fatalf("after link refresh: ci = %q %q", state, url)
	}

	// A new push without CI results yet clears the old head's status.
	f.provider.pr.HeadSHA = "head-2"
	f.refresh(t)
	if state, url, head := f.linkCI(t); state != "" || url != "" || head != "head-2" {
		t.Fatalf("after push: ci = %q %q %q", state, url, head)
	}
	if len(events.events) != 0 {
		t.Fatalf("CI status emitted %d action events, want none", len(events.events))
	}
}

func TestMergedPullRequestCIStatusStopsPollingOnceFinal(t *testing.T) {
	f, ci, _ := newCIFixture(t)
	f.syncWith(t, ci)

	f.merge()
	f.syncWith(t, ci) // still pending at merge time: read again
	ci.statuses["head-1"] = &CIStatus{State: CIStateSuccess, URL: "https://git.example/pienter/app/actions/runs/1"}
	f.syncWith(t, ci)
	if state, _, _ := f.linkCI(t); state != CIStateSuccess {
		t.Fatalf("merged PR ci_state = %q, want success", state)
	}
	asked := ci.askedCount()
	f.syncWith(t, ci)
	if ci.askedCount() != asked {
		t.Fatal("merged PR with a final CI status was read again")
	}
}

func TestProvidersWithoutCIStatusStoreNone(t *testing.T) {
	f, _, _ := newCIFixture(t)
	f.sync.resolveProviderOverride = func(context.Context, int) (Provider, error) { return f.provider, nil }

	f.syncWith(t, f.provider)
	f.refresh(t)
	if state, url, head := f.linkCI(t); state != "" || url != "" || head != "" {
		t.Fatalf("provider without CI capability stored ci = %q %q %q", state, url, head)
	}
}

func TestCIDeliveryRefreshesPullRequestsWithThatHead(t *testing.T) {
	f, ci, events := newCIFixture(t)
	ctx := context.Background()

	// The delivery can arrive before the sync saw the PR's head: the PR is
	// fetched to learn it.
	if err := f.sync.RefreshPullRequestCIForCommit(ctx, f.repoID, "head-1"); err != nil {
		t.Fatal(err)
	}
	if state, _, head := f.linkCI(t); state != CIStatePending || head != "head-1" {
		t.Fatalf("after first delivery: ci = %q head %q", state, head)
	}

	ci.statuses["head-1"] = &CIStatus{State: CIStateSuccess, URL: "https://git.example/pienter/app/actions/runs/1"}
	if err := f.sync.RefreshPullRequestCIForCommit(ctx, f.repoID, "head-1"); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := f.linkCI(t); state != CIStateSuccess {
		t.Fatalf("after second delivery: ci_state = %q, want success", state)
	}

	// A status for a commit that is no PR's head changes nothing.
	asked := ci.askedCount()
	if err := f.sync.RefreshPullRequestCIForCommit(ctx, f.repoID, "main-commit"); err != nil {
		t.Fatal(err)
	}
	if ci.askedCount() != asked {
		t.Fatal("CI status read for a commit that is no pull request head")
	}
	if got := f.linkState(t); got != models.SCMLinkStateOpen {
		t.Fatalf("CI delivery changed link state to %q", got)
	}
	if len(events.events) != 0 {
		t.Fatalf("CI delivery emitted %d action events, want none", len(events.events))
	}
}
