package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

// MilestoneCommentRepository persists Markdown comments on milestones.
// Milestone comments live in their own table so they never feed
// item events, notifications, mentions, or webhooks.
type MilestoneCommentRepository struct {
	db database.Database
}

// NewMilestoneCommentRepository creates a MilestoneCommentRepository.
func NewMilestoneCommentRepository(db database.Database) *MilestoneCommentRepository {
	return &MilestoneCommentRepository{db: db}
}

const milestoneCommentSelect = `
	SELECT mc.id, mc.milestone_id, mc.author_id, mc.content, mc.created_at, mc.updated_at,
	       COALESCE(NULLIF(TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')), ''), u.username, 'Unknown User') AS author_name,
	       COALESCE(u.avatar_url, '') AS author_avatar,
	       COALESCE(u.is_agent, FALSE) AS is_agent
	FROM milestone_comments mc
	LEFT JOIN users u ON u.id = mc.author_id`

// ListByMilestone returns one page of a milestone's comments in creation
// order (oldest first unless desc), with author name and avatar joined.
func (r *MilestoneCommentRepository) ListByMilestone(milestoneID, limit, offset int, desc bool) ([]models.MilestoneComment, error) {
	order := "ASC"
	if desc {
		order = "DESC"
	}
	query := milestoneCommentSelect + `
	WHERE mc.milestone_id = ?
	ORDER BY mc.created_at ` + order + `, mc.id ` + order + `
	LIMIT ? OFFSET ?`
	rows, err := r.db.Query(query, milestoneID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list comments for milestone %d: %w", milestoneID, err)
	}
	defer func() { _ = rows.Close() }()
	result := []models.MilestoneComment{}
	for rows.Next() {
		comment, err := scanMilestoneComment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan milestone comment: %w", err)
		}
		result = append(result, comment)
	}
	return result, rows.Err()
}

// CountByMilestone returns how many comments a milestone has.
func (r *MilestoneCommentRepository) CountByMilestone(milestoneID int) (int, error) {
	var count int
	if err := r.db.QueryRow("SELECT COUNT(*) FROM milestone_comments WHERE milestone_id = ?", milestoneID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count comments for milestone %d: %w", milestoneID, err)
	}
	return count, nil
}

// GetByID loads one milestone comment. Returns ErrNotFound when missing.
func (r *MilestoneCommentRepository) GetByID(id int) (*models.MilestoneComment, error) {
	comment, err := scanMilestoneComment(r.db.QueryRow(milestoneCommentSelect+"\n\tWHERE mc.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get milestone comment %d: %w", id, err)
	}
	return &comment, nil
}

// Create inserts a milestone comment and returns its ID.
func (r *MilestoneCommentRepository) Create(milestoneID, authorID int, content string) (int, error) {
	now := time.Now().UTC()
	var id int
	err := r.db.QueryRow(`
		INSERT INTO milestone_comments (milestone_id, author_id, content, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?) RETURNING id
	`, milestoneID, authorID, content, now, now).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create milestone comment: %w", err)
	}
	return id, nil
}

// UpdateContent replaces a comment's Markdown and bumps updated_at.
func (r *MilestoneCommentRepository) UpdateContent(id int, content string) error {
	result, err := r.db.ExecWrite(
		"UPDATE milestone_comments SET content = ?, updated_at = ? WHERE id = ?",
		content, time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("update milestone comment %d: %w", id, err)
	}
	return requireMilestoneCommentAffected(result, id)
}

// Delete removes a milestone comment.
func (r *MilestoneCommentRepository) Delete(id int) error {
	result, err := r.db.ExecWrite("DELETE FROM milestone_comments WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete milestone comment %d: %w", id, err)
	}
	return requireMilestoneCommentAffected(result, id)
}

func requireMilestoneCommentAffected(result sql.Result, id int) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("milestone comment %d rows affected: %w", id, err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

type milestoneCommentScanner interface {
	Scan(dest ...any) error
}

func scanMilestoneComment(row milestoneCommentScanner) (models.MilestoneComment, error) {
	var comment models.MilestoneComment
	err := row.Scan(&comment.ID, &comment.MilestoneID, &comment.AuthorID, &comment.Content,
		&comment.CreatedAt, &comment.UpdatedAt, &comment.AuthorName, &comment.AuthorAvatar, &comment.IsAgent)
	return comment, err
}
