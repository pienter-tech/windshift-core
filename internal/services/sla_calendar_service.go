package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"windshift/internal/businesstime"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/sla"
)

// ErrSLACalendarInvalid marks calendar validation failures that the transport
// should surface as a client error rather than an internal fault.
var ErrSLACalendarInvalid = errors.New("invalid SLA calendar")

// SLACalendarInput is the transport-neutral calendar payload. WeeklyIntervals
// and Holidays are bare column JSON (weekday map and holiday array); omitted
// intervals default to 24/7.
type SLACalendarInput struct {
	Name            string
	Description     string
	Timezone        string
	WeeklyIntervals json.RawMessage
	Holidays        json.RawMessage
	IsDefault       bool
	ApplyToOngoing  bool
}

// SLACalendarService owns workspace- and team-owned working calendars. It
// validates the compiled schedule so every transport persists usable
// calendars, and it never seeds a default: a workspace must create a calendar
// explicitly before its metrics can reference one.
type SLACalendarService struct {
	db     database.Database
	repo   *repository.SLARepository
	engine *sla.Engine
}

// NewSLACalendarService constructs the calendar service.
func NewSLACalendarService(db database.Database, engine *sla.Engine) *SLACalendarService {
	return &SLACalendarService{db: db, repo: repository.NewSLARepository(db), engine: engine}
}

// ListWorkspace returns a workspace's owned calendars.
func (s *SLACalendarService) ListWorkspace(ctx context.Context, workspaceID int) ([]models.WorkingCalendar, error) {
	return s.repo.ListWorkspaceCalendars(ctx, workspaceID)
}

// ListAvailable returns workspace calendars plus team calendars authorized by
// an active team-workspace binding.
func (s *SLACalendarService) ListAvailable(ctx context.Context, workspaceID int) ([]models.WorkingCalendar, error) {
	owned, err := s.repo.ListWorkspaceCalendars(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	shared, err := s.repo.ListBindableTeamCalendars(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return append(owned, shared...), nil
}

// ListTeam returns a team's shared calendars.
func (s *SLACalendarService) ListTeam(ctx context.Context, teamID int) ([]models.WorkingCalendar, error) {
	return s.repo.ListTeamCalendars(ctx, teamID)
}

// CreateWorkspace creates a workspace-owned calendar.
func (s *SLACalendarService) CreateWorkspace(ctx context.Context, workspaceID int, input SLACalendarInput) (*models.WorkingCalendar, error) {
	calendar, err := buildSLACalendar(input)
	if err != nil {
		return nil, err
	}
	calendar.WorkspaceID = &workspaceID
	id, err := s.repo.CreateCalendar(ctx, calendar)
	if err != nil {
		return nil, err
	}
	calendar.ID = id
	s.engine.InvalidateWorkspace(workspaceID)
	return calendar, nil
}

// UpdateWorkspace replaces a workspace-owned calendar.
func (s *SLACalendarService) UpdateWorkspace(ctx context.Context, workspaceID, calendarID int, input SLACalendarInput) (*models.WorkingCalendar, error) {
	existing, err := s.repo.GetCalendar(ctx, calendarID)
	if err != nil {
		return nil, err
	}
	if existing.WorkspaceID == nil || *existing.WorkspaceID != workspaceID {
		return nil, repository.ErrNotFound
	}
	calendar, err := buildSLACalendar(input)
	if err != nil {
		return nil, err
	}
	calendar.ID = calendarID
	calendar.WorkspaceID = &workspaceID
	if err := s.updateCalendar(ctx, calendar, input.ApplyToOngoing); err != nil {
		return nil, err
	}
	return calendar, nil
}

// DeleteWorkspace deletes a workspace-owned calendar, refusing while a goal
// target still references it.
func (s *SLACalendarService) DeleteWorkspace(ctx context.Context, workspaceID, calendarID int) error {
	existing, err := s.repo.GetCalendar(ctx, calendarID)
	if err != nil {
		return err
	}
	if existing.WorkspaceID == nil || *existing.WorkspaceID != workspaceID {
		return repository.ErrNotFound
	}
	if err := s.repo.DeleteCalendar(ctx, calendarID); err != nil {
		return err
	}
	s.engine.InvalidateWorkspace(workspaceID)
	return nil
}

// CreateTeam creates a team-shared calendar.
func (s *SLACalendarService) CreateTeam(ctx context.Context, teamID int, input SLACalendarInput) (*models.WorkingCalendar, error) {
	calendar, err := buildSLACalendar(input)
	if err != nil {
		return nil, err
	}
	calendar.TeamID = &teamID
	id, err := s.repo.CreateCalendar(ctx, calendar)
	if err != nil {
		return nil, err
	}
	calendar.ID = id
	return calendar, nil
}

// UpdateTeam replaces a team-shared calendar.
func (s *SLACalendarService) UpdateTeam(ctx context.Context, teamID, calendarID int, input SLACalendarInput) (*models.WorkingCalendar, error) {
	existing, err := s.repo.GetCalendar(ctx, calendarID)
	if err != nil {
		return nil, err
	}
	if existing.TeamID == nil || *existing.TeamID != teamID {
		return nil, repository.ErrNotFound
	}
	calendar, err := buildSLACalendar(input)
	if err != nil {
		return nil, err
	}
	calendar.ID = calendarID
	calendar.TeamID = &teamID
	if err := s.updateCalendar(ctx, calendar, input.ApplyToOngoing); err != nil {
		return nil, err
	}
	return calendar, nil
}

// recalculateCalendar enqueues recalculation for every metric that targets the
// calendar, across all bound workspaces.
func (s *SLACalendarService) updateCalendar(ctx context.Context, calendar *models.WorkingCalendar, applyToOngoing bool) error {
	queued := false
	err := database.WithTx(s.db, func(tx database.Tx) error {
		if err := s.repo.UpdateCalendarTx(ctx, tx, calendar); err != nil {
			return err
		}
		workspaceIDs, err := s.repo.WorkspaceIDsForCalendarTx(ctx, tx, calendar.ID)
		if err != nil {
			return err
		}
		for _, workspaceID := range workspaceIDs {
			if s.engine != nil {
				if _, err := s.engine.BumpConfigGeneration(ctx, tx, workspaceID); err != nil {
					return err
				}
			}
		}
		if !applyToOngoing {
			return nil
		}
		metricIDs, err := s.repo.MetricIDsReferencingCalendarTx(ctx, tx, calendar.ID)
		if err != nil {
			return err
		}
		for _, id := range metricIDs {
			metricID := id
			if err := s.repo.UpsertJob(ctx, tx, &models.SLAJob{
				Kind: models.SLAJobRecalcCalendar, ThresholdKey: fmt.Sprintf("%d", calendar.ID), MetricID: &metricID, DueAt: time.Now().UTC(),
			}); err != nil {
				return err
			}
			queued = true
		}
		return nil
	})
	if err == nil && queued && s.engine != nil {
		s.engine.Wake()
	}
	return err
}

// DeleteTeam deletes a team-shared calendar, refusing while a goal target
// still references it.
func (s *SLACalendarService) DeleteTeam(ctx context.Context, teamID, calendarID int) error {
	existing, err := s.repo.GetCalendar(ctx, calendarID)
	if err != nil {
		return err
	}
	if existing.TeamID == nil || *existing.TeamID != teamID {
		return repository.ErrNotFound
	}
	return s.repo.DeleteCalendar(ctx, calendarID)
}

// CalendarImpact previews what a calendar edit would recalculate across every
// workspace that can use it. Read-only.
func (s *SLACalendarService) CalendarImpact(ctx context.Context, calendarID int) (*models.SLACalendarImpact, error) {
	if _, err := s.repo.GetCalendar(ctx, calendarID); err != nil {
		return nil, err
	}
	workspaces, err := s.repo.CalendarImpact(ctx, calendarID)
	if err != nil {
		return nil, err
	}
	return &models.SLACalendarImpact{CalendarID: calendarID, Workspaces: workspaces}, nil
}

// TeamCalendarImpact previews the effect of editing a team calendar, scoped to
// the team that owns it.
func (s *SLACalendarService) TeamCalendarImpact(ctx context.Context, teamID, calendarID int) (*models.SLACalendarImpact, error) {
	calendar, err := s.repo.GetCalendar(ctx, calendarID)
	if err != nil {
		return nil, err
	}
	if calendar.TeamID == nil || *calendar.TeamID != teamID {
		return nil, repository.ErrNotFound
	}
	return s.CalendarImpact(ctx, calendarID)
}

// PreviewCoverage compares a workspace-visible calendar against the workspace's
// bound team service hours. Informational only: it never blocks a save.
func (s *SLACalendarService) PreviewCoverage(ctx context.Context, workspaceID, calendarID int) (*models.SLACoverage, error) {
	if s.engine == nil {
		return nil, repository.ErrNotFound
	}
	calendar, err := s.repo.GetCalendar(ctx, calendarID)
	if err != nil {
		return nil, err
	}
	accessible, err := s.repo.CalendarAccessibleToWorkspace(ctx, workspaceID, calendarID)
	if err != nil {
		return nil, err
	}
	if !accessible {
		return nil, repository.ErrNotFound
	}
	raw, err := businesstime.FromStored(calendar.Timezone, calendar.WeeklyIntervals, calendar.Holidays)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSLACalendarInvalid, err)
	}
	compiled, err := businesstime.Compile(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSLACalendarInvalid, err)
	}
	return s.engine.CoveragePreview(ctx, workspaceID, compiled)
}

