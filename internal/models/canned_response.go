package models

import "time"

// CannedResponse is a workspace-scoped reusable reply snippet for support
// agents. Body is Markdown supporting the documented {{variable}} set
// (requester.name, ticket.key, ticket.title, agent.name). Private snippets
// are internal notes and must never be delivered to portal customers.
type CannedResponse struct {
	ID          int        `json:"id"`
	WorkspaceID int        `json:"workspace_id"`
	Name        string     `json:"name"`
	Body        string     `json:"body"`
	IsPrivate   bool       `json:"is_private"`
	IsActive    bool       `json:"is_active"`
	CreatedBy   *int       `json:"created_by,omitempty"`
	UpdatedBy   *int       `json:"updated_by,omitempty"`
	UsedCount   int        `json:"used_count"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
