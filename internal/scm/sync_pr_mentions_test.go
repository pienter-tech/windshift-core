package scm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"testing"

	"windshift/internal/models"
	"windshift/internal/services"
)

// prMentionsFixture extends the PR-merged fixture (item PRM-1 linked to PR #7
// by its title) with items PRM-2 and PRM-3, and records action events.
type prMentionsFixture struct {
	*prMergedFixture
	events *recordedActionEvents
	item2  int
	item3  int
}

func newPRMentionsFixture(t *testing.T) *prMentionsFixture {
	t.Helper()
	base := newPRMergedFixture(t)
	f := &prMentionsFixture{prMergedFixture: base, events: &recordedActionEvents{}}
	f.sync.SetActionEvents(f.events)
	f.sync.SetDurableActionEvents(f.events)
	for number, id := range map[int]*int{2: &f.item2, 3: &f.item3} {
		result, err := base.db.Exec(`INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, ?, 'Stacked', ?)`,
			base.workspaceID, number, fmt.Sprintf("a%d", number))
		if err != nil {
			t.Fatal(err)
		}
		lastID, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		*id = int(lastID)
	}
	return f
}

// eventItems returns the items that received an event of the given type.
func (f *prMentionsFixture) eventItems(eventType models.ActionTriggerType) []int {
	var items []int
	for _, event := range f.events.ofType(eventType) {
		items = append(items, event.ItemID)
	}
	slices.Sort(items)
	return items
}

func requireEventItems(t *testing.T, f *prMentionsFixture, eventType models.ActionTriggerType, want ...int) {
	t.Helper()
	slices.Sort(want)
	if got := f.eventItems(eventType); !slices.Equal(got, want) {
		t.Fatalf("%s events for items %v, want %v", eventType, got, want)
	}
}

// prLink reads the item's link to the fixture PR; found is false without one.
func (f *prMentionsFixture) prLink(t *testing.T, itemID int) (id int, mention, found bool) {
	t.Helper()
	err := f.db.QueryRow(`
		SELECT id, is_mention FROM item_scm_links
		WHERE item_id = ? AND workspace_repository_id = ? AND link_type = 'pull_request' AND external_id = ?
	`, itemID, f.repoID, strconv.Itoa(f.provider.pr.Number)).Scan(&id, &mention)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return id, mention, true
}

func TestStackedPullRequestMergeOnlyMovesItsHeadBranchItem(t *testing.T) {
	f := newPRMentionsFixture(t)
	f.provider.pr.Title = "Ship it"
	f.provider.pr.HeadBranch = "PRM-1-ship-it"
	f.provider.pr.Body = "First of a stack: PRM-2 and PRM-3 follow on this branch."

	f.repoSync(t)
	f.merge()
	f.repoSync(t)

	requireEventItems(t, f, models.ActionTriggerSCMPRMerged, f.itemID)
	requireEventItems(t, f, models.ActionTriggerSCMPRLinked)
	for _, itemID := range []int{f.item2, f.item3} {
		if _, mention, found := f.prLink(t, itemID); !found || !mention {
			t.Fatalf("item %d link found=%v mention=%v, want a mention link", itemID, found, mention)
		}
	}
}

func TestRefreshOfAMentionLinkDoesNotEmitPRMerged(t *testing.T) {
	f := newPRMentionsFixture(t)
	f.provider.pr.Body = "Prepares PRM-2."
	f.repoSync(t)
	mentionID, mention, found := f.prLink(t, f.item2)
	if !found || !mention {
		t.Fatalf("item 2 link found=%v mention=%v, want a mention link", found, mention)
	}

	f.merge()
	if err := f.sync.RefreshItemSCMLink(context.Background(), mentionID); err != nil {
		t.Fatal(err)
	}
	f.refresh(t)
	f.repoSync(t)

	requireEventItems(t, f, models.ActionTriggerSCMPRMerged, f.itemID)
}

func TestBodyOnlyPullRequestOwnsItsBodyKeys(t *testing.T) {
	f := newPRMentionsFixture(t)
	f.provider.pr.Number = 8
	f.provider.pr.URL = "https://git.example/pienter/app/pulls/8"
	f.provider.pr.Title = "Ship the stack"
	f.provider.pr.HeadBranch = "feature/stack"
	f.provider.pr.Body = "Implements PRM-2 and PRM-3."

	f.repoSync(t)
	requireEventItems(t, f, models.ActionTriggerSCMPRLinked, f.item2, f.item3)
	f.merge()
	f.repoSync(t)

	requireEventItems(t, f, models.ActionTriggerSCMPRMerged, f.item2, f.item3)
	for _, itemID := range []int{f.item2, f.item3} {
		if _, mention, found := f.prLink(t, itemID); !found || mention {
			t.Fatalf("item %d link found=%v mention=%v, want an own link", itemID, found, mention)
		}
	}
}

