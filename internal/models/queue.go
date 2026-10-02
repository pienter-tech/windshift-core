package models

import "time"

// Queue is one support-queue definition. Queues are scoped to a collection
// or, when CollectionID is nil, to the workspace's default view. The built-in
// presets are virtual and never seeded; a row with BuiltinKey set and
// IsHidden true dismisses that preset for the scope.
type Queue struct {
	ID           int       `json:"id"`
	WorkspaceID  int       `json:"workspace_id"`
	CollectionID *int      `json:"collection_id,omitempty"`
	Name         string    `json:"name"`
	QLQuery      string    `json:"ql_query"`
	FilterState  *string   `json:"filter_state,omitempty"`
	Position     int       `json:"position"`
	CreatedBy    *int      `json:"created_by,omitempty"`
	BuiltinKey   *string   `json:"builtin_key,omitempty"`
	IsHidden     bool      `json:"is_hidden"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
