package services

import (
	"errors"
	"fmt"

	"windshift/internal/models"
	"windshift/internal/repository"
)

var (
	// ErrMilestonePageLinksForbidden rejects page-link changes by a user who
	// can view the milestone but not edit it.
	ErrMilestonePageLinksForbidden = errors.New("changing page links requires edit rights on the milestone")

	// ErrMilestonePageNotFound covers a page that does not exist, lives
	// outside the workspace a workspace milestone links from, or is hidden
	// from the user. They share one response so page existence does not leak.
	ErrMilestonePageNotFound = errors.New("page not found")
)

// MilestonePageLinkAccess loads a milestone and checks the user's rights on
// it. PlanningApplicationService implements it.
type MilestonePageLinkAccess interface {
	AuthorizeMilestoneRead(userID, milestoneID int) (*MilestoneResult, error)
	AuthorizeMilestoneWrite(userID, milestoneID int) (*MilestoneResult, error)
}

// MilestonePageLinkStore is the persistence the page-link service needs.
// repository.MilestonePageLinkRepository implements it.
type MilestonePageLinkStore interface {
	ListByMilestone(milestoneID int) ([]models.MilestonePageLink, error)
	GetByID(id int) (*models.MilestonePageLink, error)
	Create(milestoneID, pageID, createdBy int) (int, error)
	Delete(id, deletedBy int) error
	ListHistory(milestoneID int) ([]models.MilestonePageLinkEvent, error)
	PageWorkspaceID(pageID int) (int, error)
}

// MilestonePageLinkService links pages to milestones (WCORE-19). Anyone who
// can view the milestone sees the linked pages they may view; users with edit
// rights on the milestone link and unlink pages. A workspace milestone links
// pages from its own workspace; a global milestone links pages from any
// workspace, each checked in the page's own workspace (WCORE-44). Each link
// and unlink is kept in the milestone history (WCORE-26).
type MilestonePageLinkService struct {
	links  MilestonePageLinkStore
	access MilestonePageLinkAccess
	pages  PagePermissionChecker
}

// NewMilestonePageLinkService creates a MilestonePageLinkService. A nil page
// checker fails closed: no page is visible or linkable.
func NewMilestonePageLinkService(links MilestonePageLinkStore, access MilestonePageLinkAccess, pages PagePermissionChecker) *MilestonePageLinkService {
	return &MilestonePageLinkService{links: links, access: access, pages: pages}
}

// List returns the milestone's linked pages that the user may view, oldest
// link first.
func (s *MilestonePageLinkService) List(userID, milestoneID int) ([]models.MilestonePageLink, error) {
	milestone, err := s.access.AuthorizeMilestoneRead(userID, milestoneID)
	if err != nil {
		return nil, err
	}
	result := []models.MilestonePageLink{}
	if s.pages == nil {
		return result, nil
	}
	rows, err := s.links.ListByMilestone(milestoneID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return result, nil
	}
	pageWorkspaces := make(map[int]int, len(rows))
	for _, row := range rows {
		pageWorkspaces[row.PageID] = row.WorkspaceID
	}
	visible, err := s.visiblePageIDs(userID, pageScope(milestone), pageWorkspaces)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if visible[row.PageID] {
			result = append(result, row)
		}
	}
	return result, nil
}

// History returns the milestone's recorded page links and unlinks for pages
// the user may view, oldest first (WCORE-26). Archived, deleted, and hidden
// pages are left out.
func (s *MilestonePageLinkService) History(userID, milestoneID int) ([]models.MilestonePageLinkEvent, error) {
	milestone, err := s.access.AuthorizeMilestoneRead(userID, milestoneID)
	if err != nil {
		return nil, err
	}
	result := []models.MilestonePageLinkEvent{}
	if s.pages == nil {
		return result, nil
	}
	events, err := s.links.ListHistory(milestoneID)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return result, nil
	}
	pageWorkspaces := make(map[int]int, len(events))
	for _, event := range events {
		pageWorkspaces[event.PageID] = event.WorkspaceID
	}
	visible, err := s.visiblePageIDs(userID, pageScope(milestone), pageWorkspaces)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		if visible[event.PageID] {
			result = append(result, event)
		}
	}
	return result, nil
}

