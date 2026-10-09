package services

import (
	"errors"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
)

// fakeMilestoneAccess lets readers view only the listed milestones.
type fakeMilestoneAccess struct {
	readable map[int]bool
}

func (f fakeMilestoneAccess) AuthorizeMilestoneRead(_, milestoneID int) (*MilestoneResult, error) {
	if !f.readable[milestoneID] {
		return nil, ErrPlanningForbidden
	}
	return &MilestoneResult{ID: milestoneID}, nil
}

type fakeMilestoneCommentStore struct {
	nextID int
	rows   map[int]models.MilestoneComment
}

func newFakeMilestoneCommentStore() *fakeMilestoneCommentStore {
	return &fakeMilestoneCommentStore{nextID: 1, rows: map[int]models.MilestoneComment{}}
}

func (f *fakeMilestoneCommentStore) ListByMilestone(milestoneID, _, _ int, _ bool) ([]models.MilestoneComment, error) {
	result := []models.MilestoneComment{}
	for id := 1; id < f.nextID; id++ {
		if row, ok := f.rows[id]; ok && row.MilestoneID == milestoneID {
			result = append(result, row)
		}
	}
	return result, nil
}

func (f *fakeMilestoneCommentStore) CountByMilestone(milestoneID int) (int, error) {
	rows, _ := f.ListByMilestone(milestoneID, 0, 0, false)
	return len(rows), nil
}

func (f *fakeMilestoneCommentStore) GetByID(id int) (*models.MilestoneComment, error) {
	row, ok := f.rows[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return &row, nil
}

func (f *fakeMilestoneCommentStore) Create(milestoneID, authorID int, content string) (int, error) {
	id := f.nextID
	f.nextID++
	f.rows[id] = models.MilestoneComment{ID: id, MilestoneID: milestoneID, AuthorID: authorID, Content: content}
	return id, nil
}

func (f *fakeMilestoneCommentStore) UpdateContent(id int, content string) error {
	row, ok := f.rows[id]
	if !ok {
		return repository.ErrNotFound
	}
	row.Content = content
	f.rows[id] = row
	return nil
}

func (f *fakeMilestoneCommentStore) Delete(id int) error {
	if _, ok := f.rows[id]; !ok {
		return repository.ErrNotFound
	}
	delete(f.rows, id)
	return nil
}

const (
	readableMilestone   = 10
	otherMilestone      = 11
	unreadableMilestone = 12
	authorUserID        = 1
	otherUserID         = 2
)

func newTestMilestoneCommentService() (*MilestoneCommentService, *fakeMilestoneCommentStore) {
	store := newFakeMilestoneCommentStore()
	access := fakeMilestoneAccess{readable: map[int]bool{readableMilestone: true, otherMilestone: true}}
	return NewMilestoneCommentService(store, access), store
}

func TestMilestoneCommentCreateRequiresMilestoneRead(t *testing.T) {
	service, store := newTestMilestoneCommentService()
	if _, err := service.Create(authorUserID, unreadableMilestone, "hello"); !errors.Is(err, ErrPlanningForbidden) {
		t.Fatalf("expected ErrPlanningForbidden, got %v", err)
	}
	if _, _, err := service.List(authorUserID, unreadableMilestone, MilestoneCommentListParams{Limit: 10}); !errors.Is(err, ErrPlanningForbidden) {
		t.Fatalf("expected ErrPlanningForbidden listing, got %v", err)
	}
	if len(store.rows) != 0 {
		t.Fatalf("expected no stored comments, got %d", len(store.rows))
	}
}

func TestMilestoneCommentCreateSanitizesAndRejectsEmptyContent(t *testing.T) {
	service, _ := newTestMilestoneCommentService()
	created, err := service.Create(authorUserID, readableMilestone, "Plan <script>alert(1)</script>**now**")
	if err != nil {
		t.Fatal(err)
	}
	if created.AuthorID != authorUserID || created.MilestoneID != readableMilestone {
		t.Fatalf("unexpected comment: %+v", created)
	}
	if created.Content == "" || strings.Contains(created.Content, "<script>") {
		t.Fatalf("expected sanitized Markdown, got %q", created.Content)
	}
	for _, empty := range []string{"  <b></b> ", "<br />\n<br>"} {
		if _, err := service.Create(authorUserID, readableMilestone, empty); err == nil {
			t.Fatalf("expected empty content %q to be rejected", empty)
		} else if _, ok := AsPlanningValidationError(err); !ok {
			t.Fatalf("expected a validation error for %q, got %v", empty, err)
		}
	}
}

func TestMilestoneCommentKeepsEditorHardBreaks(t *testing.T) {
	service, _ := newTestMilestoneCommentService()
	created, err := service.Create(authorUserID, readableMilestone, "line one<br />line two")
	if err != nil {
		t.Fatal(err)
	}
	if created.Content != "line one<br />line two" {
		t.Fatalf("expected the editor's hard break to survive, got %q", created.Content)
	}
}

func TestMilestoneCommentOnlyAuthorMayEditOrDelete(t *testing.T) {
	service, store := newTestMilestoneCommentService()
	created, err := service.Create(authorUserID, readableMilestone, "Original")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Update(otherUserID, readableMilestone, created.ID, "Hijacked"); !errors.Is(err, ErrMilestoneCommentNotAuthor) {
		t.Fatalf("expected ErrMilestoneCommentNotAuthor on foreign edit, got %v", err)
	}
	if err := service.Delete(otherUserID, readableMilestone, created.ID); !errors.Is(err, ErrMilestoneCommentNotAuthor) {
		t.Fatalf("expected ErrMilestoneCommentNotAuthor on foreign delete, got %v", err)
	}
	if _, err := service.Update(authorUserID, otherMilestone, created.ID, "Wrong milestone"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for a comment on another milestone, got %v", err)
	}

	updated, err := service.Update(authorUserID, readableMilestone, created.ID, "Edited")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Content != "Edited" {
		t.Fatalf("expected edited content, got %q", updated.Content)
	}
	if err := service.Delete(authorUserID, readableMilestone, created.ID); err != nil {
		t.Fatal(err)
	}
	if len(store.rows) != 0 {
		t.Fatalf("expected the comment to be deleted, got %d rows", len(store.rows))
	}
}
