package scm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"windshift/internal/models"
)

// labelFilterForge serves the issue list of repository pienter/app the way
// one forge applies the labels query parameter: GitHub keeps issues that
// have every listed label, Gitea/Forgejo keeps issues that have any of them
// after discarding labels the repository does not have.
type labelFilterForge struct {
	forgejo    bool
	repoLabels []string
	issues     map[int][]string // issue number -> label names

	mu          sync.Mutex
	labelParams []string
}

func (f *labelFilterForge) roundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet {
		return jsonResponse(http.StatusNotFound, `{}`), nil
	}
	if strings.HasSuffix(request.URL.Path, "/comments") {
		return jsonResponse(http.StatusOK, `[]`), nil
	}
	if !strings.HasSuffix(request.URL.Path, "/repos/pienter/app/issues") {
		return jsonResponse(http.StatusNotFound, `{}`), nil
	}

	query := request.URL.Query()
	var wanted []string
	if query.Has("labels") {
		wanted = strings.Split(query.Get("labels"), ",")
	}
	f.mu.Lock()
	f.labelParams = append(f.labelParams, query.Get("labels"))
	f.mu.Unlock()

	if f.forgejo {
		known := wanted[:0:0]
		for _, label := range wanted {
			if slices.Contains(f.repoLabels, label) {
				known = append(known, label)
			}
		}
		wanted = known
	}

	numbers := make([]int, 0, len(f.issues))
	for number := range f.issues {
		numbers = append(numbers, number)
	}
	slices.Sort(numbers)
	var listed []map[string]any
	for _, number := range numbers {
		labels := f.issues[number]
		keep := len(wanted) == 0
		if !keep && f.forgejo {
			keep = slices.ContainsFunc(wanted, func(label string) bool { return slices.Contains(labels, label) })
		} else if !keep {
			keep = !slices.ContainsFunc(wanted, func(label string) bool { return !slices.Contains(labels, label) })
		}
		if !keep {
			continue
		}
		var labelJSON []any
		for i, label := range labels {
			labelJSON = append(labelJSON, map[string]any{"id": i + 1, "name": label, "color": "00aabb"})
		}
		listed = append(listed, map[string]any{
			"id": 1000 + number, "number": number, "title": fmt.Sprintf("Issue %d", number), "state": "open",
			"html_url":   fmt.Sprintf("https://forge.example/pienter/app/issues/%d", number),
			"labels":     labelJSON,
			"updated_at": "2026-10-08T09:00:00Z",
		})
	}
	out, _ := json.Marshal(listed)
	return jsonResponse(http.StatusOK, string(out)), nil
}

func (f *labelFilterForge) sentLabelParams() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.labelParams...)
}

func newLabelFilterProvider(t *testing.T, forge *labelFilterForge) Provider {
	t.Helper()
	if forge.forgejo {
		return newTestGiteaProvider(t, forge.roundTrip)
	}
	provider, err := NewGitHubProvider(ProviderConfig{BaseURL: "https://api.forge.example", AuthMethod: models.SCMAuthMethodPAT, PersonalAccessToken: "pat-token"})
	if err != nil {
		t.Fatal(err)
	}
	provider.httpClient = &http.Client{Transport: roundTripFunc(forge.roundTrip)}
	return provider
}

func TestIssueSyncLabelFilterKeepsIssuesWithAnyFilterLabel(t *testing.T) {
	cases := []struct {
		name         string
		filterLabels []string
		wantSynced   []int
		wantParam    string
	}{
		// Issue 1 has only the second filter label, issue 2 neither, issue 3 both.
		{name: "two labels", filterLabels: []string{"bug", "ui"}, wantSynced: []int{1, 3}, wantParam: ""},
		// One label means the same for both forges, so the forge narrows the list.
		{name: "one label", filterLabels: []string{"ui"}, wantSynced: []int{1, 3}, wantParam: "ui"},
		{name: "unknown label", filterLabels: []string{"missing"}, wantSynced: nil, wantParam: "missing"},
		{name: "unknown labels", filterLabels: []string{"missing", "gone"}, wantSynced: nil, wantParam: ""},
	}
	for _, forgeName := range []string{"GitHub", "Forgejo"} {
		for _, tc := range cases {
			t.Run(forgeName+"/"+tc.name, func(t *testing.T) {
				f := newGiteaIssueSyncFixture(t)
				forge := &labelFilterForge{
					forgejo:    forgeName == "Forgejo",
					repoLabels: []string{"bug", "ui", "docs"},
					issues:     map[int][]string{1: {"ui"}, 2: {"docs"}, 3: {"bug", "ui"}},
				}
				provider := newLabelFilterProvider(t, forge)
				f.sync.resolveProviderOverride = func(context.Context, int) (Provider, error) { return provider, nil }
				filter, _ := json.Marshal(tc.filterLabels)
				if _, err := f.db.Exec(`UPDATE issue_sync_configs SET filter_labels = ?`, string(filter)); err != nil {
					t.Fatal(err)
				}

				if err := f.sync.SyncRepository(context.Background(), f.repoID); err != nil {
					t.Fatal(err)
				}

				var synced []int
				rows, err := f.db.Query(`SELECT github_issue_number FROM issue_sync_items ORDER BY github_issue_number`)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = rows.Close() }()
				for rows.Next() {
					var number int
					if err := rows.Scan(&number); err != nil {
						t.Fatal(err)
					}
					synced = append(synced, number)
				}
				if !slices.Equal(synced, tc.wantSynced) {
					t.Fatalf("synced issues %v, want %v", synced, tc.wantSynced)
				}
				if got := forge.sentLabelParams(); len(got) != 1 || got[0] != tc.wantParam {
					t.Fatalf("labels params %q, want one list with %q", got, tc.wantParam)
				}
				var lastSync *time.Time
				if err := f.db.QueryRow(`SELECT last_full_sync_at FROM issue_sync_configs`).Scan(&lastSync); err != nil || lastSync == nil {
					t.Fatalf("last_full_sync_at = %v, err = %v; skipped issues must not fail the sync", lastSync, err)
				}
			})
		}
	}
}

