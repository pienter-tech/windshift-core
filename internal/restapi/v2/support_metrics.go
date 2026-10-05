package v2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
)

func registerSupportMetricsRoutes(builder *routeBuilder, deps Deps) {
	builder.RawResponse[*models.SupportMetricsAggregate](http.MethodGet, "/support/metrics/aggregate", http.StatusOK, "application/json", AuthAuthenticated, []string{"items:read"}, supportMetricsAggregate(deps))
}

// supportMetricsAggregate serves the support metrics document. The scope,
// range cap, and permission checks mirror the other aggregate reports: a
// report's date span bounds its work, and an inaccessible scope is
// indistinguishable from a missing one.
func supportMetricsAggregate(deps Deps) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		user, err := principal(r)
		if err != nil {
			return err
		}
		location, err := reportTimezone(r, user)
		if err != nil {
			return err
		}
		query, err := parseSupportMetricsQuery(r, user.ID, location)
		if err != nil {
			return err
		}
		doc, err := deps.ItemApplication.AggregateSupportMetrics(r.Context(), *query)
		if err != nil {
			var inputErr *services.SupportMetricsInputError
			if errors.As(err, &inputErr) {
				return newError(http.StatusBadRequest, "invalid_request", inputErr.Message)
			}
			if errors.Is(err, repository.ErrNotFound) {
				return newError(http.StatusNotFound, "not_found", "Workspace not found")
			}
			if errors.Is(err, context.Canceled) {
				return newError(http.StatusRequestTimeout, "request_canceled", "Metrics request was canceled")
			}
			return internalError(err)
		}
		return writeJSON(w, http.StatusOK, doc)
	}
}

// maxSupportMetricsRangeDays bounds one metrics request (WI-1598 pattern).
const maxSupportMetricsRangeDays = 366

func parseSupportMetricsQuery(r *http.Request, userID int, location *time.Location) (*services.SupportMetricsQuery, error) {
	query := &services.SupportMetricsQuery{UserID: userID, Location: location, Bucket: services.SupportBucketDay, Segment: services.SupportSegmentNone}

	for _, raw := range r.URL.Query()["workspace_id"] {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := strconv.Atoi(part)
			if err != nil || id < 1 {
				return nil, newError(http.StatusBadRequest, "invalid_request", "workspace_id must be a positive integer")
			}
			query.WorkspaceIDs = append(query.WorkspaceIDs, id)
		}
	}
	if raw := r.URL.Query().Get("collection_id"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id < 1 {
			return nil, newError(http.StatusBadRequest, "invalid_request", "collection_id must be a positive integer")
		}
		query.CollectionID = &id
	}
	if query.CollectionID != nil && len(query.WorkspaceIDs) > 0 {
		return nil, newError(http.StatusBadRequest, "invalid_request", "scope by workspace_id or collection_id, not both")
	}
	if query.CollectionID == nil && len(query.WorkspaceIDs) == 0 {
		return nil, newError(http.StatusBadRequest, "invalid_request", "workspace_id or collection_id is required")
	}

	today := time.Now().In(location)
	to := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, location).AddDate(0, 0, 1)
	from := to.AddDate(0, 0, -28)
	if raw := r.URL.Query().Get("from"); raw != "" {
		parsed, err := time.ParseInLocation(time.DateOnly, raw, location)
		if err != nil {
			return nil, newError(http.StatusBadRequest, "invalid_request", "from must use YYYY-MM-DD")
		}
		from = parsed
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		parsed, err := time.ParseInLocation(time.DateOnly, raw, location)
		if err != nil {
			return nil, newError(http.StatusBadRequest, "invalid_request", "to must use YYYY-MM-DD")
		}
		to = parsed.AddDate(0, 0, 1)
	}
	if to.Before(from) {
		return nil, newError(http.StatusBadRequest, "invalid_request", "to must not precede from")
	}
	if from.AddDate(0, 0, maxSupportMetricsRangeDays).Before(to) {
		return nil, newError(http.StatusBadRequest, "invalid_request",
			fmt.Sprintf("range exceeds %d days; split the request into smaller ranges", maxSupportMetricsRangeDays))
	}
	query.From, query.To = from, to

	if raw := r.URL.Query().Get("bucket"); raw != "" {
		query.Bucket = raw
	}
	if raw := r.URL.Query().Get("segment"); raw != "" {
		query.Segment = raw
	}
	return query, nil
}
