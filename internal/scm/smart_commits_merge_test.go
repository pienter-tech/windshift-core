package scm

import (
	"context"
	"sync"
	"testing"
	"time"

	"windshift/internal/repository"
	"windshift/internal/services"
)

// smartCommitProvider serves the merged PR #7 with one smart-commit commit to
// a repository sync; refs, branches, and releases are empty.
type smartCommitProvider struct {
	*prMergedFakeProvider
	mu          sync.Mutex
	commitLists int
}

func (p *smartCommitProvider) ListPullRequests(_ context.Context, _, _ string, opts ListPROptions) ([]PullRequest, error) {
	if opts.Page > 1 {
		return nil, nil
	}
	return []PullRequest{p.pr}, nil
}

func (p *smartCommitProvider) ListPullRequestCommits(context.Context, string, string, int) ([]Commit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commitLists++
	return []Commit{{
		SHA:       "abc123",
		Message:   "Fix the save button\n\nPRM-1 #comment from the commit",
		Committer: User{Email: "dev@example.test"},
	}}, nil
}

func (p *smartCommitProvider) ListBranches(context.Context, string, string) ([]Branch, error) {
	return nil, nil
}

func (p *smartCommitProvider) listedCommits() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.commitLists
}

type smartCommitFixture struct {
	*prMergedFixture
	provider *smartCommitProvider
}

// newSmartCommitFixture extends the PR-merged fixture with smart commits
// enabled on the connection, a workspace administrator whose email signs the
// PR and its commit, and a repository last synced before the merge.
func newSmartCommitFixture(t *testing.T) *smartCommitFixture {
	t.Helper()
	f := &smartCommitFixture{prMergedFixture: newPRMergedFixture(t)}
	f.provider = &smartCommitProvider{prMergedFakeProvider: f.prMergedFixture.provider}
	f.provider.pr.Body = "PRM-1 #comment from the PR body"
	f.provider.pr.Author.Email = "dev@example.test"

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.db.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO users (email, username, first_name, last_name) VALUES ('dev@example.test', 'synthetic-dev', 'Synthetic', 'Dev')`)
	exec(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_by)
		SELECT u.id, ?, r.id, u.id FROM users u, workspace_roles r
		WHERE u.email = 'dev@example.test' AND r.name = 'Administrator'`, f.workspaceID)
	exec(`UPDATE items SET description = '' WHERE id = ?`, f.itemID) // comments read the item's details
	exec(`UPDATE workspace_scm_connections SET smart_commits_enabled = true`)
	exec(`UPDATE workspace_repositories SET last_synced_at = ? WHERE id = ?`, time.Now().Add(-2*time.Hour), f.repoID)

	f.wire(t, f.sync)
	return f
}

// newSyncService returns a second SyncService on the fixture's database, as
// the server runs one for the scheduler and one for webhooks and manual syncs.
func (f *smartCommitFixture) newSyncService(t *testing.T) *SyncService {
	t.Helper()
	s := NewSyncService(f.db, nil)
	f.wire(t, s)
	return s
}

func (f *smartCommitFixture) wire(t *testing.T, s *SyncService) {
	t.Helper()
	config := services.DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	config.PreWarmActive = false
	permissionService, err := services.NewPermissionService(f.db, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })
	s.SetSmartCommitServices(
		services.NewWorkflowService(f.db),
		services.NewCommentService(f.db),
		permissionService,
		services.NewConditionService(f.db, permissionService, nil),
		repository.NewItemRepository(f.db),
	)
	s.resolveProviderOverride = func(context.Context, int) (Provider, error) { return f.provider, nil }
}

// scheduledSync runs the 5-minute sync's per-PR step for a sync that last ran
// before the merge.
func (f *smartCommitFixture) scheduledSync(t *testing.T, s *SyncService) {
	t.Helper()
	lastSyncedAt := time.Now().Add(-2 * time.Hour)
	if err := s.processPullRequest(context.Background(), f.provider, "pienter", "app", f.provider.pr,
		f.repoID, f.workspaceID, "PRM", "", lastSyncedAt); err != nil {
		t.Fatalf("scheduled sync: %v", err)
	}
}

