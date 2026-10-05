package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
)

// Support metric segmentation options. Values are a stable API contract.
const (
	SupportSegmentNone         = "none"
	SupportSegmentWorkspace    = "workspace"
	SupportSegmentChannel      = "channel"
	SupportSegmentRequestType  = "request_type"
	SupportSegmentTeam         = "team"
	SupportSegmentAgent        = "agent"
	SupportSegmentOrganisation = "organisation"
)

// Support bucket granularities for the trend series.
const (
	SupportBucketDay  = "day"
	SupportBucketWeek = "week"
)

var supportSegments = map[string]bool{
	SupportSegmentNone: true, SupportSegmentWorkspace: true, SupportSegmentChannel: true,
	SupportSegmentRequestType: true, SupportSegmentTeam: true, SupportSegmentAgent: true,
	SupportSegmentOrganisation: true,
}

var supportBuckets = map[string]bool{SupportBucketDay: true, SupportBucketWeek: true}

// SupportMetricsInputError reports a client-correctable metrics request
// problem; the endpoint maps it to 400 invalid_request.
type SupportMetricsInputError struct {
	Message string
}

func (e *SupportMetricsInputError) Error() string { return e.Message }

func supportMetricsInput(message string) error { return &SupportMetricsInputError{Message: message} }

// SupportMetricsQuery is one validated aggregate request. From/To are
// half-open UTC bounds; Location buckets civil days; UserID drives both the
// QL context (currentUser()) and workspace authorization.
type SupportMetricsQuery struct {
	UserID       int
	WorkspaceIDs []int
	CollectionID *int
	From, To     time.Time
	Location     *time.Location
	Bucket       string
	Segment      string
}

// supportSegmentExpr returns the SQL select expression and the label
// dimension for a segmentation, or empty strings for "none".
func supportSegmentExpr(segment string) (expr, dimension string) {
	switch segment {
	case SupportSegmentWorkspace:
		return "i.workspace_id", "workspace"
	case SupportSegmentChannel:
		return "i.channel_id", "channel"
	case SupportSegmentRequestType:
		return "i.request_type_id", "request_type"
	case SupportSegmentTeam:
		return "i.team_id", "team"
	case SupportSegmentAgent:
		return "i.assignee_id", "agent"
	case SupportSegmentOrganisation:
		return "pc.customer_organisation_id", "organisation"
	}
	return "", ""
}

// resolveSupportScope validates the requested scope and returns the effective
// workspace IDs plus the optional collection QL predicate. Authorization is
// fail-closed: any inaccessible scope is ErrNotFound (no existence leak).
func (s *ItemApplicationService) resolveSupportScope(ctx context.Context, q SupportMetricsQuery) ([]int, *resolvedItemListQL, error) {
	if q.CollectionID != nil {
		if len(q.WorkspaceIDs) > 0 {
			return nil, nil, supportMetricsInput("scope by workspace_id or collection_id, not both")
		}
		collection, err := repository.NewCollectionRepository(s.db).GetByID(*q.CollectionID)
		if err != nil {
			return nil, nil, repository.ErrNotFound
		}
		if collection.WorkspaceID == nil {
			return nil, nil, repository.ErrNotFound
		}
		if err := s.authorizeQueueRead(q.UserID, QueueScope{WorkspaceID: *collection.WorkspaceID, CollectionID: q.CollectionID}); err != nil {
			return nil, nil, repository.ErrNotFound
		}
		resolved, err := s.crud.resolveItemListQLContext(ctx, collection.QLQuery, 0, "", q.UserID)
		if err != nil {
			return nil, nil, err
		}
		return []int{*collection.WorkspaceID}, &resolved, nil
	}
	if len(q.WorkspaceIDs) == 0 {
		return nil, nil, supportMetricsInput("workspace_id or collection_id is required")
	}
	if len(q.WorkspaceIDs) > 10 {
		return nil, nil, supportMetricsInput("at most 10 workspaces per request")
	}
	for _, workspaceID := range q.WorkspaceIDs {
		var exists int
		if err := s.db.QueryRow("SELECT 1 FROM workspaces WHERE id = ? AND active = true", workspaceID).Scan(&exists); err != nil {
			return nil, nil, repository.ErrNotFound
		}
		if err := s.authorizeQueueRead(q.UserID, QueueScope{WorkspaceID: workspaceID}); err != nil {
			return nil, nil, repository.ErrNotFound
		}
	}
	return q.WorkspaceIDs, nil, nil
}

