package sla

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"windshift/internal/models"
)

// ItemSLA returns the Jira-shaped SLA state for one item. It never writes;
// elapsed time is extended from stored columns with pure calendar math.
func (e *Engine) ItemSLA(ctx context.Context, itemID, workspaceID int) ([]models.ItemSLA, error) {
	statesByItem, err := e.ItemsSLA(ctx, workspaceID, []int{itemID})
	if err != nil {
		return nil, err
	}
	return statesByItem[itemID], nil
}

// ItemsSLA returns the Jira-shaped SLA state for a batch of items sharing one
// workspace. Metrics, recalculating flags, and coverage reference are loaded
// once for the batch instead of per item, so a list or board read costs a
// fixed handful of queries rather than several per row (WI-1591). Completed
// cycles are included for detail views.
func (e *Engine) ItemsSLA(ctx context.Context, workspaceID int, itemIDs []int) (map[int][]models.ItemSLA, error) {
	return e.itemsSLA(ctx, workspaceID, itemIDs, true)
}

// ItemsSLABadges returns only the ongoing cycle state needed to render badges
// for a batch of items. Completed cycles are neither loaded nor serialized, so
// response size is bounded by the number of active metrics rather than the
// item's ticket history.
func (e *Engine) ItemsSLABadges(ctx context.Context, workspaceID int, itemIDs []int) (map[int][]models.ItemSLA, error) {
	return e.itemsSLA(ctx, workspaceID, itemIDs, false)
}

func (e *Engine) itemsSLA(ctx context.Context, workspaceID int, itemIDs []int, includeCompleted bool) (map[int][]models.ItemSLA, error) {
	out := make(map[int][]models.ItemSLA, len(itemIDs))
	if len(itemIDs) == 0 {
		return out, nil
	}
	// Badge state only needs metric identity/name/display format; hydrating
	// conditions, goals, and targets here costs 1 + 2M + G queries per batch
	// for data the response never uses.
	metrics, err := e.repo.ListMetricSummaries(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	metricByID := make(map[int]models.SLAMetric, len(metrics))
	for _, metric := range metrics {
		metricByID[metric.ID] = metric
	}

	var grouped map[int][]models.ItemSLACycle
	if includeCompleted {
		grouped, err = e.repo.ListCyclesForItems(ctx, itemIDs)
	} else {
		grouped, err = e.repo.ListOngoingCyclesForItems(ctx, itemIDs)
	}
	if err != nil {
		return nil, err
	}
	recalculatingByItem, err := e.isRecalculatingItems(ctx, itemIDs, workspaceID)
	if err != nil {
		return nil, err
	}

	now := e.clock.Now()
	reference, referenceErr := e.ReferenceForWorkspace(ctx, workspaceID)
	for _, itemID := range itemIDs {
		cycles := grouped[itemID]
		groupedCycles := map[int][]models.ItemSLACycle{}
		var metricOrder []int
		for _, cycle := range cycles {
			if _, ok := groupedCycles[cycle.MetricID]; !ok {
				metricOrder = append(metricOrder, cycle.MetricID)
			}
			groupedCycles[cycle.MetricID] = append(groupedCycles[cycle.MetricID], cycle)
		}

		states := make([]models.ItemSLA, 0, len(metricOrder))
		for _, metricID := range metricOrder {
			metric := metricByID[metricID]
			state := models.ItemSLA{
				ItemID:        itemID,
				MetricID:      metricID,
				MetricName:    metric.Name,
				DisplayFormat: metric.DisplayFormat,
				Recalculating: recalculatingByItem[itemID],
			}
			for i := range groupedCycles[metricID] {
				cycle := groupedCycles[metricID][i]
				derived, err := Derive(&cycle, now)
				if err != nil {
					return nil, err
				}
				if referenceErr == nil {
					derived.Coverage = coverageForCycle(&cycle, reference, now)
				}
				if cycle.Status == models.SLACycleOngoing {
					state.Ongoing = &derived
				} else if includeCompleted {
					state.Completed = append(state.Completed, derived)
				}
			}
			states = append(states, state)
		}
		out[itemID] = states
	}
	return out, nil
}

// isRecalculatingItems reports, per item, whether a repair or metric
// recalculation affecting the item is still pending.
func (e *Engine) isRecalculatingItems(ctx context.Context, itemIDs []int, workspaceID int) (map[int]bool, error) {
	out := make(map[int]bool, len(itemIDs))
	if len(itemIDs) == 0 {
		return out, nil
	}
	itemParams := make([]any, 0, len(itemIDs))
	for _, id := range itemIDs {
		itemParams = append(itemParams, id)
	}
	// Item repairs are scoped to the item. A metric recalculation has no
	// item_id, so it is handled separately below.
	rows, err := e.db.QueryContext(ctx, `SELECT DISTINCT item_id FROM sla_jobs
		WHERE state = 'pending' AND kind = 'recalc_item' AND item_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(itemIDs)), ",")+`)`, itemParams...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var itemID int
		if err := rows.Scan(&itemID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out[itemID] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// A metric recalculation re-derives every open item in the workspace, so the
	// whole requested batch is pending while its job is queued.
	var metricJobs int
	if err := e.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sla_jobs
		WHERE state = 'pending' AND kind = 'recalc_metric'
		  AND metric_id IN (SELECT id FROM sla_metrics WHERE workspace_id = ?)`, workspaceID).Scan(&metricJobs); err != nil {
		return nil, err
	}
	if metricJobs > 0 {
		for _, id := range itemIDs {
			out[id] = true
		}
	}
	return out, nil
}

// Report returns the completed-cycle compliance report for a workspace. The
// optional coverage block is derived from stored snapshots and never fails the
// report.
func (e *Engine) Report(ctx context.Context, workspaceID int, from, to *time.Time) (*models.SLAReport, error) {
	report, err := e.repo.SLAReport(ctx, workspaceID, from, to)
	if err != nil {
		return nil, err
	}
	if coverage, coverageErr := e.reportCoverage(ctx, workspaceID, from, to); coverageErr == nil {
		report.Coverage = coverage
	}
	return report, nil
}

// WorkspaceForItem resolves the workspace that owns an item.
func (e *Engine) WorkspaceForItem(ctx context.Context, itemID int) (int, error) {
	var workspaceID int
	err := e.db.QueryRowContext(ctx, `SELECT workspace_id FROM items WHERE id = ?`, itemID).Scan(&workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("load item workspace: %w", err)
	}
	return workspaceID, nil
}

// ErrNotFound reports that an SLA subject does not exist.
var ErrNotFound = errors.New("sla: not found")
