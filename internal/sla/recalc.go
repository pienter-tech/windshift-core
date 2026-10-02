package sla

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"windshift/internal/database"
	"windshift/internal/itemevents"
	"windshift/internal/models"
	"windshift/internal/repository"
)

// DefaultRecalcPageSize is the number of items reconciled per transaction.
const DefaultRecalcPageSize = 200

// RecalculateItem reconciles one item's SLA cycles with its current state.
func (e *Engine) RecalculateItem(ctx context.Context, itemID int) error {
	return database.WithTx(e.db, func(tx database.Tx) error {
		return e.recalcItemTx(ctx, tx, itemID)
	})
}

// EnqueueRecalculation arms a recalc_metric job due now.
func (e *Engine) EnqueueRecalculation(ctx context.Context, metricID int) error {
	return database.WithTx(e.db, func(tx database.Tx) error {
		metric := metricID
		return e.repo.UpsertJob(ctx, tx, &models.SLAJob{Kind: models.SLAJobRecalcMetric, MetricID: &metric, DueAt: e.clock.Now()})
	})
}

// BumpConfigGeneration advances a workspace's configuration generation in the
// caller's transaction and returns the new value.
func (e *Engine) BumpConfigGeneration(ctx context.Context, tx database.Tx, workspaceID int) (int, error) {
	generation, err := e.repo.TouchWorkspaceState(ctx, tx, workspaceID)
	if err == nil {
		e.InvalidateWorkspace(workspaceID)
	}
	return generation, err
}

