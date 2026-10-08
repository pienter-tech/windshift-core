package scm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

func TestGiteaListIssuesPageListsOnlyIssuesAndFollowsLinkHeader(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	provider := newTestGiteaProvider(t, func(request *http.Request) (*http.Response, error) {
		if got := request.URL.Path; got != "/api/v1/repos/pienter/app/issues" {
			t.Fatalf("unexpected path: %s", got)
		}
		query := request.URL.Query()
		for key, want := range map[string]string{
			"type": "issues", "state": "all", "page": "2", "limit": "50",
			"since": "2026-10-08T09:00:00Z", "labels": "bug,ui",
		} {
			if got := query.Get(key); got != want {
				t.Fatalf("query %s = %q, want %q", key, got, want)
			}
		}
		response := jsonResponse(http.StatusOK, `[{
			"id": 900, "number": 3, "title": "Crash", "body": "Steps", "state": "open",
			"html_url": "https://git.example/pienter/app/issues/3",
			"labels": [{"id": 1, "name": "bug", "color": "#ee0701"}],
			"assignees": [{"id": 5, "login": "alice"}],
			"milestone": {"id": 12, "title": "v1", "state": "open"},
			"updated_at": "2026-10-08T10:00:00Z"
		}]`)
		response.Header.Set("Link", `<https://git.example/api/v1/repos/pienter/app/issues?page=3>; rel="next",<https://git.example/api/v1/repos/pienter/app/issues?page=1>; rel="first"`)
		return response, nil
	})

	issues, hasNext, err := provider.ListIssuesPage(context.Background(), "pienter", "app", ListIssueOptions{
		State: "all", Since: &since, Labels: []string{"bug", "ui"}, Page: 2, PerPage: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasNext {
		t.Fatal("the Link header names a next page")
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %+v", issues)
	}
	issue := issues[0]
	if issue.Number != 3 || issue.State != "open" || issue.URL != "https://git.example/pienter/app/issues/3" {
		t.Fatalf("issue = %+v", issue)
	}
	if len(issue.Labels) != 1 || issue.Labels[0].Name != "bug" || issue.Labels[0].Color != "ee0701" {
		t.Fatalf("labels = %+v", issue.Labels)
	}
	if len(issue.Assignees) != 1 || issue.Assignees[0].Username != "alice" {
		t.Fatalf("assignees = %+v", issue.Assignees)
	}
	// Gitea milestones have no number; the ID is what the API takes and
	// what the milestone mapping is keyed on.
	if issue.Milestone == nil || issue.Milestone.Number != 12 || issue.Milestone.Title != "v1" {
		t.Fatalf("milestone = %+v", issue.Milestone)
	}
}

func TestGiteaListIssuesPageWithoutLinkHeaderIsLastPage(t *testing.T) {
	t.Parallel()
	provider := newTestGiteaProvider(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[{"id": 1, "number": 1, "title": "Only", "state": "open"}]`), nil
	})
	_, hasNext, err := provider.ListIssuesPage(context.Background(), "pienter", "app", ListIssueOptions{})
	if err != nil || hasNext {
		t.Fatalf("hasNext = %v, err = %v; want the last page", hasNext, err)
	}
}

func TestGiteaUpdateIssueEditsStateAndReplacesLabels(t *testing.T) {
	t.Parallel()
	var calls []string
	provider := newTestGiteaProvider(t, func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		calls = append(calls, request.Method+" "+request.URL.Path+" "+string(body))
		switch request.Method {
		case http.MethodPut:
			return jsonResponse(http.StatusOK, `[]`), nil
		case http.MethodPatch:
			// Gitea and Forgejo answer an issue edit with 201.
			return jsonResponse(http.StatusCreated, `{"id": 900, "number": 3, "title": "Crash", "state": "closed"}`), nil
		}
		t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		return nil, nil
	})

	closed := "closed"
	issue, err := provider.UpdateIssue(context.Background(), "pienter", "app", 3, UpdateIssueOptions{State: &closed, Labels: []string{"bug"}})
	if err != nil {
		t.Fatal(err)
	}
	if issue.State != "closed" {
		t.Fatalf("issue = %+v", issue)
	}
	want := []string{
		`PUT /api/v1/repos/pienter/app/issues/3/labels {"labels":["bug"]}`,
		`PATCH /api/v1/repos/pienter/app/issues/3 {"state":"closed"}`,
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestGiteaListRepoLabelsAndMilestonesReadEveryPage(t *testing.T) {
	t.Parallel()
	provider := newTestGiteaProvider(t, func(request *http.Request) (*http.Response, error) {
		page := request.URL.Query().Get("page")
		var response *http.Response
		switch request.URL.Path {
		case "/api/v1/repos/pienter/app/labels":
			response = jsonResponse(http.StatusOK, fmt.Sprintf(`[{"id": %s, "name": "label-%s", "color": "00aabb"}]`, page, page))
		case "/api/v1/repos/pienter/app/milestones":
			if got := request.URL.Query().Get("state"); got != "all" {
				t.Fatalf("milestone state = %q, want all", got)
			}
			response = jsonResponse(http.StatusOK, fmt.Sprintf(`[{"id": %s, "title": "v%s", "state": "closed"}]`, page, page))
		default:
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		if page == "1" {
			response.Header.Set("Link", `<https://git.example/next?page=2>; rel="next"`)
		}
		return response, nil
	})

	labels, err := provider.ListRepoLabels(context.Background(), "pienter", "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 2 || labels[1].Name != "label-2" {
		t.Fatalf("labels = %+v", labels)
	}
	milestones, err := provider.ListRepoMilestones(context.Background(), "pienter", "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(milestones) != 2 || milestones[1].Number != 2 || milestones[1].Title != "v2" {
		t.Fatalf("milestones = %+v", milestones)
	}
}

