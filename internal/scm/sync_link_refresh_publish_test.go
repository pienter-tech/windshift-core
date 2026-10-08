package scm

import (
	"context"
	"sync"
	"testing"

	"windshift/internal/services"
)

// recordedItemChanges records item-change publishes for one item.
type recordedItemChanges struct {
	mu     sync.Mutex
	itemID int
	kinds  []services.ItemChangeKind
}

func (r *recordedItemChanges) PublishItemChange(itemID int, kind services.ItemChangeKind) {
	if itemID != r.itemID {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kinds = append(r.kinds, kind)
}

// take returns the publishes recorded since the last take.
func (r *recordedItemChanges) take() []services.ItemChangeKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	kinds := r.kinds
	r.kinds = nil
	return kinds
}

// recordItemChanges installs a recording publisher for the fixture's item. The
// publisher is process-wide, so tests using it must not run in parallel.
func recordItemChanges(t *testing.T, f *prMergedFixture) *recordedItemChanges {
	t.Helper()
	recorded := &recordedItemChanges{itemID: f.itemID}
	services.SetItemChangePublisher(recorded)
	t.Cleanup(func() { services.SetItemChangePublisher(nil) })
	return recorded
}

func requireLinkPublishes(t *testing.T, recorded *recordedItemChanges, want int, after string) {
	t.Helper()
	kinds := recorded.take()
	if len(kinds) != want {
		t.Fatalf("after %s: published %v, want %d link change(s)", after, kinds, want)
	}
	for _, kind := range kinds {
		if kind != services.ItemChangeLink {
			t.Fatalf("after %s: published %q, want %q", after, kind, services.ItemChangeLink)
		}
	}
}

// commitFakeProvider answers GetCommit with the current commit.
type commitFakeProvider struct {
	*prMergedFakeProvider
	commit Commit
}

func (p *commitFakeProvider) GetCommit(context.Context, string, string, string) (*Commit, error) {
	commit := p.commit
	return &commit, nil
}

func TestRefreshPublishesALinkChangeOnlyWhenThePullRequestChanged(t *testing.T) {
	f := newPRMergedFixture(t)
	recorded := recordItemChanges(t, f)

	f.refresh(t)
	requireLinkPublishes(t, recorded, 0, "a refresh that changes nothing")

	f.provider.pr.Title = "PRM-1 Ship it now"
	f.refresh(t)
	requireLinkPublishes(t, recorded, 1, "a title change")

	f.merge()
	f.refresh(t)
	requireLinkPublishes(t, recorded, 1, "a merge")

	f.refresh(t)
	requireLinkPublishes(t, recorded, 0, "refreshing the merged pull request again")
}

func TestRefreshPublishesALinkChangeOnlyWhenTheCommitChanged(t *testing.T) {
	f := newPRMergedFixture(t)
	result, err := f.db.Exec(`INSERT INTO item_scm_links (item_id, workspace_repository_id, link_type, external_id, external_url, title, author_external_id, author_name, detection_source) VALUES (?, ?, 'commit', 'abc123', 'https://git.example/pienter/app/commit/abc123', 'PRM-1 Ship it', '9', 'Ada', 'commit_message')`, f.itemID, f.repoID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	commitLinkID := int(id)
	provider := &commitFakeProvider{prMergedFakeProvider: f.provider, commit: Commit{
		SHA:     "abc123",
		Message: "PRM-1 Ship it\n\nDetails.",
		URL:     "https://git.example/pienter/app/commit/abc123",
		Author:  User{ID: "9", Name: "Ada"},
	}}
	f.sync.resolveProviderOverride = func(context.Context, int) (Provider, error) { return provider, nil }
	recorded := recordItemChanges(t, f)

	refresh := func() {
		t.Helper()
		if err := f.sync.RefreshItemSCMLink(context.Background(), commitLinkID); err != nil {
			t.Fatalf("refresh commit link: %v", err)
		}
	}

	refresh()
	requireLinkPublishes(t, recorded, 0, "a commit refresh that changes nothing")

	provider.commit.Author.Name = "Ada Lovelace"
	refresh()
	requireLinkPublishes(t, recorded, 1, "an author change")
}
