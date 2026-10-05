package repository

import (
	"database/sql"
	"errors"
	"fmt"

	"windshift/internal/database"
	"windshift/internal/models"
)

// ErrPageLinkTypeUnavailable is returned when the built-in Page link type
// (builtin_key "page") is missing or inactive, so no page link can be stored.
var ErrPageLinkTypeUnavailable = errors.New("the built-in Page link type is missing or inactive")

// MilestonePageLinkRepository persists links from milestones to pages
// (WCORE-19). They reuse the polymorphic item_links table: source_type
// "milestone", target_type "page", the built-in Page link type, and the
// table's created_by/created_at for who linked the page and when. The generic
// link endpoints ignore these rows; only the milestone page-link routes
// manage them.
type MilestonePageLinkRepository struct {
	db database.Database
}

// NewMilestonePageLinkRepository creates a MilestonePageLinkRepository.
func NewMilestonePageLinkRepository(db database.Database) *MilestonePageLinkRepository {
	return &MilestonePageLinkRepository{db: db}
}

// The inner page join drops links whose page row no longer exists.
const milestonePageLinkSelect = `
	SELECT il.id, il.source_id, il.target_id, p.title, p.workspace_id, il.created_by, il.created_at,
	       COALESCE(NULLIF(TRIM(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, '')), ''), u.username, '') AS created_by_name
	FROM item_links il
	JOIN pages p ON p.id = il.target_id
	LEFT JOIN users u ON u.id = il.created_by
	WHERE il.source_type = 'milestone' AND il.target_type = 'page'`

// ListByMilestone returns a milestone's page links, oldest first.
func (r *MilestonePageLinkRepository) ListByMilestone(milestoneID int) ([]models.MilestonePageLink, error) {
	rows, err := r.db.Query(milestonePageLinkSelect+`
	  AND il.source_id = ?
	ORDER BY il.created_at, il.id`, milestoneID)
	if err != nil {
		return nil, fmt.Errorf("list page links for milestone %d: %w", milestoneID, err)
	}
	defer func() { _ = rows.Close() }()
	result := []models.MilestonePageLink{}
	for rows.Next() {
		link, err := scanMilestonePageLink(rows)
		if err != nil {
			return nil, fmt.Errorf("scan milestone page link: %w", err)
		}
		result = append(result, link)
	}
	return result, rows.Err()
}

// GetByID loads one milestone page link. Returns ErrNotFound when missing.
func (r *MilestonePageLinkRepository) GetByID(id int) (*models.MilestonePageLink, error) {
	link, err := scanMilestonePageLink(r.db.QueryRow(milestonePageLinkSelect+"\n\t  AND il.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get milestone page link %d: %w", id, err)
	}
	return &link, nil
}

// Create links a page to a milestone and returns the link ID. It returns
// ErrDuplicateEntry when the page is already linked and
// ErrPageLinkTypeUnavailable when the built-in Page link type is unusable.
func (r *MilestonePageLinkRepository) Create(milestoneID, pageID, createdBy int) (int, error) {
	var linkTypeID int
	err := r.db.QueryRow("SELECT id FROM link_types WHERE builtin_key = 'page' AND active = TRUE").Scan(&linkTypeID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrPageLinkTypeUnavailable
	}
	if err != nil {
		return 0, fmt.Errorf("load page link type: %w", err)
	}
	var id int
	err = r.db.QueryRow(`
		INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id, created_by, created_at)
		VALUES (?, 'milestone', ?, 'page', ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, linkTypeID, milestoneID, pageID, createdBy).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrDuplicateEntry
	}
	if err != nil {
		return 0, fmt.Errorf("create milestone page link: %w", err)
	}
	return id, nil
}

// Delete removes one milestone page link.
func (r *MilestonePageLinkRepository) Delete(id int) error {
	result, err := r.db.ExecWrite("DELETE FROM item_links WHERE id = ? AND source_type = 'milestone'", id)
	if err != nil {
		return fmt.Errorf("delete milestone page link %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("milestone page link %d rows affected: %w", id, err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteByMilestone removes every page link of a milestone inside tx. Call it
// when deleting the milestone so no links point at a missing milestone.
func (r *MilestonePageLinkRepository) DeleteByMilestone(tx database.Tx, milestoneID int) error {
	if _, err := tx.Exec("DELETE FROM item_links WHERE source_type = 'milestone' AND source_id = ?", milestoneID); err != nil {
		return fmt.Errorf("delete page links for milestone %d: %w", milestoneID, err)
	}
	return nil
}

type milestonePageLinkScanner interface {
	Scan(dest ...any) error
}

func scanMilestonePageLink(row milestonePageLinkScanner) (models.MilestonePageLink, error) {
	var link models.MilestonePageLink
	err := row.Scan(&link.ID, &link.MilestoneID, &link.PageID, &link.PageTitle, &link.WorkspaceID,
		&link.CreatedBy, &link.CreatedAt, &link.CreatedByName)
	return link, err
}