// Create links a page to the milestone: a page from the milestone's own
// workspace, or for a global milestone a page from any workspace. The user
// must be able to view the page.
func (s *MilestonePageLinkService) Create(userID, milestoneID, pageID int) (*models.MilestonePageLink, error) {
	scope, err := s.requireEditable(userID, milestoneID)
	if err != nil {
		return nil, err
	}
	workspaceID, err := s.linkableWorkspace(scope, pageID)
	if err != nil {
		return nil, err
	}
	if !s.pageVisible(userID, workspaceID, pageID) {
		return nil, ErrMilestonePageNotFound
	}
	id, err := s.links.Create(milestoneID, pageID, userID)
	if err != nil {
		return nil, err
	}
	return s.links.GetByID(id)
}

// Delete unlinks a page from the milestone. A link on another milestone, or
// to a page the user cannot view, reads as not found.
func (s *MilestonePageLinkService) Delete(userID, milestoneID, linkID int) error {
	scope, err := s.requireEditable(userID, milestoneID)
	if err != nil {
		return err
	}
	link, err := s.links.GetByID(linkID)
	if err != nil {
		return err
	}
	workspaceID := link.WorkspaceID
	if scope != nil {
		workspaceID = *scope
	}
	if link.MilestoneID != milestoneID || !s.pageVisible(userID, workspaceID, link.PageID) {
		return fmt.Errorf("milestone page link %d not on milestone %d: %w", linkID, milestoneID, repository.ErrNotFound)
	}
	return s.links.Delete(linkID, userID)
}

// requireEditable confirms the user can view and edit the milestone. It
// returns the milestone's page scope (see pageScope).
func (s *MilestonePageLinkService) requireEditable(userID, milestoneID int) (*int, error) {
	milestone, err := s.access.AuthorizeMilestoneRead(userID, milestoneID)
	if err != nil {
		return nil, err
	}
	if _, err := s.access.AuthorizeMilestoneWrite(userID, milestoneID); err != nil {
		if errors.Is(err, ErrPlanningForbidden) {
			return nil, ErrMilestonePageLinksForbidden
		}
		return nil, err
	}
	return pageScope(milestone), nil
}

// pageScope returns the workspace a milestone links pages from: its own
// workspace for a workspace milestone, nil (any workspace) for a global one.
// A workspace milestone without a workspace gets 0, which matches no page.
func pageScope(milestone *MilestoneResult) *int {
	if milestone.IsGlobal {
		return nil
	}
	workspaceID := 0
	if milestone.WorkspaceID != nil {
		workspaceID = *milestone.WorkspaceID
	}
	return &workspaceID
}

// linkableWorkspace returns the workspace to check a page in before linking
// it: the scope's workspace, or for a global milestone the page's own.
func (s *MilestonePageLinkService) linkableWorkspace(scope *int, pageID int) (int, error) {
	if scope != nil {
		return *scope, nil
	}
	if pageID <= 0 {
		return 0, ErrMilestonePageNotFound
	}
	workspaceID, err := s.links.PageWorkspaceID(pageID)
	if errors.Is(err, repository.ErrNotFound) {
		return 0, ErrMilestonePageNotFound
	}
	return workspaceID, err
}

// visiblePageIDs reports which pages the user may view, given each page's
// workspace. With a scope only pages from that workspace count; without one
// (global milestones) each page is checked in its own workspace, one batch
// per workspace.
func (s *MilestonePageLinkService) visiblePageIDs(userID int, scope *int, pageWorkspaces map[int]int) (map[int]bool, error) {
	byWorkspace := map[int][]int{}
	for pageID, workspaceID := range pageWorkspaces {
		if scope != nil && workspaceID != *scope {
			continue
		}
		byWorkspace[workspaceID] = append(byWorkspace[workspaceID], pageID)
	}
	visible := map[int]bool{}
	for workspaceID, pageIDs := range byWorkspace {
		got, err := s.pages.ListVisiblePageIDs(userID, workspaceID, pageIDs)
		if err != nil {
			return nil, err
		}
		for pageID, ok := range got {
			if ok {
				visible[pageID] = true
			}
		}
	}
	return visible, nil
}

// pageVisible reports whether the page exists in the workspace and the user
// may view it. Errors fail closed.
func (s *MilestonePageLinkService) pageVisible(userID, workspaceID, pageID int) bool {
	if s.pages == nil || pageID <= 0 {
		return false
	}
	ok, err := s.pages.Can(userID, workspaceID, pageID, PageOpView)
	return err == nil && ok
}
