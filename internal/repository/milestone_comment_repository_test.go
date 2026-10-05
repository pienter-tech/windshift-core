package repository

import (
	"errors"
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

func newMilestoneCommentFixture(t *testing.T) (*MilestoneCommentRepository, database.Database, int) {
	t.Helper()
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "milestone-comments.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecWrite(`INSERT INTO users (id, email, username, first_name, last_name, avatar_url)
		VALUES (1, 'ada@example.test', 'ada', 'Ada', 'Lovelace', '/avatars/ada.png'),
		       (2, 'bob@example.test', 'bob', '', '', NULL)`); err != nil {
		t.Fatal(err)
	}
	var milestoneID int
	if err := db.QueryRow(`INSERT INTO milestones (name, description, status, is_global)
		VALUES ('Global hub', '', 'planning', true) RETURNING id`).Scan(&milestoneID); err != nil {
		t.Fatal(err)
	}
	return NewMilestoneCommentRepository(db), db, milestoneID
}

func TestMilestoneCommentRepositoryLifecycle(t *testing.T) {
	repo, _, milestoneID := newMilestoneCommentFixture(t)

	firstID, err := repo.Create(milestoneID, 1, "First **note**")
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := repo.Create(milestoneID, 2, "Second note")
	if err != nil {
		t.Fatal(err)
	}

	first, err := repo.GetByID(firstID)
	if err != nil {
		t.Fatal(err)
	}
	if first.MilestoneID != milestoneID || first.AuthorID != 1 || first.Content != "First **note**" {
		t.Fatalf("unexpected comment: %+v", first)
	}
	if first.AuthorName != "Ada Lovelace" || first.AuthorAvatar != "/avatars/ada.png" || first.CreatedAt.IsZero() {
		t.Fatalf("expected joined author fields and timestamps, got %+v", first)
	}

	rows, err := repo.ListByMilestone(milestoneID, 10, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != firstID || rows[1].ID != secondID {
		t.Fatalf("expected oldest-first order, got %+v", rows)
	}
	if rows[1].AuthorName != "bob" {
		t.Fatalf("expected username fallback for author without a name, got %q", rows[1].AuthorName)
	}
	desc, err := repo.ListByMilestone(milestoneID, 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(desc) != 1 || desc[0].ID != secondID {
		t.Fatalf("expected newest comment first with limit 1, got %+v", desc)
	}
	count, err := repo.CountByMilestone(milestoneID)
	if err != nil || count != 2 {
		t.Fatalf("expected 2 comments, got %d (%v)", count, err)
	}

	if err := repo.UpdateContent(firstID, "Edited"); err != nil {
		t.Fatal(err)
	}
	edited, err := repo.GetByID(firstID)
	if err != nil {
		t.Fatal(err)
	}
	if edited.Content != "Edited" || edited.UpdatedAt.Before(edited.CreatedAt) {
		t.Fatalf("unexpected edited comment: %+v", edited)
	}

	if err := repo.Delete(firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByID(firstID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	if err := repo.Delete(firstID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound deleting a missing comment, got %v", err)
	}
	if err := repo.UpdateContent(firstID, "gone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound updating a missing comment, got %v", err)
	}
}

func TestMilestoneCommentsCascadeWithMilestone(t *testing.T) {
	repo, db, milestoneID := newMilestoneCommentFixture(t)
	if _, err := repo.Create(milestoneID, 1, "Will cascade"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecWrite(`DELETE FROM milestones WHERE id = ?`, milestoneID); err != nil {
		t.Fatal(err)
	}
	count, err := repo.CountByMilestone(milestoneID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected comments to cascade with their milestone, got %d", count)
	}
}
