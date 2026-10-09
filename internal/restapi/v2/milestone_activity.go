package v2

import (
	"net/http"

	"windshift/internal/models"
	"windshift/internal/services"
)

// registerMilestoneActivityRoutes publishes a milestone's Activity feed:
// milestone comments, description/status/target-date changes,
// linked pages, and member-item comments, status changes, and membership
// changes, newest first. The same route serves global and local milestones;
// item entries are limited to items the viewer can access.
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
		rows, total, err := activity.List(user.ID, milestoneID, services.MilestoneActivityListParams{
			Limit: page.PageSize, Offset: page.Offset,
		})
		if err != nil {
			return nil, page, 0, planningError(err)
		}
		return rows, page, total, nil
	}
}
