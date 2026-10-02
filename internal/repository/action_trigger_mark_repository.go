package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"windshift/internal/database"
)

// ActionTriggerMarkRepository persists per-(action, item) inactivity marks
// (WI-1132). A mark records the item activity timestamp a trigger fired for,
// so the same stale period never fires twice: the mark only matches again
// once the item's activity moves past the stored timestamp.
type ActionTriggerMarkRepository struct {
	db database.Database
}

// NewActionTriggerMarkRepository creates the repository.
func NewActionTriggerMarkRepository(db database.Database) *ActionTriggerMarkRepository {
	return &ActionTriggerMarkRepository{db: db}
}

// MarkFor returns the stored last-activity timestamp for an (action, item)
// pair, or ErrNotFound when the pair has no mark.
func (r *ActionTriggerMarkRepository) MarkFor(actionID, itemID int) (time.Time, error) {
	var lastActivity time.Time
	err := r.db.QueryRow(
		"SELECT last_activity_at FROM action_trigger_marks WHERE action_id = ? AND item_id = ?",
		actionID, itemID,
	).Scan(&lastActivity)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("load action trigger mark (action %d, item %d): %w", actionID, itemID, err)
	}
	return lastActivity, nil
}

// Upsert records that actionID fired for itemID while the item's last
// activity was at lastActivityAt.
func (r *ActionTriggerMarkRepository) Upsert(actionID, itemID int, lastActivityAt time.Time) error {
	now := time.Now()
	_, err := r.db.ExecWrite(`
		INSERT INTO action_trigger_marks (action_id, item_id, last_activity_at, marked_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(action_id, item_id) DO UPDATE SET last_activity_at = excluded.last_activity_at, marked_at = excluded.marked_at
	`, actionID, itemID, lastActivityAt, now)
	if err != nil {
		return fmt.Errorf("upsert action trigger mark (action %d, item %d): %w", actionID, itemID, err)
	}
	return nil
}

// ClearItemMarks removes every mark for an item after new activity, re-arming
// all inactivity triggers for the next stale period.
func (r *ActionTriggerMarkRepository) ClearItemMarks(itemID int) error {
	if _, err := r.db.ExecWrite("DELETE FROM action_trigger_marks WHERE item_id = ?", itemID); err != nil {
		return fmt.Errorf("clear action trigger marks for item %d: %w", itemID, err)
	}
	return nil
}

// DeleteForAction removes every mark for one action.
func (r *ActionTriggerMarkRepository) DeleteForAction(actionID int) error {
	if _, err := r.db.ExecWrite("DELETE FROM action_trigger_marks WHERE action_id = ?", actionID); err != nil {
		return fmt.Errorf("delete action trigger marks for action %d: %w", actionID, err)
	}
	return nil
}
