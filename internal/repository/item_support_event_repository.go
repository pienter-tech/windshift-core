package repository

import (
	"database/sql"
	"fmt"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

// ItemSupportEventRepository records append-only ticket facts for support
// metrics (WI-1133). Inserts are self-guarding: they only apply to
// customer-facing tickets and the single-shot kinds are deduplicated by a
// partial unique index, so callers never need read-modify-write logic.
type ItemSupportEventRepository struct {
	db database.Database
}

func NewItemSupportEventRepository(db database.Database) *ItemSupportEventRepository {
	return &ItemSupportEventRepository{db: db}
}

const itemSupportEventTicketScopeSQL = `
INSERT INTO item_support_events (workspace_id, item_id, kind, occurred_at)
SELECT i.workspace_id, i.id, ?, ?
FROM items i
WHERE i.id = ?
	AND (i.channel_id IS NOT NULL OR i.creator_portal_customer_id IS NOT NULL)
`

const itemSupportEventSingleShotSQL = itemSupportEventTicketScopeSQL + `
	ON CONFLICT DO NOTHING
`

func runItemSupportEventInsert(exec interface {
	Exec(query string, args ...any) (sql.Result, error)
}, sqlStmt string, kind string, itemID int, occurredAt time.Time) error {
	result, err := exec.Exec(sqlStmt, kind, occurredAt, itemID)
	if err != nil {
		return fmt.Errorf("record support event %s for item %d: %w", kind, itemID, err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 && kind != models.SupportEventReopened {
		// Zero rows on a single-shot kind means the item is not a
		// customer-facing ticket; reopened always applies its insert.
		return nil
	}
	return nil
}

// RecordFirstResponse records the first customer-visible response unless one
// already exists. No-op for internal items.
func (r *ItemSupportEventRepository) RecordFirstResponse(tx database.Tx, itemID int, occurredAt time.Time) error {
	return runItemSupportEventInsert(tx, itemSupportEventSingleShotSQL, models.SupportEventFirstResponse, itemID, occurredAt)
}

// RecordResolved records the first close of a ticket unless one already
// exists. No-op for internal items.
func (r *ItemSupportEventRepository) RecordResolved(tx database.Tx, itemID int, occurredAt time.Time) error {
	return runItemSupportEventInsert(tx, itemSupportEventSingleShotSQL, models.SupportEventResolved, itemID, occurredAt)
}

// RecordReopened records a completed-to-open transition. Multiple rows per
// item are expected; no-op for internal items.
func (r *ItemSupportEventRepository) RecordReopened(tx database.Tx, itemID int, occurredAt time.Time) error {
	return runItemSupportEventInsert(tx, itemSupportEventTicketScopeSQL, models.SupportEventReopened, itemID, occurredAt)
}

// CountByKind returns the number of events of each kind for an item,
// primarily for tests and diagnostics.
func (r *ItemSupportEventRepository) CountByKind(itemID int) (map[string]int, error) {
	rows, err := r.db.Query(
		"SELECT kind, COUNT(*) FROM item_support_events WHERE item_id = ? GROUP BY kind",
		itemID,
	)
	if err != nil {
		return nil, fmt.Errorf("count support events for item %d: %w", itemID, err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			return nil, fmt.Errorf("scan support event counts: %w", err)
		}
		counts[kind] = count
	}
	return counts, rows.Err()
}
