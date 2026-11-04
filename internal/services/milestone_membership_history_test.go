package services

import (
	"sort"
	"strconv"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
)

// TestMilestoneActivityKeepsMembershipHistory covers item creation and the
// workspace move end to end on SQLite: creating an item in
// milestones records a milestones history row, the Activity feed shows one
// "item added" for it, a move keeps global memberships and records "item
// removed" for the workspace milestone it leaves, and items created before
// the history row existed still show through the creation workaround.
func TestMilestoneActivityKeepsMembershipHistory(t *testing.T) {
	_, db := newMilestoneReleaseTestService(t)
	defer func() { _ = db.Close() }()
	const ada = 1
	if _, err := db.ExecWrite(`INSERT INTO users (id, email, username, first_name, last_name) VALUES (?, 'ada@example.test', 'ada', 'Ada', 'Lovelace')`, ada); err != nil {
		t.Fatal(err)
	}
	hub := activityInsert(t, db, `INSERT INTO workspaces (name, key) VALUES ('Hub', 'HUB')`)
	dest := activityInsert(t, db, `INSERT INTO workspaces (name, key) VALUES ('Dest', 'DST')`)
	local := activityInsert(t, db, `INSERT INTO milestones (name, description, status, is_global, workspace_id) VALUES ('Hub release', '', 'planning', false, ?)`, hub)
	global := insertGlobalMilestone(t, db, "Company goal")

	created, err := CreateItem(db, ItemCreationParams{
		WorkspaceID: hub, Title: "Created in milestones", CreatorID: intPtr(ada),
		MilestoneIDs: []int{local, global},
	})
	if err != nil {
		t.Fatal(err)
	}
	itemID := int(created)
	sorted := []int{local, global}
	if global < local {
		sorted = []int{global, local}
	}
	var oldValue, newValue string
	if err := db.QueryRow(`SELECT COALESCE(old_value, ''), COALESCE(new_value, '') FROM item_history WHERE item_id = ? AND field_name = 'milestones'`, itemID).Scan(&oldValue, &newValue); err != nil {
		t.Fatal(err)
	}
	if want := strconv.Itoa(sorted[0]) + "," + strconv.Itoa(sorted[1]); oldValue != "" || newValue != want {
		t.Fatalf("creation milestones row: old %q new %q, want \"\" and %q", oldValue, newValue, want)
	}

	// An item created in both milestones before creation recorded history.
	var itemTypeID, statusID int
	if err := db.QueryRow(`SELECT item_type_id, status_id FROM items WHERE id = ?`, itemID).Scan(&itemTypeID, &statusID); err != nil {
		t.Fatal(err)
	}
	legacy := activityInsert(t, db, `INSERT INTO items (workspace_id, workspace_item_number, item_type_id, title, description, frac_index, status_id, creator_id)
		VALUES (?, 99, ?, 'Legacy', '', 'zz', ?, ?)`, hub, itemTypeID, statusID, ada)
	for _, milestoneID := range []int{local, global} {
		if _, err := db.ExecWrite(`INSERT INTO item_milestones (item_id, milestone_id) VALUES (?, ?)`, legacy, milestoneID); err != nil {
			t.Fatal(err)
		}
	}

	activity := NewMilestoneActivityService(
		repository.NewMilestoneActivityRepository(db),
		activityAccess{db: db},
		activityWorkspaces{ada: {hub, dest}},
		activityPages{},
	)
	feed := func(milestoneID int) map[int][]string {
		t.Helper()
		entries, _, err := activity.List(ada, milestoneID, MilestoneActivityListParams{Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		byItem := map[int][]string{}
		for _, entry := range entries {
			if entry.Item == nil {
				continue
			}
			if entry.ActorKind != models.MilestoneActivityActorUser || entry.ActorName != "Ada Lovelace" {
				t.Fatalf("unexpected actor on %+v", entry)
			}
			byItem[entry.Item.ID] = append(byItem[entry.Item.ID], entry.Type)
		}
		return byItem
	}
	expect := func(milestoneID int, want map[int][]string) {
		t.Helper()
		got := feed(milestoneID)
		for item, types := range want {
			sort.Strings(got[item])
			sort.Strings(types)
			if len(got[item]) != len(types) {
				t.Fatalf("milestone %d item %d: got %v, want %v (feed %v)", milestoneID, item, got[item], types, got)
			}
			for i := range types {
				if got[item][i] != types[i] {
					t.Fatalf("milestone %d item %d: got %v, want %v", milestoneID, item, got[item], types)
				}
			}
		}
		if len(got) != len(want) {
			t.Fatalf("milestone %d: feed items %v, want %v", milestoneID, got, want)
		}
	}
	added, removed := models.MilestoneActivityItemAdded, models.MilestoneActivityItemRemoved
	for _, milestoneID := range []int{local, global} {
		expect(milestoneID, map[int][]string{itemID: {added}, legacy: {added}})
	}

	moves := NewItemWorkspaceMoveService(db)
	move := func(id int) {
		t.Helper()
		preview, err := moves.Preview(id, ada, ItemWorkspaceMoveInput{DestinationWorkspaceID: dest})
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range preview.Fields {
			if field.Field == "milestones" && (field.Action != "partial" || field.To != "Company goal") {
				t.Fatalf("milestones preview: %+v", field)
			}
		}
		if _, err := moves.Move(id, ada, ItemWorkspaceMoveInput{
			DestinationWorkspaceID: dest, TargetItemTypeID: preview.TargetItemTypeID,
			TargetStatusID: preview.TargetStatusID, TargetPriorityID: preview.TargetPriorityID,
		}); err != nil {
			t.Fatal(err)
		}
		ids, err := repository.NewMilestoneAttachRepository(db).MilestoneIDsForItem(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 1 || ids[0] != global {
			t.Fatalf("item %d milestones after move: %v, want only the global milestone %d", id, ids, global)
		}
	}
	move(itemID)
	move(legacy)

	expect(local, map[int][]string{itemID: {removed, added}, legacy: {removed, added}})
	expect(global, map[int][]string{itemID: {added}, legacy: {added}})
}