func TestIssueForgeNameKeepsGitHubCommentHeader(t *testing.T) {
	t.Parallel()
	if got := issueForgeName(models.SCMProviderTypeGitHub); got != "GitHub" {
		t.Fatalf("GitHub = %q", got)
	}
	if got := issueForgeName(models.SCMProviderTypeGitea); got != "Gitea/Forgejo" {
		t.Fatalf("Gitea = %q", got)
	}
}

// fakeForgejo answers the issue API of repository pienter/app from memory
// and records the writes Windshift makes.
type fakeForgejo struct {
	mu       sync.Mutex
	issue    map[string]any
	comments []map[string]any
	writes   []string
}

func (f *fakeForgejo) roundTrip(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(request.Body)
	encode := func(status int, value any) (*http.Response, error) {
		out, _ := json.Marshal(value)
		return jsonResponse(status, string(out)), nil
	}
	switch request.Method + " " + request.URL.Path {
	case "GET /api/v1/repos/pienter/app/issues":
		return encode(http.StatusOK, []any{f.issue})
	case "GET /api/v1/repos/pienter/app/issues/3/comments":
		return encode(http.StatusOK, f.comments)
	case "PATCH /api/v1/repos/pienter/app/issues/3":
		f.writes = append(f.writes, "PATCH "+string(body))
		return encode(http.StatusCreated, f.issue)
	case "POST /api/v1/repos/pienter/app/issues/3/comments":
		f.writes = append(f.writes, "COMMENT "+string(body))
		return encode(http.StatusCreated, map[string]any{"id": 501})
	}
	return jsonResponse(http.StatusNotFound, `{}`), nil
}

func (f *fakeForgejo) recordedWrites() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writes...)
}

type giteaIssueSyncFixture struct {
	db           database.Database
	sync         *IssueSyncService
	forge        *fakeForgejo
	repoID       int
	userID       int
	milestoneID  int
	openStatus   int
	closedStatus int
}