// supportScopeWhere builds the item-list WHERE clause shared by every
// metrics query: merged exclusion, the customer-facing ticket predicate
// (internal work items never appear in support metrics), workspace IN, and
// the optional QL.
func supportScopeWhere(workspaceIDs []int, resolvedQL *resolvedItemListQL) (whereClause string, whereArgs []any) {
	whereClause = "WHERE 1=1 AND i.merged_into_item_id IS NULL" +
		" AND (i.channel_id IS NOT NULL OR i.creator_portal_customer_id IS NOT NULL)"
	whereArgs = make([]any, 0, len(workspaceIDs))
	placeholders := make([]string, len(workspaceIDs))
	for i, id := range workspaceIDs {
		placeholders[i] = "?"
		whereArgs = append(whereArgs, id)
	}
	whereClause += " AND i.workspace_id IN (" + strings.Join(placeholders, ",") + ")"
	if resolvedQL != nil && resolvedQL.sql != "" {
		whereClause += " AND (" + resolvedQL.sql + ")"
		whereArgs = append(whereArgs, resolvedQL.args...)
	}
	return whereClause, whereArgs
}

// AggregateSupportMetrics computes the support metrics document for one
// validated scope. Scans are bounded by the request context and the range cap
// enforced at the endpoint.
func (s *ItemApplicationService) AggregateSupportMetrics(ctx context.Context, q SupportMetricsQuery) (*models.SupportMetricsAggregate, error) {
	workspaceIDs, resolvedQL, err := s.resolveSupportScope(ctx, q)
	if err != nil {
		return nil, err
	}
	if q.Location == nil {
		return nil, errors.New("timezone location is required")
	}
	if !supportBuckets[q.Bucket] {
		return nil, supportMetricsInput(fmt.Sprintf("bucket must be %s or %s", SupportBucketDay, SupportBucketWeek))
	}
	if !supportSegments[q.Segment] {
		return nil, supportMetricsInput(fmt.Sprintf("unsupported segment %q", q.Segment))
	}

	where, whereArgs := supportScopeWhere(workspaceIDs, resolvedQL)
	doc := &models.SupportMetricsAggregate{
		From:        q.From.In(q.Location).Format(time.DateOnly),
		To:          q.To.AddDate(0, 0, -1).In(q.Location).Format(time.DateOnly),
		Timezone:    q.Location.String(),
		Bucket:      q.Bucket,
		GeneratedAt: time.Now().UTC(),
	}

	trend, err := s.supportTrend(ctx, where, whereArgs, q)
	if err != nil {
		return nil, err
	}
	doc.Buckets = trend.buckets
	doc.Summary.Created = trend.createdTotal
	doc.Summary.Resolved = trend.resolvedTotal
	doc.Summary.Reopened = trend.reopenedTotal
	doc.Summary.FirstResponse = distribution(trend.firstResponse)
	doc.Summary.Resolution = distribution(trend.resolution)

	if doc.Summary.Backlog, doc.Summary.Unassigned, err = s.supportBacklogTotals(ctx, where, whereArgs); err != nil {
		return nil, err
	}

	if doc.Segments, err = s.supportSegmentRows(ctx, trend, where, whereArgs, q); err != nil {
		return nil, err
	}

	if doc.Summary.SLA, err = s.supportSLATotals(ctx, where, whereArgs, q); err != nil {
		return nil, err
	}

	if q.CollectionID == nil && len(workspaceIDs) == 1 {
		queues, err := s.ListQueues(ctx, q.UserID, workspaceIDs[0], nil)
		if err != nil {
			return nil, err
		}
		doc.Queues = make([]models.SupportMetricsQueueView, 0, len(queues))
		for _, queue := range queues {
			doc.Queues = append(doc.Queues, models.SupportMetricsQueueView{
				ID: queue.ID, Key: queue.Key, Name: queue.Name, Builtin: queue.Builtin, Count: queue.Count,
			})
		}
	}
	return doc, nil
}

// supportBucketStart maps a timestamp to the civil start of its bucket in the
// report timezone. Weeks are 7-civil-day buckets anchored at From.
func supportBucketStart(q SupportMetricsQuery, ts time.Time) time.Time {
	local := ts.In(q.Location)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, q.Location)
	if q.Bucket != SupportBucketWeek {
		return dayStart
	}
	anchorLocal := q.From.In(q.Location)
	anchor := time.Date(anchorLocal.Year(), anchorLocal.Month(), anchorLocal.Day(), 0, 0, 0, 0, q.Location)
	days := int(math.Round(dayStart.Sub(anchor).Hours() / 24))
	if days < 0 {
		days--
	}
	return anchor.AddDate(0, 0, (days/7)*7)
}

