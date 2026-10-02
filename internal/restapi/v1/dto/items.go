package dto

import (
	"time"
)

// ItemResponse is the public API representation of an Item. DescriptionHTML
// is populated for single-item and mutation responses, not collections.
type ItemResponse struct {
	ID                  int            `json:"id"`
	WorkspaceID         int            `json:"workspace_id"`
	WorkspaceKey        string         `json:"workspace_key"`
	Key                 string         `json:"key"` // e.g., "PROJ-123"
	WorkspaceItemNumber int            `json:"workspace_item_number"`
	Title               string         `json:"title"`
	Description         string         `json:"description,omitempty"`
	DescriptionHTML     string         `json:"description_html,omitempty"`
	IsTask              bool           `json:"is_task"`
	DueDate             *time.Time     `json:"due_date,omitempty"`
	StartDate           *time.Time     `json:"start_date,omitempty"`
	EndDate             *time.Time     `json:"end_date,omitempty"`
	CustomFields        map[string]any `json:"custom_fields,omitempty"`

	// Hierarchy. ParentID is the raw DB id. ParentKey is the parent's
	// ready-to-display key (e.g. "WI-120"), computed server-side from the
	// parent's own workspace — clients must render this rather than building a
	// key from ParentID, which is a DB id in a different namespace and may even
	// point into another workspace. ParentKey and ParentTitle are populated
	// only on permission-checked single-item reads, and only when the caller
	// may view the parent (see ItemHandler.setParentSummary); they are omitted
	// from list responses and withheld across a workspace boundary the caller
	// cannot see.
	ParentID    *int   `json:"parent_id,omitempty"`
	ParentKey   string `json:"parent_key,omitempty"`
	ParentTitle string `json:"parent_title,omitempty"`

	// Related entities (populated based on ?expand= parameter)
	Status     *StatusSummary     `json:"status,omitempty"`
	Priority   *PrioritySummary   `json:"priority,omitempty"`
	ItemType   *ItemTypeSummary   `json:"item_type,omitempty"`
	Assignee   *UserSummary       `json:"assignee,omitempty"`
	Creator    *UserSummary       `json:"creator,omitempty"`
	Workspace  *WorkspaceSummary  `json:"workspace,omitempty"`
	Milestones []MilestoneSummary `json:"milestones,omitempty"`
	Iteration  *IterationSummary  `json:"iteration,omitempty"`
	Project    *ProjectSummary    `json:"project,omitempty"`

	// Expanded collections (populated based on ?expand= parameter)
	Comments    []CommentResponse    `json:"comments,omitempty"`
	Attachments []AttachmentResponse `json:"attachments,omitempty"`
	History     []HistoryResponse    `json:"history,omitempty"`
	Children    []ItemResponse       `json:"children,omitempty"`
	Transitions []TransitionResponse `json:"transitions,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// EnforcedTemplate echoes the mandatory work item template the item's type
	// enforces (WI-438), populated only on the create response so non-UI callers
	// learn the expected structure even when they supplied their own
	// description. Omitted when the type enforces no mandatory template.
	EnforcedTemplate *EnforcedTemplateSummary `json:"enforced_template,omitempty"`

	// HATEOAS links
	Links *ItemLinks `json:"_links,omitempty"`
}

// EnforcedTemplateSummary reports which mandatory template a created item's
// type enforces and whether its body was applied (only when the caller left the
// description empty).
type EnforcedTemplateSummary struct {
	TemplateID int    `json:"template_id"`
	Name       string `json:"name"`
	Applied    bool   `json:"applied"`
}

// ItemLinks provides HATEOAS-style links for an item
type ItemLinks struct {
	Self        string `json:"self"`
	Workspace   string `json:"workspace,omitempty"`
	Comments    string `json:"comments,omitempty"`
	History     string `json:"history,omitempty"`
	Attachments string `json:"attachments,omitempty"`
	Children    string `json:"children,omitempty"`
	Parent      string `json:"parent,omitempty"`
	Transitions string `json:"transitions,omitempty"`
}

// ItemCreateRequest is the request body for creating a new item
type ItemCreateRequest struct {
	WorkspaceID int    `json:"workspace_id" validate:"required"`
	Title       string `json:"title" validate:"required,max=255"`
	Description string `json:"description,omitempty"`
	// Optional create-time placement. If omitted, the item uses the workflow initial status.
	// If provided, it must be the initial status or directly reachable from the initial status
	// without workflow conditions, validators, or approval gates.
	StatusID     *int           `json:"status_id,omitempty"`
	PriorityID   *int           `json:"priority_id,omitempty"`
	ItemTypeID   *int           `json:"item_type_id,omitempty"`
	AssigneeID   *int           `json:"assignee_id,omitempty"`
	ParentID     *int           `json:"parent_id,omitempty"`
	MilestoneIDs []int          `json:"milestone_ids,omitempty"`
	IterationID  *int           `json:"iteration_id,omitempty"`
	ProjectID    *int           `json:"project_id,omitempty"`
	DueDate      *time.Time     `json:"due_date,omitempty"`
	StartDate    *time.Time     `json:"start_date,omitempty"`
	EndDate      *time.Time     `json:"end_date,omitempty"`
	IsTask       bool           `json:"is_task,omitempty"`
	CustomFields map[string]any `json:"custom_fields,omitempty"`
}

// ItemUpdateRequest is the request body for updating an item.
// Note: status_id is deliberately NOT accepted here. Use
// POST /rest/api/v1/items/{id}/transition to change workflow status so
// validator-mode and condition-mode workflow rules are enforced.
type ItemUpdateRequest struct {
	Title        *string        `json:"title,omitempty" validate:"omitempty,max=255"`
	Description  *string        `json:"description,omitempty"`
	PriorityID   *int           `json:"priority_id,omitempty"`
	ItemTypeID   *int           `json:"item_type_id,omitempty"`
	AssigneeID   *int           `json:"assignee_id,omitempty"`
	ParentID     *int           `json:"parent_id,omitempty"`
	MilestoneIDs *[]int         `json:"milestone_ids,omitempty"`
	IterationID  *int           `json:"iteration_id,omitempty"`
	ProjectID    *int           `json:"project_id,omitempty"`
	DueDate      *time.Time     `json:"due_date,omitempty"`
	StartDate    *time.Time     `json:"start_date,omitempty"`
	EndDate      *time.Time     `json:"end_date,omitempty"`
	IsTask       *bool          `json:"is_task,omitempty"`
	CustomFields map[string]any `json:"custom_fields,omitempty"`
}

// CommentResponse is the public API representation of a Comment.
// Warnings surfaces sanitize mutations from create / update so the
// frontend can toast them at info severity. omitempty when nothing
// was modified.
type CommentResponse struct {
	ID          int          `json:"id"`
	ItemID      int          `json:"item_id"`
	Content     string       `json:"content"`
	ContentHTML string       `json:"content_html,omitempty"`
	Author      *UserSummary `json:"author,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Warnings    []string     `json:"warnings,omitempty"`
}

