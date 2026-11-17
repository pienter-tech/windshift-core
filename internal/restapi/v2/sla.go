package v2

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"windshift/internal/models"
)

// registerSLARoutes publishes the read-only SLA state on the canonical v2
// surface. Portal customers receive nothing; the routes require items:read and
// the same workspace visibility check as item reads.
func registerSLARoutes(builder *routeBuilder, deps Deps) {
	builder.Read("/items/{item_id}/sla", AuthAuthenticated, []string{"items:read"}, itemSLA(deps))
	builder.Read("/workspaces/{workspace_id}/items/sla", AuthAuthenticated, []string{"items:read"}, workspaceItemsSLA(deps))
	builder.Read("/workspaces/{workspace_id}/sla/report", AuthAuthenticated, []string{"items:read"}, workspaceSLAReport(deps))
}

// maxBatchItemSLAIDs bounds one batch read so a single request cannot fan out
// unbounded work.
const batchItemSLAIDLimit = 200

// workspaceItemsSLA returns SLA state for many items of one workspace in a
// single request. The workspace visibility check and the workspace-scoped
// item lookup keep the batch exactly as restrictive as per-item reads
// (WI-1591).
func workspaceItemsSLA(deps Deps) readOperation[map[int][]models.ItemSLA] {
	return func(r *http.Request) (map[int][]models.ItemSLA, error) {
		if deps.SLA == nil {
			return nil, newError(http.StatusNotFound, "not_found", "Workspace was not found")
		}
		workspaceID, err := pathID(r, "workspace_id")
		if err != nil {
			return nil, err
		}
		user, err := principal(r)
		if err != nil {
			return nil, err
		}
		if err := requireWorkspace(deps.Access.CanViewWorkspace, user.ID, workspaceID); err != nil {
			return nil, err
		}
		ids, err := queryItemIDs(r, "ids", batchItemSLAIDLimit)
		if err != nil {
			return nil, err
		}
		items, err := deps.Items.FindByIDsInWorkspace(r.Context(), workspaceID, ids)
		if err != nil {
			return nil, internalError(err)
		}
		itemIDs := make([]int, 0, len(items))
		for _, item := range items {
			itemIDs = append(itemIDs, item.ID)
		}
		states, err := deps.SLA.ItemsSLABadges(r.Context(), workspaceID, itemIDs)
		if err != nil {
			return nil, internalError(err)
		}
		return states, nil
	}
}

// queryItemIDs parses a comma-separated id list parameter with a hard cap.
func queryItemIDs(r *http.Request, name string, limit int) ([]int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, newError(http.StatusBadRequest, "invalid_input", name+" is required")
	}
	parts := strings.Split(raw, ",")
	seen := make(map[int]bool, len(parts))
	ids := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value <= 0 {
			return nil, newError(http.StatusBadRequest, "invalid_input", name+" must be positive integers")
		}
		if !seen[value] {
			seen[value] = true
			ids = append(ids, value)
		}
	}
	if len(ids) > limit {
		return nil, newError(http.StatusBadRequest, "invalid_input", fmt.Sprintf("%s accepts at most %d ids", name, limit))
	}
	return ids, nil
}

func itemSLA(deps Deps) readOperation[[]models.ItemSLA] {
	return func(r *http.Request) ([]models.ItemSLA, error) {
		if deps.SLA == nil {
			return nil, newError(http.StatusNotFound, "not_found", "Item was not found")
		}
		item, err := requireItem(r, deps, deps.Access.CanViewWorkspace)
		if err != nil {
			return nil, err
		}
		states, err := deps.SLA.ItemSLA(r.Context(), item.ID, item.WorkspaceID)
		if err != nil {
			return nil, internalError(err)
		}
		return states, nil
	}
}

func workspaceSLAReport(deps Deps) readOperation[models.SLAReport] {
	return func(r *http.Request) (models.SLAReport, error) {
		if deps.SLA == nil {
			return models.SLAReport{}, newError(http.StatusNotFound, "not_found", "Workspace was not found")
		}
		workspaceID, err := pathID(r, "workspace_id")
		if err != nil {
			return models.SLAReport{}, err
		}
		user, err := principal(r)
		if err != nil {
			return models.SLAReport{}, err
		}
		if err := requireWorkspace(deps.Access.CanViewWorkspace, user.ID, workspaceID); err != nil {
			return models.SLAReport{}, err
		}
		from, err := querySLATime(r, "from")
		if err != nil {
			return models.SLAReport{}, err
		}
		to, err := querySLATime(r, "to")
		if err != nil {
			return models.SLAReport{}, err
		}
		report, err := deps.SLA.Report(r.Context(), workspaceID, from, to)
		if err != nil {
			return models.SLAReport{}, internalError(err)
		}
		return *report, nil
	}
}

func querySLATime(r *http.Request, name string) (*time.Time, error) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		parsed, err = time.Parse(time.DateOnly, value)
	}
	if err != nil {
		return nil, newError(http.StatusBadRequest, "invalid_input", name+" must be RFC3339 or YYYY-MM-DD")
	}
	return &parsed, nil
}
