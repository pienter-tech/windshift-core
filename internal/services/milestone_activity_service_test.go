package services

import (
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

// activityAccess lets everyone read every existing milestone.
type activityAccess struct{ db database.Database }

func (a activityAccess) AuthorizeMilestoneRead(_, milestoneID int) (*MilestoneResult, error) {
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM milestones WHERE id = ?`, milestoneID).Scan(&count); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, repository.ErrNotFound
	}
	return &MilestoneResult{ID: milestoneID, IsGlobal: true}, nil
}

// activityWorkspaces maps a user to the workspaces whose items they may view.
type activityWorkspaces map[int][]int

func (w activityWorkspaces) AccessibleWorkspaceIDs(userID int) ([]int, error) {
	return w[userID], nil
}

type activityPages []models.MilestonePageLinkEvent

func (p activityPages) History(_, _ int) ([]models.MilestonePageLinkEvent, error) {
	return p, nil
}

func activityInsert(t *testing.T, db database.Database, query string, args ...any) int {
	t.Helper()
	var id int
	if err := db.QueryRow(query+" RETURNING id", args...).Scan(&id); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return id
}

type activityFixture struct {
	db                     database.Database
	planning               *PlanningService
	milestone, other       int
	createdIn, added, left int
	hidden                 int
	openStatus, doneStatus int
	openName, doneName     string
	base                   time.Time
}

const (
	activityAda     = 1
	activityBob     = 2
	activityViewer  = 3 // sees workspace HUB only
	activityAllSeer = 4 // sees HUB and SEC
)

func newActivityFixture(t *testing.T) *activityFixture {
	t.Helper()
	planning, db := newMilestoneReleaseTestService(t)
	t.Cleanup(func() { _ = db.Close() })
	f := &activityFixture{db: db, planning: planning, base: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)}
	for _, user := range []struct {
		id                    int
		username, first, last string
	}{{activityAda, "ada", "Ada", "Lovelace"}, {activityBob, "bob", "Bob", "Builder"}} {
		if _, err := db.ExecWrite(`INSERT INTO users (id, email, username, first_name, last_name) VALUES (?, ?, ?, ?, ?)`,
			user.id, user.username+"@example.test", user.username, user.first, user.last); err != nil {
			t.Fatal(err)
		}
	}
	hub := activityInsert(t, db, `INSERT INTO workspaces (name, key) VALUES ('Hub', 'HUB')`)
	sec := activityInsert(t, db, `INSERT INTO workspaces (name, key) VALUES ('Secret', 'SEC')`)
	f.milestone = insertGlobalMilestone(t, db, "Hub goal")
	f.other = insertGlobalMilestone(t, db, "Other goal")
	if err := db.QueryRow(`SELECT id, name FROM statuses ORDER BY id LIMIT 1`).Scan(&f.openStatus, &f.openName); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id, name FROM statuses ORDER BY id DESC LIMIT 1`).Scan(&f.doneStatus, &f.doneName); err != nil {
		t.Fatal(err)
	}
	at := func(hours int) time.Time { return f.base.Add(time.Duration(hours) * time.Hour) }
	item := func(workspaceID, number int, title string, created time.Time) int {
		return activityInsert(t, db, `INSERT INTO items (workspace_id, workspace_item_number, title, description, frac_index, status_id, creator_id, created_at, updated_at)
			VALUES (?, ?, ?, '', ?, ?, ?, ?, ?)`, workspaceID, number, title, "a"+title, f.openStatus, activityAda, created, created)
	}
	member := func(itemID, milestoneID int) {
		if _, err := db.ExecWrite(`INSERT INTO item_milestones (item_id, milestone_id) VALUES (?, ?)`, itemID, milestoneID); err != nil {
			t.Fatal(err)
		}
	}
	history := func(itemID, userID int, field, oldValue, newValue string, changed time.Time) {
		if _, err := db.ExecWrite(`INSERT INTO item_history (item_id, user_id, field_name, old_value, new_value, changed_at) VALUES (?, ?, ?, ?, ?, ?)`,
			itemID, userID, field, oldValue, newValue, changed); err != nil {
			t.Fatal(err)
		}
	}
	csv := func(ids ...int) string {
		out := ""
		for i, id := range ids {
			if i > 0 {
				out += ","
			}
			out += strconv.Itoa(id)
		}
		return out
	}

	// HUB-1 was created directly in the milestone: no milestones history.
	f.createdIn = item(hub, 1, "Created in", at(0))
	member(f.createdIn, f.milestone)
	// HUB-2 was added later, changed status, then also joined another milestone.
	f.added = item(hub, 2, "Added", at(0))
	member(f.added, f.milestone)
	member(f.added, f.other)
	history(f.added, activityAda, "status_id", "", strconv.Itoa(f.openStatus), at(0)) // creation row: not an event
	history(f.added, activityBob, "milestones", "", csv(f.milestone), at(2))
	history(f.added, activityAda, "status_id", strconv.Itoa(f.openStatus), strconv.Itoa(f.doneStatus), at(6))
	history(f.added, activityAda, "milestones", csv(f.milestone), csv(f.milestone, f.other), at(7)) // still a member: not an event
	// HUB-3 was added and removed again; it is no longer a member.
	f.left = item(hub, 3, "Left", at(0))
	history(f.left, activityAda, "milestones", "", csv(f.milestone), at(3))
	history(f.left, activityBob, "milestones", csv(f.milestone), "", at(4))
	// SEC-1 is a member in a workspace only some viewers can access.
	f.hidden = item(sec, 1, "Hidden", at(0))
	member(f.hidden, f.milestone)
	history(f.hidden, activityAda, "milestones", "", csv(f.milestone), at(5))
	if _, err := db.ExecWrite(`INSERT INTO comments (item_id, author_id, content, created_at, updated_at) VALUES (?, ?, 'x', ?, ?)`, f.hidden, activityBob, at(8), at(8)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecWrite(`INSERT INTO comments (item_id, author_id, content, created_at, updated_at) VALUES (?, ?, 'x', ?, ?)`, f.createdIn, activityBob, at(9), at(9)); err != nil {
		t.Fatal(err)
	}
	// A comment on an item of another milestone never shows up.
	stranger := item(hub, 4, "Stranger", at(0))
	member(stranger, f.other)
	if _, err := db.ExecWrite(`INSERT INTO comments (item_id, author_id, content, created_at, updated_at) VALUES (?, ?, 'x', ?, ?)`, stranger, activityBob, at(9), at(9)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecWrite(`INSERT INTO milestone_comments (milestone_id, author_id, content, created_at, updated_at) VALUES (?, ?, 'hello', ?, ?)`, f.milestone, activityAda, at(10), at(10)); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *activityFixture) service(pages activityPages) *MilestoneActivityService {
	return NewMilestoneActivityService(
		repository.NewMilestoneActivityRepository(f.db),
		activityAccess{db: f.db},
		activityWorkspaces{activityViewer: {1}, activityAllSeer: {1, 2}},
		pages,
	)
}

func activityTypes(entries []models.MilestoneActivity) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.Type
		if entry.Item != nil {
			out[i] += " " + entry.Item.Key
		}
	}
	return out
}