// newGiteaIssueSyncFixture links Forgejo repository pienter/app, whose issue
// #3 is open, labelled bug, assigned to alice, in milestone 12, and has one
// comment, to a workspace with an enabled issue sync config.
func newGiteaIssueSyncFixture(t *testing.T) *giteaIssueSyncFixture {
	t.Helper()
	db, err := database.NewSQLiteDBWithPoolSizes(filepath.Join(t.TempDir(), "windshift.db"), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatal(err)
	}
	insert := func(query string, args ...any) int {
		t.Helper()
		result, err := db.Exec(query, args...)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return int(id)
	}

	f := &giteaIssueSyncFixture{db: db}
	f.userID = insert(`INSERT INTO users (email, username, first_name, last_name) VALUES ('alice@example.test', 'alice', 'Alice', 'Example')`)
	workspaceID := insert(`INSERT INTO workspaces (name, key) VALUES ('Forgejo issues', 'FGI')`)
	f.milestoneID = insert(`INSERT INTO milestones (name, is_global, workspace_id) VALUES ('Version 1', 0, ?)`, workspaceID)
	providerID := insert(`INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled) VALUES ('forgejo', 'Forgejo', 'gitea', 'pat', true)`)
	connectionID := insert(`INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id) VALUES (?, ?)`, workspaceID, providerID)
	f.repoID = insert(`INSERT INTO workspace_repositories (workspace_scm_connection_id, repository_external_id, repository_name, repository_url) VALUES (?, '42', 'pienter/app', 'https://git.example/pienter/app')`, connectionID)
	if err := db.QueryRow(`
		SELECT wt.from_status_id, wt.to_status_id FROM workflow_transitions wt
		JOIN workflows w ON w.id = wt.workflow_id
		WHERE w.is_default = true AND wt.from_status_id IS NOT NULL AND wt.from_status_id <> wt.to_status_id
		LIMIT 1`).Scan(&f.openStatus, &f.closedStatus); err != nil {
		t.Fatalf("default workflow statuses: %v", err)
	}

	f.forge = &fakeForgejo{
		issue: map[string]any{
			"id": 900, "number": 3, "title": "Crash on save", "body": "Steps", "state": "open",
			"html_url":   "https://git.example/pienter/app/issues/3",
			"labels":     []any{map[string]any{"id": 1, "name": "bug", "color": "ee0701"}},
			"assignees":  []any{map[string]any{"id": 5, "login": "alice"}},
			"milestone":  map[string]any{"id": 12, "title": "v1", "state": "open"},
			"updated_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		},
		comments: []map[string]any{{
			"id": 77, "body": "Same here", "user": map[string]any{"id": 6, "login": "bob"},
			"created_at": "2026-10-08T09:00:00Z", "updated_at": "2026-10-08T09:00:00Z",
		}},
	}
	f.sync = NewIssueSyncService(db, nil)
	f.sync.resolveProviderOverride = func(context.Context, int) (Provider, error) {
		return newTestGiteaProvider(t, f.forge.roundTrip), nil
	}

	if _, err := f.sync.CreateSyncConfig(context.Background(), f.userID, models.IssueSyncConfigRequest{
		WorkspaceRepositoryID: f.repoID,
		SyncEnabled:           true,
		StatusMapping:         fmt.Sprintf(`{"open": %d, "closed": %d}`, f.openStatus, f.closedStatus),
		ReverseStatusMapping:  fmt.Sprintf(`{"%d": "closed"}`, f.closedStatus),
		LabelSyncMode:         models.IssueSyncLabelMirror,
		AssigneeMappings:      fmt.Sprintf(`{"alice": %d}`, f.userID),
		MilestoneMappings:     fmt.Sprintf(`{"12": %d}`, f.milestoneID),
		SyncComments:          true,
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestGiteaIssueSyncMapsIssueIntoItemAndPushesBack(t *testing.T) {
	f := newGiteaIssueSyncFixture(t)
	ctx := context.Background()

	if err := f.sync.SyncRepository(ctx, f.repoID); err != nil {
		t.Fatal(err)
	}

	var itemID, statusID int
	var title string
	var assigneeID *int
	if err := f.db.QueryRow(`
		SELECT i.id, i.title, i.status_id, i.assignee_id FROM issue_sync_items isi
		JOIN items i ON i.id = isi.item_id
		WHERE isi.github_issue_number = 3 AND isi.github_issue_url = 'https://git.example/pienter/app/issues/3'`).Scan(&itemID, &title, &statusID, &assigneeID); err != nil {
		t.Fatalf("issue #3 did not become an item: %v", err)
	}
	if title != "Crash on save" || statusID != f.openStatus || assigneeID == nil || *assigneeID != f.userID {
		t.Fatalf("item title=%q status=%d assignee=%v; want the mapped issue", title, statusID, assigneeID)
	}
	var label string
	if err := f.db.QueryRow(`SELECT l.name FROM item_labels il JOIN labels l ON l.id = il.label_id WHERE il.item_id = ?`, itemID).Scan(&label); err != nil || label != "bug" {
		t.Fatalf("label = %q, err = %v; want the mirrored bug label", label, err)
	}
	var milestoneID int
	if err := f.db.QueryRow(`SELECT milestone_id FROM item_milestones WHERE item_id = ?`, itemID).Scan(&milestoneID); err != nil || milestoneID != f.milestoneID {
		t.Fatalf("milestone = %d, err = %v; want %d", milestoneID, err, f.milestoneID)
	}
	var comment string
	if err := f.db.QueryRow(`SELECT content FROM comments WHERE item_id = ?`, itemID).Scan(&comment); err != nil {
		t.Fatal(err)
	}
	if comment != "**@bob** commented on Gitea/Forgejo:\n\nSame here" {
		t.Fatalf("comment = %q", comment)
	}
	var lastSync time.Time
	if err := f.db.QueryRow(`SELECT last_full_sync_at FROM issue_sync_configs WHERE workspace_repository_id = ?`, f.repoID).Scan(&lastSync); err != nil {
		t.Fatalf("last sync not recorded: %v", err)
	}

	f.sync.PushStatusToIssue(ctx, itemID, f.closedStatus)
	f.sync.PushCommentToIssue(ctx, itemID, 0, f.userID, "Fixed in main")
	want := []string{
		`PATCH {"state":"closed"}`,
		`COMMENT {"body":"Fixed in main"}`,
	}
	if got := f.forge.recordedWrites(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var syncLock bool
	if err := f.db.QueryRow(`SELECT sync_lock FROM issue_sync_items WHERE item_id = ?`, itemID).Scan(&syncLock); err != nil || !syncLock {
		t.Fatalf("sync_lock = %v, err = %v; the pushed state must skip the next inbound sync once", syncLock, err)
	}
}

func TestIssueSyncRepositoryWithoutEnabledConfigDoesNothing(t *testing.T) {
	f := newGiteaIssueSyncFixture(t)
	if _, err := f.db.Exec(`UPDATE issue_sync_configs SET sync_enabled = ?`, false); err != nil {
		t.Fatal(err)
	}
	if err := f.sync.SyncRepository(context.Background(), f.repoID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM issue_sync_items`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("synced %d issues, err = %v; want none", count, err)
	}
}

func TestIssueSyncRunsOfOneConfigDoNotOverlap(t *testing.T) {
	release, err := lockIssueSyncConfig(context.Background(), 4242)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := lockIssueSyncConfig(ctx, 4242); err == nil {
		t.Fatal("a second sync of the config started while the first ran")
	}
	release()
	again, err := lockIssueSyncConfig(context.Background(), 4242)
	if err != nil {
		t.Fatal(err)
	}
	again()
}
