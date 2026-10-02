package sla

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

// resolveGoals matches each item against a metric's ordered goals in one
// statement and selects the winning priority or fallback target.
func (e *Engine) resolveGoals(ctx context.Context, queryer database.Tx, config *compiledConfig, metric *compiledMetric, itemIDs []int, effectiveAt time.Time) (map[int]goalTarget, error) {
	resolved := make(map[int]goalTarget, len(itemIDs))
	if len(itemIDs) == 0 {
		return resolved, nil
	}
	if len(metric.goals) == 0 {
		return resolved, nil
	}

	var caseSQL strings.Builder
	caseSQL.WriteString("CASE")
	args := make([]any, 0, len(metric.goals))
	usableGoals := make([]*compiledGoal, 0, len(metric.goals))
	for _, goal := range metric.goals {
		clause := "1 = 1"
		if goal.ast != nil {
			generated, goalArgs, err := config.generator.GenerateSQLAt(goal.ast, effectiveAt)
			if err != nil {
				return nil, fmt.Errorf("generate goal %d SQL: %w", goal.id, err)
			}
			clause = generated
			args = append(args, goalArgs...)
		}
		fmt.Fprintf(&caseSQL, " WHEN (%s) THEN %d", clause, goal.id)
		usableGoals = append(usableGoals, goal)
	}
	caseSQL.WriteString(" ELSE NULL END")

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(itemIDs)), ",")
	queryArgs := make([]any, 0, len(args)+len(itemIDs))
	queryArgs = append(queryArgs, args...)
	for _, id := range itemIDs {
		queryArgs = append(queryArgs, id)
	}
	query := `SELECT i.id, i.priority_id, ` + caseSQL.String() + ` ` + repository.ItemListFilterFromClause() + ` WHERE i.id IN (` + placeholders + `)`

	rows, err := queryer.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("match metric %d goals: %w", metric.id, err)
	}
	defer func() { _ = rows.Close() }()

	goalsByID := make(map[int]*compiledGoal, len(usableGoals))
	for _, goal := range usableGoals {
		goalsByID[goal.id] = goal
	}
	for rows.Next() {
		var itemID int
		var priorityID, matchedGoalID sql.NullInt64
		if err := rows.Scan(&itemID, &priorityID, &matchedGoalID); err != nil {
			return nil, fmt.Errorf("scan goal match: %w", err)
		}
		if !matchedGoalID.Valid {
			resolved[itemID] = goalTarget{matched: false}
			continue
		}
		goal := goalsByID[int(matchedGoalID.Int64)]
		var priority *int
		if priorityID.Valid {
			value := int(priorityID.Int64)
			priority = &value
		}
		target := selectTarget(goal, priority)
		resolved[itemID] = goalTarget{goalID: goal.id, target: target, query: goal.ql, matched: target != nil}
	}
	return resolved, rows.Err()
}

// selectTarget picks the priority-scoped target matching the item's priority,
// falling back to the goal's fallback target last.
func selectTarget(goal *compiledGoal, priority *int) *models.SLAGoalTarget {
	var fallback *models.SLAGoalTarget
	for i := range goal.targets {
		target := &goal.targets[i]
		if target.IsFallback {
			if fallback == nil {
				fallback = target
			}
			continue
		}
		if priority != nil && target.PriorityID != nil && *target.PriorityID == *priority {
			return target
		}
	}
	return fallback
}
