package services

import (
	"errors"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
)

const (
	pageLinkWorkspace      = 7
	pageLinkOtherWorkspace = 8
	pageLinkLocalMilestone = 1
	pageLinkGlobal         = 2
	pageLinkEditor         = 10
	pageLinkViewer         = 11
)

// fakePageLinkAccess: everyone reads both milestones; only the editor writes.
type fakePageLinkAccess struct{}

func (fakePageLinkAccess) milestone(id int) (*MilestoneResult, error) {
	switch id {
	case pageLinkLocalMilestone:
		workspaceID := pageLinkWorkspace
		return &MilestoneResult{ID: id, WorkspaceID: &workspaceID}, nil
	case pageLinkGlobal:
		return &MilestoneResult{ID: id, IsGlobal: true}, nil
	}
	return nil, repository.ErrNotFound
}

func (f fakePageLinkAccess) AuthorizeMilestoneRead(_, milestoneID int) (*MilestoneResult, error) {
	return f.milestone(milestoneID)
}

func (f fakePageLinkAccess) AuthorizeMilestoneWrite(userID, milestoneID int) (*MilestoneResult, error) {
	milestone, err := f.milestone(milestoneID)
	if err != nil {
		return nil, err
	}
	if userID != pageLinkEditor {
		return nil, ErrPlanningForbidden
	}
	return milestone, nil
}

// fakePageLinkPages: page -> workspace; hidden pages are invisible to all.
type fakePageLinkPages struct {
	workspaceOf map[int]int
	hidden      map[int]bool
}

func (f fakePageLinkPages) Can(_, workspaceID, pageID int, op string) (bool, error) {
	if op != PageOpView {
		return false, nil
	}
	ws, ok := f.workspaceOf[pageID]
	return ok && ws == workspaceID && !f.hidden[pageID], nil
}

func (f fakePageLinkPages) ListVisiblePageIDs(userID, workspaceID int, pageIDs []int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, id := range pageIDs {
		out[id], _ = f.Can(userID, workspaceID, id, PageOpView)
	}
	return out, nil
}

type fakePageLinkStore struct {
	nextID int
	rows   map[int]models.MilestonePageLink
	pages  fakePageLinkPages
}

func (f *fakePageLinkStore) ListByMilestone(milestoneID int) ([]models.MilestonePageLink, error) {
	result := []models.MilestonePageLink{}
	for id := 1; id < f.nextID; id++ {
		if row, ok := f.rows[id]; ok && row.MilestoneID == milestoneID {
			result = append(result, row)
		}
	}
	return result, nil
}

func (f *fakePageLinkStore) GetByID(id int) (*models.MilestonePageLink, error) {
	row, ok := f.rows[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return &row, nil
}

func (f *fakePageLinkStore) Create(milestoneID, pageID, createdBy int) (int, error) {
	for _, row := range f.rows {
		if row.MilestoneID == milestoneID && row.PageID == pageID {
			return 0, repository.ErrDuplicateEntry
		}
	}
	id := f.nextID
	f.nextID++
	f.rows[id] = models.MilestonePageLink{
		ID: id, MilestoneID: milestoneID, PageID: pageID,
		WorkspaceID: f.pages.workspaceOf[pageID], CreatedBy: &createdBy,
	}
	return id, nil
}

func (f *fakePageLinkStore) Delete(id int) error {
	if _, ok := f.rows[id]; !ok {
		return repository.ErrNotFound
	}
	delete(f.rows, id)
	return nil
}

func newPageLinkTestService() (*MilestonePageLinkService, *fakePageLinkStore) {
	pages := fakePageLinkPages{
		workspaceOf: map[int]int{100: pageLinkWorkspace, 101: pageLinkWorkspace, 102: pageLinkWorkspace, 200: pageLinkOtherWorkspace},
		hidden:      map[int]bool{102: true},
	}
	store := &fakePageLinkStore{nextID: 1, rows: map[int]models.MilestonePageLink{}, pages: pages}
	return NewMilestonePageLinkService(store, fakePageLinkAccess{}, pages), store
}

func TestMilestonePageLinkServiceLinksAndUnlinksWorkspacePages(t *testing.T) {
	service, _ := newPageLinkTestService()

	created, err := service.Create(pageLinkEditor, pageLinkLocalMilestone, 100)
	if err != nil {
		t.Fatal(err)
	}
	if created.MilestoneID != pageLinkLocalMilestone || created.PageID != 100 || created.CreatedBy == nil || *created.CreatedBy != pageLinkEditor {
		t.Fatalf("unexpected link: %+v", created)
	}
	if _, err := service.Create(pageLinkEditor, pageLinkLocalMilestone, 100); !errors.Is(err, repository.ErrDuplicateEntry) {
		t.Fatalf("duplicate link: got %v, want ErrDuplicateEntry", err)
	}
	if _, err := service.Create(pageLinkEditor, pageLinkLocalMilestone, 101); err != nil {
		t.Fatal(err)
	}

	// Viewers without edit rights still see the linked pages.
	rows, err := service.List(pageLinkViewer, pageLinkLocalMilestone)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].PageID != 100 || rows[1].PageID != 101 {
		t.Fatalf("unexpected list: %+v", rows)
	}

	if err := service.Delete(pageLinkEditor, pageLinkLocalMilestone, created.ID); err != nil {
		t.Fatal(err)
	}
	rows, err = service.List(pageLinkEditor, pageLinkLocalMilestone)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].PageID != 101 {
		t.Fatalf("expected only page 101 after unlink, got %+v", rows)
	}
}

