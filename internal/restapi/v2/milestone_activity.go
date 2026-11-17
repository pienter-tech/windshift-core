package v2

import (
	"net/http"
	"strings"
	"time"

	"windshift/internal/models"
	"windshift/internal/services"
)

// registerMilestoneActivityRoutes publishes a milestone's Activity feed:
// milestone comments, description/status/target-date changes,
// linked pages, and member-item comments, status changes, and membership
// changes, newest first. The same route serves global and local milestones;
// item entries are limited to items the viewer can access. The optional since
// query parameter (RFC 3339) keeps entries that occurred at or after
// it, so a poller can fetch only new entries; see the route description in
// applyParameterCorrections for the contract.
func registerMilestoneActivityRoutes(builder *routeBuilder, deps Deps) {
	builder.Read("/milestones/{milestone_id}/activity", AuthAuthenticated, []string{"milestones:read"}, listMilestoneActivity(deps.MilestoneActivity))
}

// milestoneActivityPage is one page of the feed. The feed reports whether
// another page follows instead of a total, so reading a page never counts the
// full item history.
type milestoneActivityPage struct {
	Entries []models.MilestoneActivity `json:"entries"`
	HasMore bool                       `json:"has_more"`
}

func listMilestoneActivity(activity milestoneActivityApplication) readOperation[milestoneActivityPage] {
	return func(r *http.Request) (milestoneActivityPage, error) {
		user, milestoneID, err := planningTarget(r, "milestone_id")
		if err != nil {
			return milestoneActivityPage{}, err
		}
		page, err := ParsePage(r)
		if err != nil {
			return milestoneActivityPage{}, err
		}
		since, err := parseActivitySince(r)
		if err != nil {
			return milestoneActivityPage{}, err
		}
		entries, hasMore, err := activity.List(user.ID, milestoneID, services.MilestoneActivityListParams{
			Limit: page.PageSize, Offset: page.Offset, Since: since,
		})
		if err != nil {
			return milestoneActivityPage{}, planningError(err)
		}
		return milestoneActivityPage{Entries: entries, HasMore: hasMore}, nil
	}
}

// parseActivitySince reads the optional since timestamp. It must be RFC 3339
// with a zone (fractional seconds allowed), like the entries' occurred_at.
func parseActivitySince(r *http.Request) (time.Time, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("since"))
	if raw == "" {
		return time.Time{}, nil
	}
	since, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		apiErr := newError(http.StatusBadRequest, "invalid_request", "since must be an RFC 3339 timestamp")
		apiErr.Details = map[string]any{"field": "since"}
		return time.Time{}, apiErr
	}
	return since, nil
}
