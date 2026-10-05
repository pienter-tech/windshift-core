package models

import "time"

// ItemParticipant is an external person (a portal customer) attached to a work
// item as a request participant (WI-1136). Participants are distinct from the
// single accountable assignee and from internal collaborators: internal users
// already collaborate through assignment, teams, and watchers, so they are
// never stored here.
//
// Participants see customer-facing content only. Internal notes, private
// comments, and agent-only fields are never exposed to them.
type ItemParticipant struct {
	ID               int       `json:"id"`
	ItemID           int       `json:"item_id"`
	PortalCustomerID int       `json:"portal_customer_id"`
	AddedBy          *int      `json:"added_by,omitempty"`
	CreatedAt        time.Time `json:"created_at"`

	// Joined fields for API responses.
	CustomerName  string `json:"customer_name,omitempty"`
	CustomerEmail string `json:"customer_email,omitempty"`
	AddedByName   string `json:"added_by_name,omitempty"`
}
