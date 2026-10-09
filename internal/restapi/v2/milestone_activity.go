package v2

import (
	"net/http"
	"strings"
	"time"

	"windshift/internal/models"
	"windshift/internal/services"
)

// registerMilestoneActivityRoutes publishes a milestone's Activity feed
// (WCORE-21): milestone comments, description/status/target-date changes,
// linked pages, and member-item comments, status changes, and membership
// changes, newest first. The same route serves global and local milestones;
// item entries are limited to items the viewer can access. The optional since
// query parameter (RFC 3339, WCORE-43) keeps entries that occurred at or after
// it, so a poller can fetch only new entries; see the route description in
// applyParameterCorrections for the contract.
func registerMilestoneActivityRoutes(builder *routeBuilder, deps Deps) {
	builder.Page("/milestones/{milestone_id}/activity", AuthAuthenticated, []string{"milestones:read"}, listMilestoneActivity(deps.MilestoneActivity))
}

func listMilestoneActivity(activity milestoneActivityApplication) pageOperation[models.MilestoneActivity] {
	return func(r *http.Request) ([]models.MilestoneActivity, Pagination, int, error) {
		user, milestoneID, err := planningTarget(r, "milestone_id")
		if err != nil {
			return nil, Pagination{}, 0, err
		}
		page, err := ParsePage(r)
		if err != nil {
			return nil, Pagination{}, 0, err
		}
		since, err := parseActivitySince(r)
		if err != nil {
			return nil, Pagination{}, 0, err
		}
		rows, total, err := activity.List(user.ID, milestoneID, services.MilestoneActivityListParams{
			Limit: page.PageSize, Offset: page.Offset, Since: since,
		})
		if err != nil {
			return nil, page, 0, planningError(err)
		}
		return rows, page, total, nil
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
