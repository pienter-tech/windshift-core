package services

import (
	"errors"
	"strconv"

	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/sanitize"
	"windshift/internal/services/template"
)

var (
	ErrCannedResponseNameRequired   = errors.New("canned response name is required")
	ErrCannedResponseBodyRequired   = errors.New("canned response body is required")
	ErrCannedResponseWorkspaceWrong = errors.New("canned response does not belong to workspace")
	ErrCannedResponseArchived       = errors.New("canned response is archived")
)

// CannedResponseUpdate carries the optional fields of a partial update.
type CannedResponseUpdate struct {
	Name      *string
	Body      *string
	IsPrivate *bool
	IsActive  *bool
}

// CannedResponseInput is the create payload before validation.
type CannedResponseInput struct {
	Name      string
	Body      string
	IsPrivate bool
	IsActive  bool
}

// CannedResponseService owns workspace canned responses (WI-1138): CRUD with
// workspace ownership checks plus {{variable}} rendering for previews and
// automation insertion.
type CannedResponseService struct {
	repo    *repository.CannedResponseRepository
	users   *repository.UserRepository
	items   *repository.ItemRepository
	auditor *logger.Auditor
}

// NewCannedResponseService creates the service. users and items back
// variable resolution; a nil items repository falls back to sample values.
func NewCannedResponseService(repo *repository.CannedResponseRepository, users *repository.UserRepository, items *repository.ItemRepository, auditors ...*logger.Auditor) *CannedResponseService {
	service := &CannedResponseService{repo: repo, users: users, items: items}
	if len(auditors) > 0 {
		service.auditor = auditors[0]
	}
	return service
}

// List returns a workspace's canned responses, optionally including archived.
func (s *CannedResponseService) List(workspaceID int, includeArchived bool) ([]models.CannedResponse, error) {
	return s.repo.ListByWorkspace(workspaceID, includeArchived)
}

// Get returns one canned response when it belongs to the workspace.
func (s *CannedResponseService) Get(workspaceID, id int) (*models.CannedResponse, error) {
	cr, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if cr.WorkspaceID != workspaceID {
		return nil, ErrCannedResponseWorkspaceWrong
	}
	return cr, nil
}

// Create validates and creates a canned response in the workspace.
func (s *CannedResponseService) Create(workspaceID int, input CannedResponseInput, actors ...AuditActor) (*models.CannedResponse, error) {
	cr, err := s.validateInput(workspaceID, input, 0)
	if err != nil {
		return nil, err
	}
	cr.WorkspaceID = workspaceID
	cr.IsActive = true // new responses always start active; archive via update
	if actor := optionalAuditActor(actors); actor != nil && actor.UserID > 0 {
		createdBy := actor.UserID
		cr.CreatedBy, cr.UpdatedBy = &createdBy, &createdBy
	}
	created, err := s.repo.Create(cr)
	if err != nil {
		return nil, err
	}
	s.emitAudit(optionalAuditActor(actors), logger.ActionCannedResponseCreate, created)
	return created, nil
}

// Update applies a partial update after checking workspace ownership.
func (s *CannedResponseService) Update(workspaceID, id int, update CannedResponseUpdate, actors ...AuditActor) (*models.CannedResponse, error) {
	existing, err := s.Get(workspaceID, id)
	if err != nil {
		return nil, err
	}
	input := CannedResponseInput{
		Name:      existing.Name,
		Body:      existing.Body,
		IsPrivate: existing.IsPrivate,
		IsActive:  existing.IsActive,
	}
	if update.Name != nil {
		input.Name = *update.Name
	}
	if update.Body != nil {
		input.Body = *update.Body
	}
	if update.IsPrivate != nil {
		input.IsPrivate = *update.IsPrivate
	}
	if update.IsActive != nil {
		input.IsActive = *update.IsActive
	}
	merged, err := s.validateInput(workspaceID, input, id)
	if err != nil {
		return nil, err
	}
	merged.ID = existing.ID
	merged.WorkspaceID = workspaceID
	merged.CreatedBy, merged.CreatedAt = existing.CreatedBy, existing.CreatedAt
	merged.UsedCount, merged.LastUsedAt = existing.UsedCount, existing.LastUsedAt
	if actor := optionalAuditActor(actors); actor != nil && actor.UserID > 0 {
		updatedBy := actor.UserID
		merged.UpdatedBy = &updatedBy
	}
	if err := s.repo.Update(merged); err != nil {
		return nil, err
	}
	updated, err := s.repo.GetByID(id)
	if err == nil {
		s.emitAudit(optionalAuditActor(actors), logger.ActionCannedResponseUpdate, updated)
	}
	return updated, err
}

// Delete removes a canned response after checking workspace ownership.
func (s *CannedResponseService) Delete(workspaceID, id int, actors ...AuditActor) (*models.CannedResponse, error) {
	existing, err := s.Get(workspaceID, id)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Delete(id); err != nil {
		return nil, err
	}
	s.emitAudit(optionalAuditActor(actors), logger.ActionCannedResponseDelete, existing)
	return existing, nil
}

