package models

import "time"

// Kinds of append-only ticket fact events recorded for support metrics.
const (
	SupportEventFirstResponse = "first_response"
	SupportEventResolved      = "resolved"
	SupportEventReopened      = "reopened"
)

// ItemSupportEvent is one observed ticket fact (WI-1133). Rows are
// append-only: first_response and resolved are written once per item,
// reopened once per completed-to-open transition. Only customer-facing
// tickets (items with a channel or a portal creator) produce events.
type ItemSupportEvent struct {
	ID          int64     `json:"id"`
	WorkspaceID int       `json:"workspace_id"`
	ItemID      int       `json:"item_id"`
	Kind        string    `json:"kind"`
	OccurredAt  time.Time `json:"occurred_at"`
}
