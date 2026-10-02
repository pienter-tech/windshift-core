package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/sla"
)

// ErrSLAThresholdInvalid marks warning-threshold validation failures.
var ErrSLAThresholdInvalid = errors.New("invalid SLA warning threshold")

// SLAWarningThresholdInput is the transport-neutral threshold payload.
type SLAWarningThresholdInput struct {
	Label    string
	Percent  int
	MetricID *int
	IsActive bool
}

// SLASettingsService owns SLA warning-threshold configuration. Thresholds are
// not part of Jira goals; edits bump the configuration generation and enqueue
// recalculation so ongoing cycles re-arm their warning jobs.
type SLASettingsService struct {
	db     database.Database
	repo   *repository.SLARepository
	engine *sla.Engine
}

// NewSLASettingsService constructs the settings service.
func NewSLASettingsService(db database.Database, engine *sla.Engine) *SLASettingsService {
	return &SLASettingsService{db: db, repo: repository.NewSLARepository(db), engine: engine}
}

// ListWarningThresholds returns a workspace's thresholds.
func (s *SLASettingsService) ListWarningThresholds(ctx context.Context, workspaceID int) ([]models.SLAWarningThreshold, error) {
	return s.repo.ListWarningThresholds(ctx, workspaceID)
}

// CreateWarningThreshold validates and inserts a threshold.
func (s *SLASettingsService) CreateWarningThreshold(ctx context.Context, workspaceID int, input SLAWarningThresholdInput) (*models.SLAWarningThreshold, error) {
	threshold, err := s.buildThreshold(ctx, workspaceID, input)
	if err != nil {
		return nil, err
	}
	threshold.WorkspaceID = workspaceID
	err = database.WithTx(s.db, func(tx database.Tx) error {
		id, err := s.repo.CreateWarningThreshold(ctx, tx, threshold)
		if err != nil {
			return err
		}
		threshold.ID = id
		return s.touchAndRecalculate(ctx, tx, workspaceID, threshold.MetricID)
	})
	if err != nil {
		return nil, err
	}
	return threshold, nil
}

// UpdateWarningThreshold validates and rewrites a threshold.
func (s *SLASettingsService) UpdateWarningThreshold(ctx context.Context, workspaceID, id int, input SLAWarningThresholdInput) (*models.SLAWarningThreshold, error) {
	existing, err := s.repo.GetWarningThreshold(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing.WorkspaceID != workspaceID {
		return nil, repository.ErrNotFound
	}
	threshold, err := s.buildThreshold(ctx, workspaceID, input)
	if err != nil {
		return nil, err
	}
	threshold.ID = id
	threshold.WorkspaceID = workspaceID
	err = database.WithTx(s.db, func(tx database.Tx) error {
		if err := s.repo.UpdateWarningThreshold(ctx, tx, threshold); err != nil {
			return err
		}
		return s.touchAndRecalculate(ctx, tx, workspaceID, threshold.MetricID)
	})
	if err != nil {
		return nil, err
	}
	return threshold, nil
}

// DeleteWarningThreshold removes a threshold and recalculates affected cycles.
func (s *SLASettingsService) DeleteWarningThreshold(ctx context.Context, workspaceID, id int) error {
	existing, err := s.repo.GetWarningThreshold(ctx, id)
	if err != nil {
		return err
	}
	if existing.WorkspaceID != workspaceID {
		return repository.ErrNotFound
	}
	return database.WithTx(s.db, func(tx database.Tx) error {
		if err := s.repo.DeleteWarningThreshold(ctx, tx, id); err != nil {
			return err
		}
		return s.touchAndRecalculate(ctx, tx, workspaceID, existing.MetricID)
	})
}

func (s *SLASettingsService) buildThreshold(ctx context.Context, workspaceID int, input SLAWarningThresholdInput) (*models.SLAWarningThreshold, error) {
	if input.Percent <= 0 || input.Percent >= 100 {
		return nil, fmt.Errorf("%w: percent must be between 1 and 99", ErrSLAThresholdInvalid)
	}
	if input.MetricID != nil {
		metric, err := s.repo.GetMetric(ctx, *input.MetricID)
		if err != nil {
			return nil, err
		}
		if metric.WorkspaceID != workspaceID {
			return nil, fmt.Errorf("%w: metric does not belong to this workspace", ErrSLAThresholdInvalid)
		}
	}
	return &models.SLAWarningThreshold{
		WorkspaceID: workspaceID,
		MetricID:    input.MetricID,
		Label:       strings.TrimSpace(input.Label),
		Percent:     input.Percent,
		IsActive:    input.IsActive,
	}, nil
}

func (s *SLASettingsService) touchAndRecalculate(ctx context.Context, tx database.Tx, workspaceID int, metricID *int) error {
	if _, err := s.engine.BumpConfigGeneration(ctx, tx, workspaceID); err != nil {
		return err
	}
	metricIDs := []int{}
	if metricID != nil {
		metricIDs = append(metricIDs, *metricID)
	} else {
		metrics, err := s.repo.ListMetrics(ctx, workspaceID)
		if err != nil {
			return err
		}
		for _, metric := range metrics {
			metricIDs = append(metricIDs, metric.ID)
		}
	}
	now := time.Now().UTC()
	for _, id := range metricIDs {
		metric := id
		if err := s.repo.UpsertJob(ctx, tx, &models.SLAJob{Kind: models.SLAJobRecalcMetric, MetricID: &metric, DueAt: now}); err != nil {
			return err
		}
	}
	s.engine.Wake()
	return nil
}