func TestMilestoneActivityFeedSourcesOrderAndVisibility(t *testing.T) {
	f := newActivityFixture(t)
	adaID := activityAda
	pages := activityPages{{
		ID: 5, MilestoneID: f.milestone, PageID: 42, PageTitle: "Spec", WorkspaceID: 1,
		UserID: &adaID, UserName: "Ada Lovelace", OccurredAt: f.base.Add(11 * time.Hour),
	}}

	// Milestone edits by Bob, recorded by the update path.
	target := "2026-12-01"
	if _, err := f.planning.UpdateMilestone(UpdateMilestoneParams{
		ID: f.milestone, Name: "Hub goal", Description: "Now with **Markdown**", TargetDate: &target,
		Status: "in-progress", AuditActor: &AuditActor{UserID: activityBob},
	}); err != nil {
		t.Fatal(err)
	}

	service := f.service(pages)
	entries, hasMore, err := service.List(activityViewer, f.milestone, MilestoneActivityListParams{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		models.MilestoneActivityMilestoneTargetDateChange,
		models.MilestoneActivityMilestoneStatusChanged,
		models.MilestoneActivityMilestoneDescriptionEdit,
		models.MilestoneActivityMilestonePageLinked,
		models.MilestoneActivityMilestoneCommentAdded,
		models.MilestoneActivityItemCommentAdded + " HUB-1",
		models.MilestoneActivityItemStatusChanged + " HUB-2",
		models.MilestoneActivityItemRemoved + " HUB-3",
		models.MilestoneActivityItemAdded + " HUB-3",
		models.MilestoneActivityItemAdded + " HUB-2",
		models.MilestoneActivityItemAdded + " HUB-1",
	}
	got := activityTypes(entries)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("feed:\n got %v\nwant %v", got, want)
	}
	if hasMore {
		t.Fatal("hasMore = true on the only page")
	}

	byType := map[string]models.MilestoneActivity{}
	for _, entry := range entries {
		key := entry.Type
		if entry.Item != nil {
			key += " " + entry.Item.Key
		}
		byType[key] = entry
	}
	status := byType[models.MilestoneActivityMilestoneStatusChanged]
	if status.ActorKind != models.MilestoneActivityActorUser || status.ActorID == nil || *status.ActorID != activityBob ||
		status.ActorName != "Bob Builder" || status.OldValue == nil || *status.OldValue != "planning" ||
		status.NewValue == nil || *status.NewValue != "in-progress" {
		t.Fatalf("unexpected milestone status entry: %+v", status)
	}
	targetEntry := byType[models.MilestoneActivityMilestoneTargetDateChange]
	if targetEntry.OldValue != nil || targetEntry.NewValue == nil || *targetEntry.NewValue != target {
		t.Fatalf("unexpected target date entry: %+v", targetEntry)
	}
	if description := byType[models.MilestoneActivityMilestoneDescriptionEdit]; description.OldValue != nil || description.NewValue != nil {
		t.Fatalf("description entries carry no values: %+v", description)
	}
	itemStatus := byType[models.MilestoneActivityItemStatusChanged+" HUB-2"]
	if itemStatus.OldValue == nil || *itemStatus.OldValue != f.openName || itemStatus.NewValue == nil || *itemStatus.NewValue != f.doneName ||
		itemStatus.ActorName != "Ada Lovelace" || itemStatus.Item.ID != f.added || itemStatus.Item.Title != "Added" {
		t.Fatalf("unexpected item status entry: %+v (item %+v)", itemStatus, itemStatus.Item)
	}
	removed := byType[models.MilestoneActivityItemRemoved+" HUB-3"]
	if removed.ActorName != "Bob Builder" || !removed.OccurredAt.Equal(f.base.Add(4*time.Hour)) {
		t.Fatalf("unexpected removal entry: %+v", removed)
	}
	createdIn := byType[models.MilestoneActivityItemAdded+" HUB-1"]
	if createdIn.ActorName != "Ada Lovelace" || !createdIn.OccurredAt.Equal(f.base) || createdIn.ID != "item_created:"+strconv.Itoa(f.createdIn) {
		t.Fatalf("unexpected created-in entry: %+v", createdIn)
	}
	page := byType[models.MilestoneActivityMilestonePageLinked]
	if page.Page == nil || page.Page.ID != 42 || page.Page.Title != "Spec" || page.ActorName != "Ada Lovelace" {
		t.Fatalf("unexpected page entry: %+v", page)
	}
	comment := byType[models.MilestoneActivityItemCommentAdded+" HUB-1"]
	if comment.CommentID == nil || comment.ActorName != "Bob Builder" {
		t.Fatalf("unexpected item comment entry: %+v", comment)
	}

	// A viewer with access to SEC also sees its item's events.
	all, allHasMore, err := service.List(activityAllSeer, f.milestone, MilestoneActivityListParams{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if allHasMore || len(all) != len(want)+2 {
		t.Fatalf("full-access feed has %d entries (hasMore %v), want %d", len(all), allHasMore, len(want)+2)
	}
	gotAll := activityTypes(all)
	if gotAll[6] != models.MilestoneActivityItemCommentAdded+" SEC-1" || gotAll[8] != models.MilestoneActivityItemAdded+" SEC-1" {
		t.Fatalf("full-access feed: %v", gotAll)
	}

	// Paging slices the same ordering.
	page2, page2HasMore, err := service.List(activityViewer, f.milestone, MilestoneActivityListParams{Limit: 4, Offset: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !page2HasMore || !reflect.DeepEqual(activityTypes(page2), want[4:8]) {
		t.Fatalf("page 2: got %v (hasMore %v), want %v", activityTypes(page2), page2HasMore, want[4:8])
	}
	page3, page3HasMore, err := service.List(activityViewer, f.milestone, MilestoneActivityListParams{Limit: 4, Offset: 8})
	if err != nil {
		t.Fatal(err)
	}
	if page3HasMore || !reflect.DeepEqual(activityTypes(page3), want[8:]) {
		t.Fatalf("last page: got %v (hasMore %v), want %v", activityTypes(page3), page3HasMore, want[8:])
	}
	// A page that ends exactly at the last entry has nothing after it.
	exact, exactHasMore, err := service.List(activityViewer, f.milestone, MilestoneActivityListParams{Limit: len(want)})
	if err != nil || exactHasMore || len(exact) != len(want) {
		t.Fatalf("exact page: %d entries, hasMore %v, err %v", len(exact), exactHasMore, err)
	}
	// The entry after the page is a linked page, which the store does not read.
	short, shortHasMore, err := service.List(activityViewer, f.milestone, MilestoneActivityListParams{Limit: 3})
	if err != nil || !shortHasMore || !reflect.DeepEqual(activityTypes(short), want[:3]) {
		t.Fatalf("first page: got %v (hasMore %v, err %v), want %v", activityTypes(short), shortHasMore, err, want[:3])
	}
	beyond, _, err := service.List(activityViewer, f.milestone, MilestoneActivityListParams{Limit: 4, Offset: 40})
	if err != nil || len(beyond) != 0 {
		t.Fatalf("beyond the end: %v %v", beyond, err)
	}

	// No accessible workspace: only milestone events remain.
	none, noneHasMore, err := service.List(99, f.milestone, MilestoneActivityListParams{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if noneHasMore || len(none) != 5 {
		t.Fatalf("no-workspace feed: %v", activityTypes(none))
	}

	if _, _, err := service.List(activityViewer, 9999, MilestoneActivityListParams{Limit: 50}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("missing milestone: got %v, want ErrNotFound", err)
	}
}

func TestMilestoneHistoryRecordsOnlyChangedFields(t *testing.T) {
	f := newActivityFixture(t)
	count := func() int {
		var n int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM milestone_history WHERE milestone_id = ?`, f.milestone).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// Renaming changes no tracked field.
	if _, err := f.planning.UpdateMilestone(UpdateMilestoneParams{ID: f.milestone, Name: "Renamed", Status: "planning"}); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatalf("rename recorded %d history rows", n)
	}
	// A status change without an audit actor is a system change.
	if _, err := f.planning.UpdateMilestone(UpdateMilestoneParams{ID: f.milestone, Name: "Renamed", Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	var field string
	var userID *int
	if err := f.db.QueryRow(`SELECT field_name, user_id FROM milestone_history WHERE milestone_id = ?`, f.milestone).Scan(&field, &userID); err != nil {
		t.Fatal(err)
	}
	if field != repository.MilestoneHistoryStatus || userID != nil {
		t.Fatalf("got %s by %v, want a status row with no actor", field, userID)
	}
	entries, _, err := f.service(nil).List(activityViewer, f.milestone, MilestoneActivityListParams{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Type != models.MilestoneActivityMilestoneStatusChanged || entries[0].ActorKind != models.MilestoneActivityActorSystem {
		t.Fatalf("unexpected newest entry: %+v", entries)
	}
	// An update in the wrong scope changes and records nothing.
	workspaceID := 1
	if _, err := f.planning.UpdateMilestone(UpdateMilestoneParams{ID: f.milestone, Name: "Renamed", Status: "planning", WorkspaceID: &workspaceID}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("cross-scope update: got %v, want ErrNotFound", err)
	}
	if n := count(); n != 1 {
		t.Fatalf("cross-scope update recorded history (%d rows)", n)
	}

	// Automation and release flows record their status changes too.
	local := activityInsert(t, f.db, `INSERT INTO milestones (name, description, status, is_global, workspace_id) VALUES ('Local', '', 'planning', false, 1)`)
	if err := f.planning.SetMilestoneStatus(local, 1, "in-progress"); err != nil {
		t.Fatal(err)
	}
	if err := f.planning.SetMilestoneStatus(local, 1, "in-progress"); err != nil {
		t.Fatal(err)
	}
	ada := activityAda
	if _, err := f.planning.ReleaseMilestone(ReleaseMilestoneParams{ID: local, TagName: "v1", CreatedBy: &ada}); err != nil {
		t.Fatal(err)
	}
	rows, err := f.db.Query(`SELECT old_value, new_value, user_id FROM milestone_history WHERE milestone_id = ? ORDER BY id`, local)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var oldValue, newValue string
		var actor *int
		if err := rows.Scan(&oldValue, &newValue, &actor); err != nil {
			t.Fatal(err)
		}
		entry := oldValue + "->" + newValue
		if actor != nil {
			entry += " by " + strconv.Itoa(*actor)
		}
		got = append(got, entry)
	}
	want := []string{"planning->in-progress", "in-progress->completed by 1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("local milestone history: got %v, want %v", got, want)
	}
}