func (e *Engine) recalcItemTx(ctx context.Context, tx database.Tx, itemID int) error {
	snapshot, err := loadItemSnapshotTx(ctx, tx, itemID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if snapshot.WorkspaceID == 0 {
		return nil
	}
	generations, err := e.repo.GenerationsForWorkspacesTx(ctx, tx, []int{snapshot.WorkspaceID})
	if err != nil {
		return err
	}
	generation, ok := generations[snapshot.WorkspaceID]
	if !ok {
		return nil
	}
	config, err := e.configuration(ctx, snapshot.WorkspaceID, generation)
	if err != nil {
		return err
	}
	if len(config.metrics) == 0 {
		return nil
	}

	ongoingList, err := e.repo.LockOngoingCycles(ctx, tx, []int{itemID})
	if err != nil {
		return err
	}
	ongoing := map[cycleKey]*models.ItemSLACycle{}
	for i := range ongoingList {
		cycle := &ongoingList[i]
		ongoing[cycleKey{cycle.ItemID, cycle.MetricID}] = cycle
	}

	effectiveAt := e.clock.Now()
	for _, metric := range config.metrics {
		target := goalTarget{}
		if len(metric.goals) > 0 {
			resolved, err := e.resolveGoals(ctx, tx, config, metric, []int{itemID}, effectiveAt)
			if err != nil {
				return err
			}
			target = resolved[itemID]
		}
		key := cycleKey{itemID, metric.id}
		if err := e.reconcileMetric(ctx, tx, config, metric, snapshot.WorkspaceID, itemID, snapshot, ongoing[key], target, effectiveAt); err != nil {
			return err
		}
	}
	return nil
}

// reconcileMetric brings one item's cycle for a metric in line with current
// state. It is the same transition table as inline evaluation, driven by
// current-state condition matching.
func (e *Engine) reconcileMetric(ctx context.Context, tx database.Tx, config *compiledConfig, metric *compiledMetric, workspaceID, itemID int, snapshot itemevents.ItemSnapshot, cycle *models.ItemSLACycle, target goalTarget, effectiveAt time.Time) error {
	stop := metric.matchPhaseCurrent(models.SLAPhaseStop, snapshot, config)
	pause := metric.matchPhaseCurrent(models.SLAPhasePause, snapshot, config)
	start := metric.matchPhaseCurrent(models.SLAPhaseStart, snapshot, config)

	if cycle == nil {
		if start && !stop {
			_, err := e.startCycle(ctx, tx, config, metric, workspaceID, itemID, target, pause, effectiveAt, models.SLAOriginBackfill)
			return err
		}
		return nil
	}

	effective := effectiveAt
	if cycle.LastCalculatedAt.After(effective) {
		effective = cycle.LastCalculatedAt
	}
	if stop {
		return e.completeCycle(ctx, tx, config, workspaceID, cycle, effective)
	}
	if err := e.advanceCycle(config, cycle, effective); err != nil {
		return err
	}
	switch {
	case cycle.Paused && !pause:
		if err := e.resumeCycle(ctx, tx, config, metric, workspaceID, cycle, target, effective); err != nil {
			return err
		}
	case !cycle.Paused && pause:
		if err := e.pauseCycle(ctx, tx, workspaceID, cycle, effective); err != nil {
			return err
		}
	}
	if !cycle.Paused {
		if err := e.reGoalCycle(ctx, tx, config, metric, workspaceID, cycle, target, effective); err != nil {
			return err
		}
	}
	if err := e.repo.UpdateCycle(ctx, tx, cycle); err != nil {
		return err
	}
	// Re-arm jobs so a threshold or goal edit picked up by the recalculation is
	// reflected even when the matched goal did not change.
	if !cycle.Paused {
		return e.syncCycleJobs(ctx, tx, config, metric, cycle, effective)
	}
	return nil
}

// RecalculateMetric reconciles a page of open items for a metric. It returns
// the cursor to resume from and whether the metric is fully reconciled.
func (e *Engine) RecalculateMetric(ctx context.Context, metricID int, cursor string, pageSize int) (next string, done bool, err error) {
	if pageSize <= 0 {
		pageSize = DefaultRecalcPageSize
	}
	err = database.WithTx(e.db, func(tx database.Tx) error {
		var runErr error
		next, done, runErr = e.recalcMetricPageTx(ctx, tx, metricID, cursor, pageSize)
		return runErr
	})
	return next, done, err
}

func (e *Engine) recalcMetricPageTx(ctx context.Context, tx database.Tx, metricID int, cursor string, pageSize int) (nextCursor string, complete bool, err error) {
	metric, err := e.repo.GetMetric(ctx, metricID)
	if err != nil {
		return "", false, err
	}
	afterID := 0
	if cursor != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil {
			return "", false, fmt.Errorf("invalid recalc cursor %q", cursor)
		}
		afterID = parsed
	}

	rows, err := tx.QueryContext(ctx, `SELECT i.id FROM items i
		LEFT JOIN statuses st ON i.status_id = st.id
		LEFT JOIN status_categories sc ON st.category_id = sc.id
		WHERE i.workspace_id = ? AND i.id > ?
		  AND (sc.is_completed IS NULL OR sc.is_completed = false)
		ORDER BY i.id LIMIT ?`, metric.WorkspaceID, afterID, pageSize)
	if err != nil {
		return "", false, fmt.Errorf("page metric items: %w", err)
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return "", false, fmt.Errorf("scan metric item: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return "", false, err
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}

	if len(ids) > 0 {
		if err := e.recalcItemsForMetricTx(ctx, tx, metric, ids); err != nil {
			return "", false, err
		}
	}
	// Re-arm until a page comes back empty, so a short page still reconciles
	// once more and a crash mid-metric resumes from the last full page.
	if len(ids) == 0 {
		return "", true, nil
	}
	return strconv.Itoa(ids[len(ids)-1]), false, nil
}

// recalcItemsForMetricTx reconciles one metric across a page of items in
// batches: snapshots, cycle locks, and goal resolution each cost one query
// for the whole page, and only the job's metric is evaluated instead of
// every workspace metric per item (WI-1592).
func (e *Engine) recalcItemsForMetricTx(ctx context.Context, tx database.Tx, metric *models.SLAMetric, itemIDs []int) error {
	generations, err := e.repo.GenerationsForWorkspacesTx(ctx, tx, []int{metric.WorkspaceID})
	if err != nil {
		return err
	}
	generation, ok := generations[metric.WorkspaceID]
	if !ok {
		return nil
	}
	config, err := e.configuration(ctx, metric.WorkspaceID, generation)
	if err != nil {
		return err
	}
	compiled := config.metricsByID[metric.ID]
	if compiled == nil {
		return nil
	}

	snapshots, err := loadItemSnapshotsTx(ctx, tx, itemIDs)
	if err != nil {
		return err
	}

	ongoingList, err := e.repo.LockOngoingCycles(ctx, tx, itemIDs)
	if err != nil {
		return err
	}
	ongoing := map[cycleKey]*models.ItemSLACycle{}
	for i := range ongoingList {
		cycle := &ongoingList[i]
		if cycle.MetricID != metric.ID {
			continue
		}
		ongoing[cycleKey{cycle.ItemID, cycle.MetricID}] = cycle
	}

	effectiveAt := e.clock.Now()
	resolved := map[int]goalTarget{}
	if len(compiled.goals) > 0 {
		resolved, err = e.resolveGoals(ctx, tx, config, compiled, itemIDs, effectiveAt)
		if err != nil {
			return err
		}
	}

	for _, id := range itemIDs {
		snapshot, ok := snapshots[id]
		if !ok {
			continue
		}
		if err := e.reconcileMetric(ctx, tx, config, compiled, metric.WorkspaceID, id, snapshot, ongoing[cycleKey{id, metric.ID}], resolved[id], effectiveAt); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) recalcCalendarPageTx(ctx context.Context, tx database.Tx, metricID, calendarID int, cursor int64, pageSize int) (nextCursor int64, complete bool, err error) {
	metric, err := e.repo.GetMetric(ctx, metricID)
	if err != nil {
		return 0, false, err
	}
	ids, err := e.repo.OngoingCycleIDsForCalendarTx(ctx, tx, metricID, calendarID, cursor, pageSize)
	if err != nil {
		return 0, false, err
	}
	if len(ids) == 0 {
		return 0, true, nil
	}
	generations, err := e.repo.GenerationsForWorkspacesTx(ctx, tx, []int{metric.WorkspaceID})
	if err != nil {
		return 0, false, err
	}
	generation, ok := generations[metric.WorkspaceID]
	if !ok {
		return 0, true, nil
	}
	config, err := e.configuration(ctx, metric.WorkspaceID, generation)
	if err != nil {
		return 0, false, err
	}
	compiledMetric := config.metricsByID[metric.ID]
	if compiledMetric == nil {
		return 0, true, nil
	}
	effectiveAt := e.clock.Now()
	for _, id := range ids {
		cycle, err := e.repo.GetCycleForUpdate(ctx, tx, id)
		if errors.Is(err, repository.ErrNotFound) {
			continue
		}
		if err != nil {
			return 0, false, err
		}
		if cycle.Status != models.SLACycleOngoing || cycle.Paused || cycle.CalendarID == nil || *cycle.CalendarID != calendarID {
			continue
		}
		resolved, err := e.resolveGoals(ctx, tx, config, compiledMetric, []int{cycle.ItemID}, effectiveAt)
		if err != nil {
			return 0, false, err
		}
		if err := e.advanceCycle(config, cycle, effectiveAt); err != nil {
			return 0, false, err
		}
		if err := e.reGoalCycle(ctx, tx, config, compiledMetric, metric.WorkspaceID, cycle, resolved[cycle.ItemID], effectiveAt); err != nil {
			return 0, false, err
		}
		if err := e.repo.UpdateCycle(ctx, tx, cycle); err != nil {
			return 0, false, err
		}
		if err := e.syncCycleJobs(ctx, tx, config, compiledMetric, cycle, effectiveAt); err != nil {
			return 0, false, err
		}
	}
	return ids[len(ids)-1], false, nil
}

func loadItemSnapshotTx(ctx context.Context, tx database.Tx, itemID int) (itemevents.ItemSnapshot, error) {
	var snapshot itemevents.ItemSnapshot
	var statusID, assigneeID, priorityID sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id, workspace_id, status_id, assignee_id, priority_id FROM items WHERE id = ?`, itemID).
		Scan(&snapshot.ID, &snapshot.WorkspaceID, &statusID, &assigneeID, &priorityID)
	if err != nil {
		return itemevents.ItemSnapshot{}, err
	}
	if statusID.Valid {
		value := int(statusID.Int64)
		snapshot.StatusID = &value
	}
	if assigneeID.Valid {
		value := int(assigneeID.Int64)
		snapshot.AssigneeID = &value
	}
	if priorityID.Valid {
		value := int(priorityID.Int64)
		snapshot.PriorityID = &value
	}
	return snapshot, nil
}

// loadItemSnapshotsTx loads current snapshots for a batch of items in one
// query.
func loadItemSnapshotsTx(ctx context.Context, tx database.Tx, itemIDs []int) (map[int]itemevents.ItemSnapshot, error) {
	out := make(map[int]itemevents.ItemSnapshot, len(itemIDs))
	if len(itemIDs) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(itemIDs)), ",")
	args := make([]any, 0, len(itemIDs))
	for _, id := range itemIDs {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT id, workspace_id, status_id, assignee_id, priority_id FROM items WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("load item snapshots: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var snapshot itemevents.ItemSnapshot
		var statusID, assigneeID, priorityID sql.NullInt64
		if err := rows.Scan(&snapshot.ID, &snapshot.WorkspaceID, &statusID, &assigneeID, &priorityID); err != nil {
			return nil, fmt.Errorf("scan item snapshot: %w", err)
		}
		if statusID.Valid {
			value := int(statusID.Int64)
			snapshot.StatusID = &value
		}
		if assigneeID.Valid {
			value := int(assigneeID.Int64)
			snapshot.AssigneeID = &value
		}
		if priorityID.Valid {
			value := int(priorityID.Int64)
			snapshot.PriorityID = &value
		}
		out[snapshot.ID] = snapshot
	}
	return out, rows.Err()
}