func supportBucketStep(q SupportMetricsQuery) int {
	if q.Bucket == SupportBucketWeek {
		return 7
	}
	return 1
}

// supportTrend accumulates streamed window facts.
type supportTrend struct {
	buckets       []models.SupportMetricsBucket
	bucketIndex   map[string]int
	createdTotal  int64
	resolvedTotal int64
	reopenedTotal int64

	createdTimes  []time.Time
	resolvedTimes []time.Time
	firstResponse []float64
	resolution    []float64

	firstResponseByBucket map[string][]float64
	resolutionByBucket    map[string][]float64

	createdSegment *segmentAccumulator
	eventSegment   *segmentAccumulator
}

// supportTrend streams created tickets and fact events, then reconstructs the
// open-at-bucket-end series and bucket medians.
func (s *ItemApplicationService) supportTrend(ctx context.Context, where string, whereArgs []any, q SupportMetricsQuery) (*supportTrend, error) {
	trend := &supportTrend{
		bucketIndex:           map[string]int{},
		firstResponseByBucket: map[string][]float64{},
		resolutionByBucket:    map[string][]float64{},
	}
	for start := q.From.In(q.Location); start.Before(q.To); start = start.AddDate(0, 0, supportBucketStep(q)) {
		trend.bucketIndex[start.Format(time.DateOnly)] = len(trend.buckets)
		trend.buckets = append(trend.buckets, models.SupportMetricsBucket{BucketStart: start.Format(time.DateOnly)})
	}
	if trend.buckets == nil {
		trend.buckets = []models.SupportMetricsBucket{}
	}

	segExpr, _ := supportSegmentExpr(q.Segment)
	if segExpr == "" {
		segExpr = "NULL"
	}

	createdQuery := `SELECT i.created_at, ` + segExpr + ` ` + repository.ItemListFilterFromClause() + `
		LEFT JOIN portal_customers pc ON pc.id = i.creator_portal_customer_id ` + where +
		` AND i.created_at >= ? AND i.created_at < ?`
	createdArgs := append(append([]any{}, whereArgs...), q.From, q.To)
	trend.createdSegment = newSegmentAccumulator(q.Segment)
	if err := streamRows(ctx, s.db, "created tickets", createdQuery, createdArgs,
		func(rows *sql.Rows) (createdTicketRow, error) {
			var row createdTicketRow
			return row, rows.Scan(&row.createdAt, &row.segment)
		},
		func(row createdTicketRow) error {
			createdAt := row.createdAt
			segment := row.segment
			key := supportBucketStart(q, createdAt).Format(time.DateOnly)
			if bucket, ok := trend.bucketIndex[key]; ok {
				trend.buckets[bucket].Created++
			}
			trend.createdTotal++
			trend.createdTimes = append(trend.createdTimes, createdAt)
			trend.createdSegment.apply(segment, func(v *models.SupportMetricsSegment) { v.Created++ })
			return nil
		}); err != nil {
		return nil, err
	}

	eventsQuery := `SELECT e.kind, e.occurred_at, i.created_at, ` + segExpr + ` ` +
		repository.ItemListFilterFromClause() + `
		JOIN item_support_events e ON e.item_id = i.id
		LEFT JOIN portal_customers pc ON pc.id = i.creator_portal_customer_id ` + where +
		` AND e.occurred_at >= ? AND e.occurred_at < ?`
	eventArgs := append(append([]any{}, whereArgs...), q.From, q.To)
	trend.eventSegment = newSegmentAccumulator(q.Segment)
	if err := streamRows(ctx, s.db, "support events", eventsQuery, eventArgs,
		func(rows *sql.Rows) (supportEventRow, error) {
			var row supportEventRow
			return row, rows.Scan(&row.kind, &row.occurredAt, &row.itemCreated, &row.segment)
		},
		func(row supportEventRow) error {
			kind := row.kind
			occurredAt := row.occurredAt
			itemCreated := row.itemCreated
			segment := row.segment
			key := supportBucketStart(q, occurredAt).Format(time.DateOnly)
			switch kind {
			case models.SupportEventResolved:
				if bucket, ok := trend.bucketIndex[key]; ok {
					trend.buckets[bucket].Resolved++
				}
				trend.resolvedTotal++
				trend.resolvedTimes = append(trend.resolvedTimes, occurredAt)
				duration := occurredAt.Sub(itemCreated).Seconds() * 1000
				trend.resolution = append(trend.resolution, duration)
				trend.resolutionByBucket[key] = append(trend.resolutionByBucket[key], duration)
			case models.SupportEventReopened:
				if bucket, ok := trend.bucketIndex[key]; ok {
					trend.buckets[bucket].Reopened++
				}
				trend.reopenedTotal++
				trend.eventSegment.apply(segment, func(v *models.SupportMetricsSegment) { v.Reopened++ })
			case models.SupportEventFirstResponse:
				duration := occurredAt.Sub(itemCreated).Seconds() * 1000
				trend.firstResponse = append(trend.firstResponse, duration)
				trend.firstResponseByBucket[key] = append(trend.firstResponseByBucket[key], duration)
			}
			return nil
		}); err != nil {
		return nil, err
	}

	trend.finish(q)
	return trend, nil
}