func TestSyncRecomputesMentionsWhenThePullRequestChanges(t *testing.T) {
	f := newPRMentionsFixture(t)
	f.provider.pr.Title = "Ship the stack"
	f.provider.pr.Body = "Implements PRM-2."

	f.repoSync(t) // body-only: PRM-2 is the PR's own item
	if _, mention, found := f.prLink(t, f.item2); !found || mention {
		t.Fatalf("item 2 link found=%v mention=%v, want an own link", found, mention)
	}

	f.provider.pr.HeadBranch = "PRM-3-ship"
	f.repoSync(t) // the branch now names PRM-3, so PRM-2 is only mentioned
	if _, mention, _ := f.prLink(t, f.item2); !mention {
		t.Fatal("item 2 link is still an own link after the branch named another item")
	}
	requireEventItems(t, f, models.ActionTriggerSCMPRLinked, f.item2, f.item3)

	f.provider.pr.Title = "PRM-2 Ship the stack"
	f.repoSync(t) // the title names PRM-2: own again, which links it anew
	if _, mention, _ := f.prLink(t, f.item2); mention {
		t.Fatal("item 2 link is still a mention after the title named it")
	}
	requireEventItems(t, f, models.ActionTriggerSCMPRLinked, f.item2, f.item2, f.item3)
}

func TestSyncRemovesDetectedLinksThePullRequestNoLongerNames(t *testing.T) {
	f := newPRMentionsFixture(t)
	f.provider.pr.Body = "Also touches PRM-2."
	if _, err := f.db.Exec(`INSERT INTO item_scm_links (item_id, workspace_repository_id, link_type, external_id, detection_source) VALUES (?, ?, 'pull_request', '7', 'manual')`,
		f.item3, f.repoID); err != nil {
		t.Fatal(err)
	}
	f.repoSync(t)
	if _, _, found := f.prLink(t, f.item2); !found {
		t.Fatal("item 2 is not linked to the PR whose body names it")
	}

	recorded := &recordedItemChanges{itemID: f.item2}
	services.SetItemChangePublisher(recorded)
	t.Cleanup(func() { services.SetItemChangePublisher(nil) })

	f.provider.pr.Body = "Touches nothing else."
	f.repoSync(t)

	if _, _, found := f.prLink(t, f.item2); found {
		t.Fatal("item 2 link survived the key's removal from the PR body")
	}
	requireLinkPublishes(t, recorded, 1, "removing the stale link")
	if _, _, found := f.prLink(t, f.itemID); !found {
		t.Fatal("item 1 link was removed although the PR title names it")
	}
	if _, _, found := f.prLink(t, f.item3); !found {
		t.Fatal("the manual link was removed by the sync")
	}
}

func TestSyncDoesNotRecreateADeletedDetectedLink(t *testing.T) {
	f := newPRMentionsFixture(t)
	f.provider.pr.Body = "Also touches PRM-2."
	f.repoSync(t)
	linkID, _, found := f.prLink(t, f.item2)
	if !found {
		t.Fatal("item 2 is not linked to the PR whose body names it")
	}

	// What the item page's delete does.
	if _, err := f.db.Exec(`DELETE FROM item_scm_links WHERE id = ?`, linkID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO item_scm_link_dismissals (item_id, workspace_repository_id, link_type, external_id) VALUES (?, ?, 'pull_request', '7')`,
		f.item2, f.repoID); err != nil {
		t.Fatal(err)
	}

	f.repoSync(t)
	if _, _, found := f.prLink(t, f.item2); found {
		t.Fatal("the sync re-created a deleted link")
	}

	// Once the PR stops naming the key the dismissal lapses, so naming it
	// again links the item again.
	f.provider.pr.Body = "Touches nothing else."
	f.repoSync(t)
	f.provider.pr.Body = "Also touches PRM-2 after all."
	f.repoSync(t)
	if _, _, found := f.prLink(t, f.item2); !found {
		t.Fatal("item 2 was not linked again after the PR named it anew")
	}
}

func TestSyncDoesNotRecreateDeletedBranchAndCommitLinks(t *testing.T) {
	f := newPRMentionsFixture(t)
	ctx := context.Background()
	for _, link := range []struct {
		linkType models.SCMLinkType
		external string
	}{
		{models.SCMLinkTypeBranch, "PRM-2-branch"},
		{models.SCMLinkTypeCommit, "abc123"},
	} {
		if _, err := f.db.Exec(`INSERT INTO item_scm_link_dismissals (item_id, workspace_repository_id, link_type, external_id) VALUES (?, ?, ?, ?)`,
			f.item2, f.repoID, link.linkType, link.external); err != nil {
			t.Fatal(err)
		}
		if err := f.sync.upsertItemSCMLink(ctx, f.item2, f.repoID, link.linkType, link.external, "", link.external, "", "", "", string(DetectionSourceBranchName)); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM item_scm_links WHERE item_id = ? AND link_type = ?`, f.item2, link.linkType).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("the sync re-created a deleted %s link", link.linkType)
		}
	}
}