func TestMilestonePageLinkServiceRejectsGlobalMilestones(t *testing.T) {
	service, _ := newPageLinkTestService()

	if _, err := service.Create(pageLinkEditor, pageLinkGlobal, 100); !errors.Is(err, ErrMilestonePageLinksGlobal) {
		t.Fatalf("create on global: got %v, want ErrMilestonePageLinksGlobal", err)
	}
	if err := service.Delete(pageLinkEditor, pageLinkGlobal, 1); !errors.Is(err, ErrMilestonePageLinksGlobal) {
		t.Fatalf("delete on global: got %v, want ErrMilestonePageLinksGlobal", err)
	}
	rows, err := service.List(pageLinkViewer, pageLinkGlobal)
	if err != nil || len(rows) != 0 {
		t.Fatalf("list on global: got %+v, %v; want empty", rows, err)
	}
}

func TestMilestonePageLinkServiceRequiresEditRights(t *testing.T) {
	service, _ := newPageLinkTestService()
	created, err := service.Create(pageLinkEditor, pageLinkLocalMilestone, 100)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Create(pageLinkViewer, pageLinkLocalMilestone, 101); !errors.Is(err, ErrMilestonePageLinksForbidden) {
		t.Fatalf("viewer create: got %v, want ErrMilestonePageLinksForbidden", err)
	}
	if err := service.Delete(pageLinkViewer, pageLinkLocalMilestone, created.ID); !errors.Is(err, ErrMilestonePageLinksForbidden) {
		t.Fatalf("viewer delete: got %v, want ErrMilestonePageLinksForbidden", err)
	}
	if _, err := service.Create(pageLinkEditor, 99, 100); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("missing milestone: got %v, want ErrNotFound", err)
	}
}

func TestMilestonePageLinkServiceLimitsPagesToTheMilestoneWorkspace(t *testing.T) {
	service, store := newPageLinkTestService()

	for _, pageID := range []int{200, 102, 999} { // other workspace, hidden, missing
		if _, err := service.Create(pageLinkEditor, pageLinkLocalMilestone, pageID); !errors.Is(err, ErrMilestonePageNotFound) {
			t.Fatalf("page %d: got %v, want ErrMilestonePageNotFound", pageID, err)
		}
	}

	// Rows that are not visible (here: a hidden page) are dropped from the list
	// and cannot be unlinked through the milestone.
	hiddenID, _ := store.Create(pageLinkLocalMilestone, 102, pageLinkEditor)
	rows, err := service.List(pageLinkEditor, pageLinkLocalMilestone)
	if err != nil || len(rows) != 0 {
		t.Fatalf("expected hidden page to be filtered, got %+v, %v", rows, err)
	}
	if err := service.Delete(pageLinkEditor, pageLinkLocalMilestone, hiddenID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("hidden page delete: got %v, want ErrNotFound", err)
	}

	// A link on another milestone reads as not found.
	otherID, _ := store.Create(3, 100, pageLinkEditor)
	if err := service.Delete(pageLinkEditor, pageLinkLocalMilestone, otherID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("foreign link delete: got %v, want ErrNotFound", err)
	}
}