// CommentCreateRequest is the request body for creating a comment
type CommentCreateRequest struct {
	Content string `json:"content" validate:"required"`
}

// CommentUpdateRequest is the request body for updating a comment
type CommentUpdateRequest struct {
	Content string `json:"content" validate:"required"`
}

// HistoryResponse is the public API representation of an item history entry
type HistoryResponse struct {
	ID               int          `json:"id"`
	ItemID           int          `json:"item_id"`
	FieldName        string       `json:"field_name"`
	OldValue         *string      `json:"old_value,omitempty"`
	NewValue         *string      `json:"new_value,omitempty"`
	ResolvedOldValue *string      `json:"resolved_old_value,omitempty"`
	ResolvedNewValue *string      `json:"resolved_new_value,omitempty"`
	User             *UserSummary `json:"user,omitempty"`
	// ActorKind is "user" (also when absent, for legacy rows),
	// "portal_customer", or "system". PortalCustomer carries the acting
	// portal customer for portal_customer entries.
	ActorKind      string                 `json:"actor_kind,omitempty"`
	PortalCustomer *PortalCustomerSummary `json:"portal_customer,omitempty"`
	ChangedAt      time.Time              `json:"changed_at"`
}

// AttachmentResponse is the public API representation of an Attachment
type AttachmentResponse struct {
	ID               int          `json:"id"`
	ItemID           *int         `json:"item_id,omitempty"`
	Filename         string       `json:"filename"`
	OriginalFilename string       `json:"original_filename"`
	MimeType         string       `json:"mime_type"`
	FileSize         int64        `json:"file_size"`
	HasThumbnail     bool         `json:"has_thumbnail"`
	Uploader         *UserSummary `json:"uploader,omitempty"`
	// UploaderPortalCustomer carries the portal-customer uploader when the
	// file was submitted through a portal or public form.
	UploaderPortalCustomer *PortalCustomerSummary `json:"uploader_portal_customer,omitempty"`
	CreatedAt              time.Time              `json:"created_at"`
	DownloadURL            string                 `json:"download_url,omitempty"`
	ThumbnailURL           string                 `json:"thumbnail_url,omitempty"`
}

// TransitionResponse represents an available workflow transition
type TransitionResponse struct {
	ID              int            `json:"id"`
	FromStatusID    *int           `json:"from_status_id,omitempty"`
	FromAllStatuses bool           `json:"from_all_statuses"`
	ToStatusID      int            `json:"to_status_id"`
	FromStatus      *StatusSummary `json:"from_status,omitempty"`
	ToStatus        *StatusSummary `json:"to_status,omitempty"`
}

// TransitionRequest is the body for POST /rest/api/v1/items/{id}/transition
type TransitionRequest struct {
	ToStatusID *int `json:"to_status_id" validate:"required"`
}

// ItemTypeChangeRequest is the body for POST /rest/api/v1/items/{id}/change-type.
// TargetStatusID is required when the target type's workflow does not contain
// the item's current status; otherwise the server returns 409 with the set of
// candidate statuses.
type ItemTypeChangeRequest struct {
	TargetItemTypeID int  `json:"target_item_type_id" validate:"required"`
	TargetStatusID   *int `json:"target_status_id,omitempty"`
}

// TransitionResultResponse is returned when an item transition completes.
type TransitionResultResponse struct {
	Item        *ItemResponse `json:"item"`
	OldStatusID *int          `json:"old_status_id,omitempty"`
	NewStatusID *int          `json:"new_status_id,omitempty"`
	NoOp        bool          `json:"no_op"`
}
