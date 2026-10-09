package services

import (
	"math"
	"sort"
	"strconv"

	"windshift/internal/models"
)

// MilestoneActivityStore reads the stored Activity sources of a milestone.
// repository.MilestoneActivityRepository implements it.
type MilestoneActivityStore interface {
	List(milestoneID int, workspaceIDs []int, limit int) ([]models.MilestoneActivity, int, error)
}

// MilestoneActivityWorkspaces lists the workspaces whose items a user may
// view. PermissionService implements it; the milestone progress view uses the
// same list.
type MilestoneActivityWorkspaces interface {
	AccessibleWorkspaceIDs(userID int) ([]int, error)
}

// MilestoneActivityPageLinks lists the page links of a milestone that a user
// may view. MilestonePageLinkService implements it.
type MilestoneActivityPageLinks interface {
	List(userID, milestoneID int) ([]models.MilestonePageLink, error)
}

// MilestoneActivityService builds a milestone's Activity feed: one
// shared, newest-first feed of milestone events (comments; description,
// status, and target-date changes; linked pages) and member-item events
// (comments, status changes, items added or removed). Every viewer sees the
// same feed, except that item events are limited to items in workspaces the
// viewer can access (the progress view's rule) and page links to pages the
// viewer may view.
type MilestoneActivityService struct {
	store      MilestoneActivityStore
	access     MilestoneReadAuthorizer
	workspaces MilestoneActivityWorkspaces
	pages      MilestoneActivityPageLinks
}

// NewMilestoneActivityService creates a MilestoneActivityService. A nil page
// lister leaves page-link entries out of the feed.
func NewMilestoneActivityService(store MilestoneActivityStore, access MilestoneReadAuthorizer, workspaces MilestoneActivityWorkspaces, pages MilestoneActivityPageLinks) *MilestoneActivityService {
	return &MilestoneActivityService{store: store, access: access, workspaces: workspaces, pages: pages}
}

// MilestoneActivityListParams selects one page of the feed.
type MilestoneActivityListParams struct {
	Limit  int
	Offset int
}

const defaultMilestoneActivityLimit = 50

// List returns one page of the milestone's Activity feed, newest first, and
// the total number of entries the user can see.
func (s *MilestoneActivityService) List(userID, milestoneID int, params MilestoneActivityListParams) ([]models.MilestoneActivity, int, error) {
	if _, err := s.access.AuthorizeMilestoneRead(userID, milestoneID); err != nil {
		return nil, 0, err
	}
	limit := params.Limit
	if limit <= 0 {
		limit = defaultMilestoneActivityLimit
	}
	offset := max(params.Offset, 0)
	window := offset + limit
	if window < offset {
		window = math.MaxInt32
	}

	workspaceIDs, err := s.workspaces.AccessibleWorkspaceIDs(userID)
	if err != nil {
		return nil, 0, err
	}
	entries, total, err := s.store.List(milestoneID, workspaceIDs, window)
	if err != nil {
		return nil, 0, err
	}
	if s.pages != nil {
		links, err := s.pages.List(userID, milestoneID)
		if err != nil {
			return nil, 0, err
		}
		for _, link := range links {
			entries = append(entries, pageLinkedActivity(link))
		}
		total += len(links)
		sort.SliceStable(entries, func(a, b int) bool {
			return entries[a].OccurredAt.After(entries[b].OccurredAt)
		})
	}

	if offset >= len(entries) {
		return []models.MilestoneActivity{}, total, nil
	}
	end := min(offset+limit, len(entries))
	return entries[offset:end], total, nil
}

func pageLinkedActivity(link models.MilestonePageLink) models.MilestoneActivity {
	activity := models.MilestoneActivity{
		ID:         "milestone_page_link:" + strconv.Itoa(link.ID),
		Type:       models.MilestoneActivityMilestonePageLinked,
		OccurredAt: link.CreatedAt.UTC(),
		ActorKind:  models.MilestoneActivityActorSystem,
		Page: &models.MilestoneActivityPage{
			ID:          link.PageID,
			Title:       link.PageTitle,
			WorkspaceID: link.WorkspaceID,
		},
	}
	if link.CreatedBy != nil {
		id := *link.CreatedBy
		activity.ActorKind = models.MilestoneActivityActorUser
		activity.ActorID = &id
		activity.ActorName = link.CreatedByName
	}
	return activity
}