// allowAllWorkspacePermissions grants every workspace permission, so only the
// endpoint type decides what the generic link filters keep.
type allowAllWorkspacePermissions struct{}

func (allowAllWorkspacePermissions) HasWorkspacePermission(_, _ int, _ string) (bool, error) {
	return true, nil
}

func (allowAllWorkspacePermissions) AccessibleWorkspaceIDs(int) ([]int, error) { return nil, nil }

func (allowAllWorkspacePermissions) AccessibleWorkspaceIDKeys(int) ([]repository.IDKey, error) {
	return nil, nil
}

// TestMilestonePageLinksStoredInItemLinks covers the SQLite repository, the
// cleanup when a milestone is deleted, and that the generic link routes leave
// milestone page links alone.
func TestMilestonePageLinksStoredInItemLinks(t *testing.T) {
	planning, db := newMilestoneReleaseTestService(t)
	defer func() { _ = db.Close() }()
	if _, err := db.ExecWrite(`INSERT INTO users (id, email, username, first_name, last_name) VALUES (1, 'ada@example.test', 'ada', 'Ada', 'Lovelace')`); err != nil {
		t.Fatal(err)
	}
	var workspaceID, pageID, milestoneID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Hub', 'HUB') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO pages (workspace_id, title, slug, created_by) VALUES (?, 'Spec', 'spec', 1) RETURNING id`, workspaceID).Scan(&pageID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO milestones (name, description, status, is_global, workspace_id) VALUES ('Local hub', '', 'planning', false, ?) RETURNING id`, workspaceID).Scan(&milestoneID); err != nil {
		t.Fatal(err)
	}

	repo := repository.NewMilestonePageLinkRepository(db)
	linkID, err := repo.Create(milestoneID, pageID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(milestoneID, pageID, 1); !errors.Is(err, repository.ErrDuplicateEntry) {
		t.Fatalf("duplicate: got %v, want ErrDuplicateEntry", err)
	}
	rows, err := repo.ListByMilestone(milestoneID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one link, got %+v", rows)
	}
	got := rows[0]
	if got.ID != linkID || got.PageID != pageID || got.PageTitle != "Spec" || got.WorkspaceID != workspaceID ||
		got.CreatedBy == nil || *got.CreatedBy != 1 || got.CreatedByName != "Ada Lovelace" || got.CreatedAt.IsZero() {
		t.Fatalf("unexpected link row: %+v", got)
	}
	var sourceType, targetType string
	if err := db.QueryRow(`SELECT source_type, target_type FROM item_links WHERE id = ?`, linkID).Scan(&sourceType, &targetType); err != nil {
		t.Fatal(err)
	}
	if sourceType != "milestone" || targetType != "page" {
		t.Fatalf("stored as %s -> %s, want milestone -> page", sourceType, targetType)
	}

	// The generic routes leave the row alone: the page's link list drops it
	// (no backlink) and the generic delete treats it as not found.
	links := NewItemLinkService(db).
		WithPermissionService(allowAllWorkspacePermissions{}).
		WithPagePermissionChecker(fakePageLinkPages{workspaceOf: map[int]int{pageID: workspaceID}})
	pageLinks, err := links.getLinksWhere("target_type = ? AND target_id = ?", "page", pageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pageLinks) != 1 || len(links.FilterLinksForUser(1, pageLinks)) != 0 {
		t.Fatalf("expected the page link list to drop the milestone link, got %+v", pageLinks)
	}
	if err := links.DeleteLinkWithChecks(1, linkID); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("generic delete: got %v, want ErrLinkNotFound", err)
	}

	if err := planning.DeleteMilestone(milestoneID); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_links WHERE source_type = 'milestone' AND source_id = ?`, milestoneID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("expected milestone page links removed with the milestone, %d remain", remaining)
	}
}