func TestIssueHasAnyLabelIgnoresCase(t *testing.T) {
	t.Parallel()
	issue := &Issue{Labels: []IssueLabel{{Name: "Bug"}, {Name: "ui"}}}
	if !issueHasAnyLabel(issue, []string{"docs", "bug"}) {
		t.Fatal("an issue labelled Bug matches the filter label bug")
	}
	if issueHasAnyLabel(issue, []string{"docs"}) {
		t.Fatal("an issue without docs matched the filter label docs")
	}
}

// TestPushStatusToIssueWaitsForInboundSync races an inbound sync against a
// status push whose PATCH is in flight: the inbound sync must not consume the
// loopback lock before the PATCH lands, or the next sync maps the closed
// issue back onto the item and overwrites the user's status.
func TestPushStatusToIssueWaitsForInboundSync(t *testing.T) {
	f := newGiteaIssueSyncFixture(t)
	ctx := context.Background()
	if err := f.sync.SyncRepository(ctx, f.repoID); err != nil {
		t.Fatal(err)
	}
	var itemID int
	if err := f.db.QueryRow(`SELECT item_id FROM issue_sync_items WHERE github_issue_number = 3`).Scan(&itemID); err != nil {
		t.Fatal(err)
	}

	// The user moves the item to a status that pushes "closed" but is not
	// the status a closed issue maps to, like Won't Do next to Done.
	var wontDo int
	if err := f.db.QueryRow(`SELECT id FROM statuses WHERE id NOT IN (?, ?) ORDER BY id LIMIT 1`, f.openStatus, f.closedStatus).Scan(&wontDo); err != nil {
		t.Fatalf("third status: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE issue_sync_configs SET reverse_status_mapping = ?`, fmt.Sprintf(`{"%d": "closed"}`, wontDo)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE items SET status_id = ? WHERE id = ?`, wontDo, itemID); err != nil {
		t.Fatal(err)
	}

	// The forge holds the PATCH until released, then closes the issue.
	patchStarted := make(chan struct{})
	releasePatch := make(chan struct{})
	f.sync.resolveProviderOverride = func(context.Context, int) (Provider, error) {
		return newTestGiteaProvider(t, func(request *http.Request) (*http.Response, error) {
			if request.Method == http.MethodPatch {
				close(patchStarted)
				<-releasePatch
				f.forge.mu.Lock()
				f.forge.issue["state"] = "closed"
				f.forge.issue["updated_at"] = time.Now().UTC().Format(time.RFC3339)
				f.forge.mu.Unlock()
			}
			return f.forge.roundTrip(request)
		}), nil
	}

	pushed := make(chan struct{})
	go func() {
		defer close(pushed)
		f.sync.PushStatusToIssue(ctx, itemID, wontDo)
	}()
	<-patchStarted

	inbound := make(chan error, 1)
	go func() { inbound <- f.sync.SyncRepository(ctx, f.repoID) }()
	select {
	case err := <-inbound:
		t.Errorf("an inbound sync ran while the status push was in flight (err = %v)", err)
		inbound <- err
	case <-time.After(100 * time.Millisecond):
	}
	close(releasePatch)
	<-pushed
	if err := <-inbound; err != nil {
		t.Fatal(err)
	}

	// The sync after the push sees the closed issue as already applied.
	if err := f.sync.SyncRepository(ctx, f.repoID); err != nil {
		t.Fatal(err)
	}
	var statusID int
	if err := f.db.QueryRow(`SELECT status_id FROM items WHERE id = ?`, itemID).Scan(&statusID); err != nil {
		t.Fatal(err)
	}
	if statusID != wontDo {
		t.Fatalf("item status = %d, want the user's status %d (closed maps to %d)", statusID, wontDo, f.closedStatus)
	}
}

func TestPushStatusToIssuePushesWithoutTheLockAfterWaiting(t *testing.T) {
	f := newGiteaIssueSyncFixture(t)
	ctx := context.Background()
	if err := f.sync.SyncRepository(ctx, f.repoID); err != nil {
		t.Fatal(err)
	}
	var itemID, configID int
	if err := f.db.QueryRow(`SELECT item_id, issue_sync_config_id FROM issue_sync_items WHERE github_issue_number = 3`).Scan(&itemID, &configID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE issue_sync_configs SET reverse_status_mapping = ?`, fmt.Sprintf(`{"%d": "closed"}`, f.closedStatus)); err != nil {
		t.Fatal(err)
	}

	// A long inbound sync holds the config's lock past the push's wait.
	saved := pushStatusLockWait
	pushStatusLockWait = 50 * time.Millisecond
	t.Cleanup(func() { pushStatusLockWait = saved })
	release, err := lockIssueSyncConfig(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	patched := false
	f.sync.resolveProviderOverride = func(context.Context, int) (Provider, error) {
		return newTestGiteaProvider(t, func(request *http.Request) (*http.Response, error) {
			if request.Method == http.MethodPatch {
				patched = true
			}
			return f.forge.roundTrip(request)
		}), nil
	}

	f.sync.PushStatusToIssue(ctx, itemID, f.closedStatus)

	if !patched {
		t.Fatal("no PATCH sent: the push must not be dropped while an inbound sync holds the lock")
	}
}