func (t *supportTrend) finish(q SupportMetricsQuery) {
	sort.Slice(t.createdTimes, func(i, j int) bool { return t.createdTimes[i].Before(t.createdTimes[j]) })
	sort.Slice(t.resolvedTimes, func(i, j int) bool { return t.resolvedTimes[i].Before(t.resolvedTimes[j]) })

	createdIdx, resolvedIdx := 0, 0
	open := 0
	for i := range t.buckets {
		bucketStart, err := time.ParseInLocation(time.DateOnly, t.buckets[i].BucketStart, q.Location)
		if err != nil {
			continue
		}
		bucketEnd := bucketStart.AddDate(0, 0, supportBucketStep(q))
		for createdIdx < len(t.createdTimes) && t.createdTimes[createdIdx].Before(bucketEnd) {
			open++
			createdIdx++
		}
		for resolvedIdx < len(t.resolvedTimes) && t.resolvedTimes[resolvedIdx].Before(bucketEnd) {
			open--
			resolvedIdx++
		}
		if open < 0 {
			open = 0
		}
		t.buckets[i].BacklogEnd = int64(open)

		key := t.buckets[i].BucketStart
		if durations := t.firstResponseByBucket[key]; len(durations) > 0 {
			value := percentile(durations, 0.5)
			t.buckets[i].FirstResponseP50Ms = &value
		}
		if durations := t.resolutionByBucket[key]; len(durations) > 0 {
			value := percentile(durations, 0.5)
			t.buckets[i].ResolutionP50Ms = &value
		}
	}
}