// WorkspaceCalendarImpact previews the effect of editing a calendar visible to
// one workspace. A team calendar's impact is filtered to that workspace so one
// workspace admin never sees another workspace's metrics.
func (s *SLACalendarService) WorkspaceCalendarImpact(ctx context.Context, workspaceID, calendarID int) (*models.SLACalendarImpact, error) {
	impact, err := s.CalendarImpact(ctx, calendarID)
	if err != nil {
		return nil, err
	}
	for _, workspace := range impact.Workspaces {
		if workspace.WorkspaceID == workspaceID {
			return &models.SLACalendarImpact{CalendarID: calendarID, Workspaces: []models.SLACalendarImpactWorkspace{workspace}}, nil
		}
	}
	return nil, repository.ErrNotFound
}

func buildSLACalendar(input SLACalendarInput) (*models.WorkingCalendar, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrSLACalendarInvalid)
	}
	timezone := strings.TrimSpace(input.Timezone)
	if timezone == "" {
		timezone = "UTC"
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return nil, fmt.Errorf("%w: timezone must be a valid IANA zone", ErrSLACalendarInvalid)
	}
	weekly := input.WeeklyIntervals
	if len(weekly) == 0 {
		encoded, _ := json.Marshal(businesstime.AlwaysOpenRaw(timezone).WeeklyIntervals)
		weekly = encoded
	}
	holidays := input.Holidays
	if len(holidays) == 0 {
		holidays = json.RawMessage("[]")
	}
	raw, err := businesstime.FromStored(timezone, weekly, holidays)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSLACalendarInvalid, err)
	}
	if _, err := businesstime.Compile(raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSLACalendarInvalid, err)
	}
	return &models.WorkingCalendar{
		Name:            name,
		Description:     input.Description,
		Timezone:        timezone,
		WeeklyIntervals: weekly,
		Holidays:        holidays,
		IsDefault:       input.IsDefault,
	}, nil
}
