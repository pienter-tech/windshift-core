package scm

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

// prMergedFakeProvider answers GetPullRequest with the current PR; every other
// Provider method is unused by these tests.
type prMergedFakeProvider struct {
	Provider
	pr PullRequest
}

func (p *prMergedFakeProvider) GetPullRequest(context.Context, string, string, int) (*PullRequest, error) {
	pr := p.pr
	return &pr, nil
}

// recordedActionEvents records both the durable and the legacy emitter.
type recordedActionEvents struct {
	mu     sync.Mutex
	events []*models.ActionEvent
}

func (r *recordedActionEvents) EmitActionEventInTx(_ context.Context, _ database.Tx, event *models.ActionEvent) error {
	r.EmitActionEvent(event)
	return nil
}

func (r *recordedActionEvents) EmitActionEvent(event *models.ActionEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recordedActionEvents) ofType(eventType models.ActionTriggerType) []*models.ActionEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*models.ActionEvent
	for _, event := range r.events {
		if event.EventType == eventType {
			out = append(out, event)
		}
	}
	return out
}

type prMergedFixture struct {
	db          database.Database
	sync        *SyncService
	provider    *prMergedFakeProvider
	workspaceID int
	itemID      int
	repoID      int
	linkID      int
}

// newPRMergedFixture stores one item with an open link to PR #7, the state the
// 15-minute refresh finds after the repository sync first saw the PR open.
func newPRMergedFixture(t *testing.T) *prMergedFixture {
	t.Helper()
	db, err := database.NewSQLiteDBWithPoolSizes(filepath.Join(t.TempDir(), "windshift.db"), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatal(err)
	}

	f := &prMergedFixture{db: db}
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
	f.workspaceID = insert(`INSERT INTO workspaces (name, key) VALUES ('PR merge', 'PRM')`)
	f.itemID = insert(`INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, 1, 'Ship it', 'a0')`, f.workspaceID)
	providerID := insert(`INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled) VALUES ('forgejo', 'Forgejo', 'gitea', 'pat', true)`)
	connectionID := insert(`INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id) VALUES (?, ?)`, f.workspaceID, providerID)
	f.repoID = insert(`INSERT INTO workspace_repositories (workspace_scm_connection_id, repository_external_id, repository_name, repository_url) VALUES (?, '1', 'pienter/app', 'https://git.example/pienter/app')`, connectionID)
	f.linkID = insert(`INSERT INTO item_scm_links (item_id, workspace_repository_id, link_type, external_id, external_url, title, state, detection_source) VALUES (?, ?, 'pull_request', '7', 'https://git.example/pienter/app/pulls/7', 'PRM-1 Ship it', 'open', 'pr_title')`, f.itemID, f.repoID)

	f.provider = &prMergedFakeProvider{pr: PullRequest{
		Number:    7,
		Title:     "PRM-1 Ship it",
		State:     "open",
		URL:       "https://git.example/pienter/app/pulls/7",
		UpdatedAt: time.Now().Add(-time.Hour),
	}}
	f.sync = NewSyncService(db, nil)
	f.sync.resolveProviderOverride = func(context.Context, int) (Provider, error) { return f.provider, nil }
	return f
}

func (f *prMergedFixture) merge() {
	mergedAt := time.Now().Add(-time.Minute)
	f.provider.pr.State = "closed"
	f.provider.pr.IsMerged = true
	f.provider.pr.MergedAt = &mergedAt
	f.provider.pr.UpdatedAt = mergedAt
}

func (f *prMergedFixture) refresh(t *testing.T) {
	t.Helper()
	if err := f.sync.RefreshItemSCMLink(context.Background(), f.linkID); err != nil {
		t.Fatalf("refresh link: %v", err)
	}
}

// repoSync runs the repository sync's per-PR step, as a 5-minute sync that
// last ran before the merge would.
func (f *prMergedFixture) repoSync(t *testing.T) {
	t.Helper()
	lastSyncedAt := time.Now().Add(-2 * time.Hour)
	if err := f.sync.processPullRequest(context.Background(), f.provider, "pienter", "app", f.provider.pr,
		f.repoID, f.workspaceID, "PRM", "", lastSyncedAt); err != nil {
		t.Fatalf("repository sync: %v", err)
	}
}

func (f *prMergedFixture) linkState(t *testing.T) models.SCMLinkState {
	t.Helper()
	var state models.SCMLinkState
	if err := f.db.QueryRow(`SELECT state FROM item_scm_links WHERE id = ?`, f.linkID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func requirePRMergedEvents(t *testing.T, events *recordedActionEvents, f *prMergedFixture, want int) {
	t.Helper()
	merged := events.ofType(models.ActionTriggerSCMPRMerged)
	if len(merged) != want {
		t.Fatalf("scm_pr_merged events = %d, want %d", len(merged), want)
	}
	for _, event := range merged {
		if event.ItemID != f.itemID || event.WorkspaceID != f.workspaceID {
			t.Fatalf("scm_pr_merged for item %d workspace %d, want item %d workspace %d", event.ItemID, event.WorkspaceID, f.itemID, f.workspaceID)
		}
		if event.NewValues["pr.number"] != 7 || event.NewValues["pr.is_merged"] != true || event.NewValues["repo.workspace_repository_id"] != f.repoID {
			t.Fatalf("scm_pr_merged payload = %v", event.NewValues)
		}
	}
}

func TestRefreshEmitsPRMergedWhenItSeesTheMergeFirst(t *testing.T) {
	f := newPRMergedFixture(t)
	events := &recordedActionEvents{}
	f.sync.SetActionEvents(events)
	f.sync.SetDurableActionEvents(events)

	f.refresh(t)
	requirePRMergedEvents(t, events, f, 0)

	f.merge()
	f.refresh(t)
	if got := f.linkState(t); got != models.SCMLinkStateMerged {
		t.Fatalf("link state = %q, want merged", got)
	}
	requirePRMergedEvents(t, events, f, 1)

	// A manual refresh and the next repository sync see the same merge.
	f.refresh(t)
	f.repoSync(t)
	requirePRMergedEvents(t, events, f, 1)
}

func TestRefreshDoesNotReemitAMergeTheRepositorySyncSawFirst(t *testing.T) {
	f := newPRMergedFixture(t)
	events := &recordedActionEvents{}
	f.sync.SetActionEvents(events)
	f.sync.SetDurableActionEvents(events)

	f.merge()
	f.repoSync(t)
	requirePRMergedEvents(t, events, f, 1)

	f.refresh(t)
	f.repoSync(t)
	requirePRMergedEvents(t, events, f, 1)
}

func TestRefreshEmitsPRMergedThroughTheLegacyEmitter(t *testing.T) {
	f := newPRMergedFixture(t)
	events := &recordedActionEvents{}
	f.sync.SetActionEvents(events)

	f.merge()
	f.refresh(t)
	f.refresh(t)
	f.repoSync(t)
	requirePRMergedEvents(t, events, f, 1)
}