func (f *smartCommitFixture) comments(t *testing.T) []string {
	t.Helper()
	rows, err := f.db.Query(`SELECT content FROM comments WHERE item_id = ? ORDER BY id`, f.itemID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			t.Fatal(err)
		}
		out = append(out, content)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func requireSmartCommitsAppliedOnce(t *testing.T, f *smartCommitFixture) {
	t.Helper()
	got := f.comments(t)
	if len(got) != 2 || got[0] != "from the PR body" || got[1] != "from the commit" {
		t.Fatalf("comments = %q, want the PR body's and the commit's once each", got)
	}
	if n := f.provider.listedCommits(); n != 1 {
		t.Fatalf("PR commits listed %d times, want once", n)
	}
}

func TestSmartCommitsRunWhenTheLinkRefreshSeesTheMergeFirst(t *testing.T) {
	f := newSmartCommitFixture(t)
	f.merge()
	f.refresh(t)
	if got := f.comments(t); len(got) != 0 {
		t.Fatalf("the refresh applied smart commits: %q", got)
	}

	f.scheduledSync(t, f.sync)
	requireSmartCommitsAppliedOnce(t, f)

	f.refresh(t)
	f.scheduledSync(t, f.sync)
	requireSmartCommitsAppliedOnce(t, f)
}

func TestSmartCommitsRunWhenAWebhookSyncSeesTheMergeFirst(t *testing.T) {
	f := newSmartCommitFixture(t)
	scheduler := f.newSyncService(t)
	f.merge()

	// The webhook receivers and the manual sync endpoint call SyncRepository.
	if err := f.sync.SyncRepository(context.Background(), f.repoID); err != nil {
		t.Fatalf("webhook sync: %v", err)
	}
	requireSmartCommitsAppliedOnce(t, f)

	// The next scheduled sync, on its own SyncService, sees the same merge.
	if err := scheduler.SyncAllRepositories(context.Background()); err != nil {
		t.Fatalf("scheduled sync: %v", err)
	}
	f.scheduledSync(t, scheduler)
	requireSmartCommitsAppliedOnce(t, f)
}

func TestSmartCommitsApplyOnceWhenTheScheduledSyncSeesTheMergeFirst(t *testing.T) {
	f := newSmartCommitFixture(t)
	f.merge()
	f.scheduledSync(t, f.sync)
	requireSmartCommitsAppliedOnce(t, f)

	f.refresh(t)
	f.scheduledSync(t, f.sync)
	if err := f.sync.SyncRepository(context.Background(), f.repoID); err != nil {
		t.Fatalf("webhook sync: %v", err)
	}
	requireSmartCommitsAppliedOnce(t, f)
}

func TestSmartCommitsApplyOnceWhenTwoSyncsRace(t *testing.T) {
	f := newSmartCommitFixture(t)
	other := f.newSyncService(t)
	f.merge()

	var wg sync.WaitGroup
	for _, s := range []*SyncService{f.sync, other} {
		wg.Add(1)
		go func(s *SyncService) {
			defer wg.Done()
			s.processSmartCommitsForPR(context.Background(), f.provider, "pienter", "app", f.provider.pr,
				f.repoID, f.workspaceID, "PRM")
		}(s)
	}
	wg.Wait()

	if got := f.comments(t); len(got) != 2 {
		t.Fatalf("comments = %q, want the PR body's and the commit's once each", got)
	}
}

func TestSmartCommitsStayOffWhenTheConnectionDisablesThem(t *testing.T) {
	f := newSmartCommitFixture(t)
	if _, err := f.db.Exec(`UPDATE workspace_scm_connections SET smart_commits_enabled = false`); err != nil {
		t.Fatal(err)
	}
	f.merge()
	f.refresh(t)
	f.scheduledSync(t, f.sync)
	if err := f.sync.SyncRepository(context.Background(), f.repoID); err != nil {
		t.Fatalf("webhook sync: %v", err)
	}
	if got := f.comments(t); len(got) != 0 {
		t.Fatalf("comments = %q, want none", got)
	}
	if n := f.provider.listedCommits(); n != 0 {
		t.Fatalf("PR commits listed %d times, want none", n)
	}
}
