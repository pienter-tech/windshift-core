package sla

import (
	"context"
	"time"

	"windshift/internal/businesstime"
	"windshift/internal/models"
)

// Coverage reference states.
const (
	CoverageReferenceTeamServiceHours = "team_service_hours"
	CoverageReferenceNone             = "none"
	CoverageReferenceUnknown          = "unknown"

	coverageDiscrepancyAligned   = "aligned"
	coverageDiscrepancySLAWider  = "sla_wider"
	coverageDiscrepancyTeamWider = "team_wider"
	coverageDiscrepancyBoth      = "both"

	coverageNoteSLAWider  = "sla_outside_team_service_hours"
	coverageNoteTeamWider = "team_service_hours_outside_sla"
	coverageNoteBoth      = "sla_and_team_service_hours_differ"
)

// CoverageReference is the resolved union of a workspace's bound teams'
// service-hours calendars.
type CoverageReference struct {
	TeamIDs    []int
	Calendars  []*businesstime.Calendar
	Unresolved bool
}

// ReferenceForWorkspace resolves the workspace's reference team service hours.
// No bound team yields reference none. A calendar that cannot be compiled marks
// the reference unknown rather than failing the read.
func (e *Engine) ReferenceForWorkspace(ctx context.Context, workspaceID int) (CoverageReference, error) {
	teamIDs, err := e.repo.ListBoundTeamIDs(ctx, workspaceID)
	if err != nil {
		return CoverageReference{}, err
	}
	if len(teamIDs) == 0 {
		return CoverageReference{}, nil
	}
	calendars, err := e.repo.ListBindableTeamCalendars(ctx, workspaceID)
	if err != nil {
		return CoverageReference{}, err
	}
	reference := CoverageReference{TeamIDs: teamIDs}
	for i := range calendars {
		raw, err := businesstime.FromStored(calendars[i].Timezone, calendars[i].WeeklyIntervals, calendars[i].Holidays)
		if err != nil {
			reference.Unresolved = true
			continue
		}
		compiled, err := businesstime.Compile(raw)
		if err != nil {
			reference.Unresolved = true
			continue
		}
		reference.Calendars = append(reference.Calendars, compiled)
	}
	return reference, nil
}

// CycleCoverage builds the informative coverage block for one cycle's
// scheduling calendar. It never returns an error: an unresolvable reference is
// reported as a state.
func CycleCoverage(scheduling *businesstime.Calendar, reference CoverageReference, now time.Time) *models.SLACoverage {
	if len(reference.TeamIDs) == 0 {
		return &models.SLACoverage{Reference: CoverageReferenceNone}
	}
	if reference.Unresolved || scheduling == nil {
		return &models.SLACoverage{Reference: CoverageReferenceUnknown, TeamIDs: reference.TeamIDs}
	}
	sla, team, overlap := businesstime.WeeklyCoverage(scheduling, reference.Calendars)
	slaOutside := sla - overlap
	teamOutside := team - overlap
	block := &models.SLACoverage{
		Reference:              CoverageReferenceTeamServiceHours,
		TeamIDs:                reference.TeamIDs,
		WithinTeamServiceHours: businesstime.ReferencesWithinCalendarHours(reference.Calendars, now),
		SLAWeeklyMs:            sla.Milliseconds(),
		TeamWeeklyMs:           team.Milliseconds(),
		OverlapWeeklyMs:        overlap.Milliseconds(),
		SLAOutsideTeamWeeklyMs: slaOutside.Milliseconds(),
		TeamOutsideSLAWeeklyMs: teamOutside.Milliseconds(),
	}
	switch {
	case slaOutside <= 0 && teamOutside <= 0:
		block.Discrepancy = coverageDiscrepancyAligned
	case slaOutside > 0 && teamOutside > 0:
		block.Discrepancy = coverageDiscrepancyBoth
		block.Note = coverageNoteBoth
	case slaOutside > 0:
		block.Discrepancy = coverageDiscrepancySLAWider
		block.Note = coverageNoteSLAWider
	default:
		block.Discrepancy = coverageDiscrepancyTeamWider
		block.Note = coverageNoteTeamWider
	}
	return block
}

// coverageForCycle compiles the cycle's stored scheduling snapshot and builds
// its coverage block. A snapshot that cannot be compiled is reported unknown.
func coverageForCycle(cycle *models.ItemSLACycle, reference CoverageReference, now time.Time) *models.SLACoverage {
	scheduling, err := businesstime.CompileJSON(string(cycle.CalendarSnapshot))
	if err != nil {
		if len(reference.TeamIDs) == 0 {
			return &models.SLACoverage{Reference: CoverageReferenceNone}
		}
		return &models.SLACoverage{Reference: CoverageReferenceUnknown, TeamIDs: reference.TeamIDs}
	}
	return CycleCoverage(scheduling, reference, now)
}

// reportCoverage aggregates exact windowed coverage over completed cycles,
// using each cycle's stored snapshot so later calendar edits do not rewrite
// history. The reference teams are always current-state, labeled in the block.
func (e *Engine) reportCoverage(ctx context.Context, workspaceID int, from, to *time.Time) (*models.SLAReportCoverage, error) {
	reference, err := e.ReferenceForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if len(reference.TeamIDs) == 0 {
		return &models.SLAReportCoverage{Reference: CoverageReferenceNone, CurrentStateReference: true}, nil
	}
	if reference.Unresolved {
		return &models.SLAReportCoverage{Reference: CoverageReferenceUnknown, TeamIDs: reference.TeamIDs, CurrentStateReference: true}, nil
	}
	cycles, err := e.repo.CompletedCyclesForCoverage(ctx, workspaceID, from, to)
	if err != nil {
		return nil, err
	}
	block := &models.SLAReportCoverage{
		Reference:             CoverageReferenceTeamServiceHours,
		TeamIDs:               reference.TeamIDs,
		CurrentStateReference: true,
	}
	now := e.clock.Now()
	for i := range cycles {
		cycle := &cycles[i]
		scheduling, err := businesstime.CompileJSON(cycle.CalendarSnapshot)
		if err != nil {
			continue
		}
		start := cycle.StartedAt
		if from != nil && from.After(start) {
			start = *from
		}
		end := now
		if cycle.StoppedAt != nil {
			end = *cycle.StoppedAt
		}
		if to != nil && to.Before(end) {
			end = *to
		}
		if !end.After(start) {
			continue
		}
		slaCounted := scheduling.ElapsedCalendarTime(start, end)
		teamService := businesstime.ElapsedUnion(reference.Calendars, start, end)
		uncovered := businesstime.ElapsedExclusiveUnion(scheduling, reference.Calendars, start, end)
		block.SLACountedMs += slaCounted.Milliseconds()
		block.TeamServiceMs += teamService.Milliseconds()
		block.UncoveredMs += uncovered.Milliseconds()
		block.ExcludedMs += (teamService - (slaCounted - uncovered)).Milliseconds()
		if cycle.BreachedAt != nil && !businesstime.ReferencesWithinCalendarHours(reference.Calendars, *cycle.BreachedAt) {
			block.BreachedOutsideServiceHours++
		}
	}
	return block, nil
}

// CoveragePreview builds the structural comparison for a candidate scheduling
// calendar against the workspace's reference service hours. Callers must have
// already checked calendar access.
func (e *Engine) CoveragePreview(ctx context.Context, workspaceID int, scheduling *businesstime.Calendar) (*models.SLACoverage, error) {
	reference, err := e.ReferenceForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return CycleCoverage(scheduling, reference, e.clock.Now()), nil
}
