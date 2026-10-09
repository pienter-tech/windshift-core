package services

import (
	"math"
	"sort"
	"strconv"
	"time"

	"windshift/internal/models"
)

// MilestoneActivityStore reads the stored Activity sources of a milestone.
// repository.MilestoneActivityRepository implements it. A non-zero since keeps
// only entries that occurred at or after it.
type MilestoneActivityStore interface {
	List(milestoneID int, workspaceIDs []int, since time.Time, limit int) ([]models.MilestoneActivity, int, error)
}

// MilestoneActivityWorkspaces lists the workspaces whose items a user may
// view. PermissionService implements it; the milestone progress view uses the
// same list.
type MilestoneActivityWorkspaces interface {
	AccessibleWorkspaceIDs(userID int) ([]int, error)
}

// MilestoneActivityPageLinks lists the recorded page links and unlinks of a
// milestone for pages a user may view. MilestonePageLinkService implements it.
type MilestoneActivityPageLinks interface {
	History(userID, milestoneID int) ([]models.MilestonePageLinkEvent, error)
}

// MilestoneActivityService builds a milestone's Activity feed (WCORE-21): one
// shared, newest-first feed of milestone events (comments; description,
// status, and target-date changes; pages linked and unlinked) and member-item events
// (comments, status changes, items added or removed). Every viewer sees the
// same feed, except that item events are limited to items in workspaces the
// viewer can access (the progress view's rule) and page link entries to pages
// the viewer may view. Page link entries come from the milestone history
// (WCORE-26), so an unlinked page keeps its "linked" entry and gains an
// "unlinked" one; links made before that history existed have no entry.
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

// MilestoneActivityListParams selects one page of the feed. A non-zero Since
// limits the feed to entries that occurred at or after it (WCORE-43); Limit,
// Offset, and the total then apply to that filtered feed. Since is inclusive
// so an entry recorded later with the same timestamp as the newest entry a
// poller has seen is not skipped; pollers drop already-seen entries by ID.
type MilestoneActivityListParams struct {
	Limit  int
	Offset int
	Since  time.Time
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
	entries, total, err := s.store.List(milestoneID, workspaceIDs, params.Since, window)
	if err != nil {
		return nil, 0, err
	}
	if s.pages != nil {
		events, err := s.pages.History(userID, milestoneID)
		if err != nil {
			return nil, 0, err
		}
		// History is oldest first; append newest first so the stable sort
		// keeps a later event ahead of an earlier one with the same time.
		for i := len(events) - 1; i >= 0; i-- {
			if !params.Since.IsZero() && events[i].OccurredAt.Before(params.Since) {
				continue
			}
			entries = append(entries, pageLinkActivity(events[i]))
			total++
		}
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

func pageLinkActivity(event models.MilestonePageLinkEvent) models.MilestoneActivity {
	activity := models.MilestoneActivity{
		ID:         "milestone_history:" + strconv.Itoa(event.ID),
		Type:       models.MilestoneActivityMilestonePageLinked,
		OccurredAt: event.OccurredAt.UTC(),
		ActorKind:  models.MilestoneActivityActorSystem,
		Page: &models.MilestoneActivityPage{
			ID:          event.PageID,
			Title:       event.PageTitle,
			WorkspaceID: event.WorkspaceID,
		},
	}
	if event.Unlinked {
		activity.Type = models.MilestoneActivityMilestonePageUnlinked
	}
	if event.UserID != nil {
		id := *event.UserID
		activity.ActorKind = models.MilestoneActivityActorUser
		activity.ActorID = &id
		activity.ActorName = event.UserName
	}
	return activity
}