// streamRows runs one bounded streaming scan: scan builds the row value,
// handle consumes it. Both run inside the rows loop so ctx-cancel stops the
// scan mid-flight.
func streamRows[T any](ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, label, query string, args []any, scan func(*sql.Rows) (T, error), handle func(T) error) error {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("stream %s: %w", label, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		value, err := scan(rows)
		if err != nil {
			return fmt.Errorf("scan %s: %w", label, err)
		}
		if err := handle(value); err != nil {
			return fmt.Errorf("handle %s: %w", label, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s: %w", label, err)
	}
	return rows.Close()
}

type createdTicketRow struct {
	createdAt time.Time
	segment   sql.NullInt64
}

type supportEventRow struct {
	kind        string
	occurredAt  time.Time
	itemCreated time.Time
	segment     sql.NullInt64
}

type segmentCountRow struct {
	segment sql.NullInt64
	count   int64
}

// segmentAccumulator collects per-segment counters during a stream and keeps
// the seen ids for batched label resolution.
type segmentAccumulator struct {
	segment string
	rows    map[string]*models.SupportMetricsSegment
	ids     map[int64]bool
}

func newSegmentAccumulator(segment string) *segmentAccumulator {
	return &segmentAccumulator{segment: segment, rows: map[string]*models.SupportMetricsSegment{}, ids: map[int64]bool{}}
}

func (a *segmentAccumulator) apply(value sql.NullInt64, mutate func(*models.SupportMetricsSegment)) {
	if a.segment == SupportSegmentNone {
		return
	}
	var key string
	var id int64
	if value.Valid {
		key = fmt.Sprintf("%s:%d", a.segment, value.Int64)
		id = value.Int64
		a.ids[id] = true
	} else {
		key = a.segment + ":"
	}
	entry, ok := a.rows[key]
	if !ok {
		entry = &models.SupportMetricsSegment{}
		a.rows[key] = entry
	}
	mutate(entry)
}

// supportBacklogTotals returns the live open count and unassigned subset for
// the scope (no window restriction) — the widget's headline numbers.
func (s *ItemApplicationService) supportBacklogTotals(ctx context.Context, where string, whereArgs []any) (backlog, unassigned int64, err error) {
	base := "SELECT COUNT(*) " + repository.ItemListFilterFromClause() + where
	if err = s.db.QueryRowContext(ctx, base+" AND COALESCE(sc.is_completed, false) = false", whereArgs...).Scan(&backlog); err != nil {
		return 0, 0, fmt.Errorf("count backlog: %w", err)
	}
	if err = s.db.QueryRowContext(ctx, base+" AND COALESCE(sc.is_completed, false) = false AND i.assignee_id IS NULL AND i.team_id IS NULL", whereArgs...).Scan(&unassigned); err != nil {
		return 0, 0, fmt.Errorf("count unassigned backlog: %w", err)
	}
	return backlog, unassigned, nil
}

// supportSegmentRows assembles the segment list from the streamed
// accumulators plus live backlog and SLA breaches per segment.
func (s *ItemApplicationService) supportSegmentRows(ctx context.Context, trend *supportTrend, where string, whereArgs []any, q SupportMetricsQuery) ([]models.SupportMetricsSegment, error) {
	if q.Segment == SupportSegmentNone {
		return []models.SupportMetricsSegment{}, nil
	}
	segExpr, dimension := supportSegmentExpr(q.Segment)

	backlogBySegment, err := s.supportBacklogBySegment(ctx, segExpr, where, whereArgs)
	if err != nil {
		return nil, err
	}
	breachedBySegment, err := s.supportBreachedBySegment(ctx, segExpr, where, whereArgs, q)
	if err != nil {
		return nil, err
	}

	type partial struct {
		entry    models.SupportMetricsSegment
		id       int64
		hasValue bool
	}
	merged := map[string]*partial{}
	collect := func(acc *segmentAccumulator) {
		for key, value := range acc.rows {
			target, ok := merged[key]
			if !ok {
				target = &partial{}
				merged[key] = target
				if rest, isSet := strings.CutPrefix(key, acc.segment+":"); isSet && rest != "" {
					target.id = parseID(rest)
					target.hasValue = true
				}
			}
			target.entry.Created += value.Created
			target.entry.Reopened += value.Reopened
		}
	}
	collect(trend.createdSegment)
	collect(trend.eventSegment)

	ids := make([]int64, 0, len(merged))
	for _, value := range merged {
		if value.hasValue {
			ids = append(ids, value.id)
		}
	}
	labels, err := s.supportSegmentLabels(ctx, dimension, ids)
	if err != nil {
		return nil, err
	}

	rows := make([]models.SupportMetricsSegment, 0, len(merged))
	for _, value := range merged {
		entry := value.entry
		if value.hasValue {
			entry.Key = fmt.Sprintf("%d", value.id)
			entry.Label = labels[value.id]
		}
		entry.Backlog = backlogBySegment[value.id]
		entry.SLABreached = breachedBySegment[value.id]
		rows = append(rows, entry)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows, nil
}

func parseID(value string) int64 {
	var parsed int64
	_, _ = fmt.Sscanf(value, "%d", &parsed)
	return parsed
}

func (s *ItemApplicationService) supportBacklogBySegment(ctx context.Context, segExpr, where string, whereArgs []any) (map[int64]int64, error) {
	query := `SELECT ` + segExpr + `, COUNT(*) ` + repository.ItemListFilterFromClause() + `
		LEFT JOIN portal_customers pc ON pc.id = i.creator_portal_customer_id ` + where +
		` AND COALESCE(sc.is_completed, false) = false GROUP BY ` + segExpr
	result := map[int64]int64{}
	err := streamRows(ctx, s.db, "backlog segment", query, whereArgs,
		func(rows *sql.Rows) (segmentCountRow, error) {
			var row segmentCountRow
			return row, rows.Scan(&row.segment, &row.count)
		},
		func(row segmentCountRow) error {
			if row.segment.Valid {
				result[row.segment.Int64] = row.count
			}
			return nil
		})
	return result, err
}

// supportSLATotals counts completed SLA cycles in the window over the scoped
// item set. Cycles are calendar-aware by construction.
func (s *ItemApplicationService) supportSLATotals(ctx context.Context, where string, whereArgs []any, q SupportMetricsQuery) (*models.SupportMetricsSLA, error) {
	query := `SELECT COUNT(c.id), COALESCE(SUM(CASE WHEN c.breached_at IS NOT NULL THEN 1 ELSE 0 END), 0)
		FROM item_sla_cycles c
		JOIN (SELECT i.id ` + repository.ItemListFilterFromClause() + where + `) scoped ON scoped.id = c.item_id
		WHERE c.status = 'completed' AND c.stopped_at >= ? AND c.stopped_at < ?`
	args := append(append([]any{}, whereArgs...), q.From, q.To)
	var completed, breached int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&completed, &breached); err != nil {
		return nil, fmt.Errorf("SLA totals: %w", err)
	}
	sla := &models.SupportMetricsSLA{Completed: completed, Breached: breached}
	if completed > 0 {
		sla.CompliantPct = float64(completed-breached) / float64(completed) * 100
	}
	return sla, nil
}

func (s *ItemApplicationService) supportBreachedBySegment(ctx context.Context, segExpr, where string, whereArgs []any, q SupportMetricsQuery) (map[int64]int64, error) {
	query := `SELECT scoped.seg, COUNT(c.id)
		FROM item_sla_cycles c
		JOIN (SELECT i.id, ` + segExpr + ` AS seg ` + repository.ItemListFilterFromClause() + `
			LEFT JOIN portal_customers pc ON pc.id = i.creator_portal_customer_id ` + where + `) scoped ON scoped.id = c.item_id
		WHERE c.status = 'completed' AND c.stopped_at >= ? AND c.stopped_at < ?
		GROUP BY scoped.seg`
	args := append(append([]any{}, whereArgs...), q.From, q.To)
	result := map[int64]int64{}
	err := streamRows(ctx, s.db, "SLA segment", query, args,
		func(rows *sql.Rows) (segmentCountRow, error) {
			var row segmentCountRow
			return row, rows.Scan(&row.segment, &row.count)
		},
		func(row segmentCountRow) error {
			if row.segment.Valid {
				result[row.segment.Int64] = row.count
			}
			return nil
		})
	return result, err
}

// supportSegmentLabels resolves display labels for segment ids in batched
// lookups (chunked like the analytics ID batching).
const supportSegmentLabelChunk = 400

func (s *ItemApplicationService) supportSegmentLabels(ctx context.Context, dimension string, ids []int64) (map[int64]string, error) {
	labels := map[int64]string{}
	if len(ids) == 0 {
		return labels, nil
	}
	var query string
	switch dimension {
	case "workspace":
		query = "SELECT id, key FROM workspaces WHERE id IN (%s)"
	case "channel":
		query = "SELECT id, name FROM channels WHERE id IN (%s)"
	case "request_type":
		query = "SELECT id, name FROM request_types WHERE id IN (%s)"
	case "team":
		query = "SELECT id, name FROM teams WHERE id IN (%s)"
	case "agent":
		query = `SELECT id, COALESCE(NULLIF(TRIM(first_name || ' ' || last_name), ' '), username) FROM users WHERE id IN (%s)`
	case "organisation":
		query = "SELECT id, name FROM customer_organisations WHERE id IN (%s)"
	default:
		return labels, nil
	}
	for start := 0; start < len(ids); start += supportSegmentLabelChunk {
		end := start + supportSegmentLabelChunk
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(query, strings.Join(placeholders, ",")), args...)
		if err != nil {
			return nil, fmt.Errorf("resolve %s labels: %w", dimension, err)
		}
		for rows.Next() {
			var id int64
			var label string
			if err := rows.Scan(&id, &label); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan %s label: %w", dimension, err)
			}
			labels[id] = label
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate %s labels: %w", dimension, err)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return labels, nil
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64{}, values...)
	sort.Float64s(sorted)
	return sorted[int(p*float64(len(sorted)-1))]
}

func distribution(values []float64) *models.SupportMetricsDistribution {
	if len(values) == 0 {
		return nil
	}
	return &models.SupportMetricsDistribution{
		P50Ms: percentile(values, 0.5),
		P90Ms: percentile(values, 0.9),
		Count: int64(len(values)),
	}
}