// RenderPreview renders a canned response body with variables resolved from
// an optional item. Without an item, documented sample values are used so
// managers can preview snippets before any ticket exists.
func (s *CannedResponseService) RenderPreview(workspaceID, id, itemID int, actors ...AuditActor) (string, error) {
	cr, err := s.Get(workspaceID, id)
	if err != nil {
		return "", err
	}
	vars := SampleCannedResponseVariables()
	if itemID > 0 && s.items != nil {
		item, err := s.items.FindByIDWithDetails(itemID)
		if err != nil {
			return "", err
		}
		if item.WorkspaceID != workspaceID {
			return "", ErrCannedResponseWorkspaceWrong
		}
		vars = s.ItemVariables(item, actors...)
	}
	return template.Substitute(cr.Body, vars), nil
}

// RenderForItem loads an active canned response and renders it against an
// item. Private responses are only returned when allowPrivate is set, so
// customer-facing automation cannot pull internal snippets.
func (s *CannedResponseService) RenderForItem(workspaceID, id, itemID int, allowPrivate bool, actors ...AuditActor) (rendered string, isPrivate bool, err error) {
	cr, err := s.Get(workspaceID, id)
	if err != nil {
		return "", false, err
	}
	if !cr.IsActive {
		return "", false, ErrCannedResponseArchived
	}
	if cr.IsPrivate && !allowPrivate {
		return "", false, ErrCannedResponseArchived
	}
	vars := SampleCannedResponseVariables()
	if itemID > 0 && s.items != nil {
		if item, itemErr := s.items.FindByIDWithDetails(itemID); itemErr == nil && item.WorkspaceID == workspaceID {
			vars = s.ItemVariables(item, actors...)
		}
	}
	return template.Substitute(cr.Body, vars), cr.IsPrivate, nil
}

// TouchUsage records that a response was inserted somewhere.
func (s *CannedResponseService) TouchUsage(id int) {
	_ = s.repo.TouchUsage(id)
}

func (s *CannedResponseService) validateInput(workspaceID int, input CannedResponseInput, excludeID int) (*models.CannedResponse, error) {
	name := sanitize.ShortIdentifier.Sanitize(input.Name)
	if name == "" {
		return nil, ErrCannedResponseNameRequired
	}
	if input.Body == "" {
		return nil, ErrCannedResponseBodyRequired
	}
	exists, err := s.repo.NameExistsInWorkspace(workspaceID, name, excludeID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, repository.ErrDuplicateEntry
	}
	return &models.CannedResponse{Name: name, Body: input.Body, IsPrivate: input.IsPrivate, IsActive: input.IsActive}, nil
}

// SampleCannedResponseVariables documents the supported variable set with
// clearly-sample values.
func SampleCannedResponseVariables() map[string]string {
	return map[string]string{
		"requester.name": "Sample Requester",
		"ticket.key":     "WS-1",
		"ticket.title":   "Sample ticket title",
		"agent.name":     "Support Agent",
	}
}

// ItemVariables resolves the canned-response variable set for an item and
// the acting agent. Requester prefers the portal customer who created the
// ticket and falls back to the internal creator.
func (s *CannedResponseService) ItemVariables(item *models.Item, actors ...AuditActor) map[string]string {
	vars := map[string]string{
		"ticket.title": item.Title,
		"ticket.key":   item.WorkspaceKey + "-" + strconv.Itoa(item.WorkspaceItemNumber),
		"agent.name":   "Support",
	}
	if actor := optionalAuditActor(actors); actor != nil && actor.UserID > 0 && s.users != nil {
		if user, err := s.users.GetByID(actor.UserID); err == nil && user != nil {
			if user.FullName != "" {
				vars["agent.name"] = user.FullName
			} else {
				vars["agent.name"] = user.Username
			}
		}
	}
	if item.CreatorPortalCustomerID != nil {
		if name, err := s.repo.GetPortalCustomerName(*item.CreatorPortalCustomerID); err == nil && name != "" {
			vars["requester.name"] = name
			return vars
		}
	}
	if item.CreatorID != nil && s.users != nil {
		if creator, err := s.users.GetByID(*item.CreatorID); err == nil && creator != nil {
			if creator.FullName != "" {
				vars["requester.name"] = creator.FullName
			} else {
				vars["requester.name"] = creator.Username
			}
		}
	}
	return vars
}

func (s *CannedResponseService) emitAudit(actor *AuditActor, action string, cr *models.CannedResponse) {
	if actor == nil || s.auditor == nil || cr == nil {
		return
	}
	s.auditor.LogEvent(logger.AuditEvent{
		UserID: actor.UserID, Username: actor.Username, IPAddress: actor.IPAddress,
		UserAgent: actor.UserAgent, ActionType: action, ResourceType: logger.ResourceCannedResponse,
		ResourceID: &cr.ID, ResourceName: cr.Name,
		Details: mergeAuditDetails(map[string]any{"workspace_id": cr.WorkspaceID, "is_private": cr.IsPrivate}, *actor), Success: true,
	})
}
