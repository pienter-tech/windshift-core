package repository

import (
	"fmt"
	"time"
)

// Milestone history fields. Only the fields the milestone Activity
// tab shows are recorded. A page_link row records a page linked
// (new_value is the page ID) or unlinked (old_value is the page ID).
const (
	MilestoneHistoryDescription = "description"
	MilestoneHistoryStatus      = "status"
	MilestoneHistoryTargetDate  = "target_date"
	MilestoneHistoryPageLink    = "page_link"
)

// MilestoneHistoryEntry is one recorded change to a milestone field.
// UserID nil means a system actor (automation, imports).
type MilestoneHistoryEntry struct {
	MilestoneID int
	UserID      *int
	FieldName   string
	OldValue    string
	NewValue    string
	ChangedAt   time.Time
}

// RecordMilestoneHistory writes milestone history rows through w, which may be
// a transaction so the rows commit with the change they describe. Empty
// values are stored as NULL.
func RecordMilestoneHistory(w HistoryWriter, entries ...MilestoneHistoryEntry) error {
	for _, entry := range entries {
		changedAt := entry.ChangedAt
		if changedAt.IsZero() {
			changedAt = time.Now().UTC()
		}
		var userID any
		if entry.UserID != nil && *entry.UserID > 0 {
			userID = *entry.UserID
		}
		if _, err := w.ExecWrite(`
			INSERT INTO milestone_history (milestone_id, user_id, field_name, old_value, new_value, changed_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, entry.MilestoneID, userID, entry.FieldName, nullableHistoryValue(entry.OldValue), nullableHistoryValue(entry.NewValue), changedAt); err != nil {
			return fmt.Errorf("record milestone %d %s history: %w", entry.MilestoneID, entry.FieldName, err)
		}
	}
	return nil
}

func nullableHistoryValue(value string) any {
	if value == "" {
		return nil
	}
	return value
}
