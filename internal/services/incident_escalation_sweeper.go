package services

import (
	"log/slog"
	"sync"
	"time"

	"windshift/internal/database"
	"windshift/internal/repository"
)

// IncidentEscalationSweeperConfig configures the background ticker that drives
// time-based escalation for triggered incidents.
type IncidentEscalationSweeperConfig struct {
	TickInterval time.Duration // how often to scan for due incidents; default 30s
	BatchSize    int           // max incidents to advance per pass; default 50
	// MaxDrainDuration bounds one tick's drain loop. A tick keeps processing
	// further batches while due work remains, so a burst drains within
	// TickInterval + MaxDrainDuration of its deadline instead of one batch
	// per interval; default 15s.
	MaxDrainDuration time.Duration
}

// DefaultIncidentEscalationSweeperConfig returns sensible defaults.
func DefaultIncidentEscalationSweeperConfig() IncidentEscalationSweeperConfig {
	return IncidentEscalationSweeperConfig{
		TickInterval:     30 * time.Second,
		BatchSize:        50,
		MaxDrainDuration: 15 * time.Second,
	}
}

// IncidentEscalationSweeper periodically advances incidents whose
// next_escalation_at has passed. It shares IncidentService.AdvanceDue with the
// inline step-0 path so the cursor transition is identical.
type IncidentEscalationSweeper struct {
	repo     *repository.OnCallRepository
	incident *IncidentService
	config   IncidentEscalationSweeperConfig
	stopChan chan struct{}
	wg       sync.WaitGroup

	ticksProcessed    int64
	stepsAdvanced     int64
	notificationsSent int64
	errors            int64
}

// NewIncidentEscalationSweeper constructs the sweeper. Call Start() to begin.
func NewIncidentEscalationSweeper(db database.Database, incident *IncidentService, config IncidentEscalationSweeperConfig) *IncidentEscalationSweeper {
	if config.TickInterval == 0 {
		config.TickInterval = 30 * time.Second
	}
	if config.BatchSize == 0 {
		config.BatchSize = 50
	}
	if config.MaxDrainDuration == 0 {
		config.MaxDrainDuration = 15 * time.Second
	}
	return &IncidentEscalationSweeper{
		repo:     repository.NewOnCallRepository(db),
		incident: incident,
		config:   config,
		stopChan: make(chan struct{}),
	}
}

// Start launches the background worker. Idempotent — calling Start twice is a no-op.
func (s *IncidentEscalationSweeper) Start() {
	s.wg.Add(1)
	go s.run()
	slog.Debug("incident escalation sweeper started",
		slog.String("component", "oncall"),
		slog.Duration("tick_interval", s.config.TickInterval),
		slog.Int("batch_size", s.config.BatchSize),
	)
}

// Stop signals shutdown and waits for the worker to drain.
func (s *IncidentEscalationSweeper) Stop() {
	close(s.stopChan)
	s.wg.Wait()
}

func (s *IncidentEscalationSweeper) run() {
	defer s.wg.Done()
	t := time.NewTicker(s.config.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stopChan:
			return
		case <-t.C:
			s.tick()
		}
	}
}

// tick runs one sweep drain. It processes batches of due escalations and
// due scheduled notifications alternately, continuing while either queue
// still returns a full batch so a burst drains within one tick instead of
// accruing a per-interval backlog. Alternation keeps neither queue starved
// when both are saturated. The loop stops at the drain budget, on shutdown,
// or when both queues return a partial batch. Per-row failures are logged
// and skipped.
func (s *IncidentEscalationSweeper) tick() {
	s.ticksProcessed++
	budgetEnd := time.Now().Add(s.config.MaxDrainDuration)
	for {
		more := s.drainPass()
		if !more {
			return
		}
		select {
		case <-s.stopChan:
			return
		default:
		}
		if time.Now().After(budgetEnd) {
			return
		}
	}
}

// drainPass processes one incident batch and one notification batch. It
// reports whether either batch was full, meaning more work likely remains.
func (s *IncidentEscalationSweeper) drainPass() bool {
	now := time.Now()
	fullIncidents := false

	dueIDs, err := s.repo.FindDueIncidentIDs(now, s.config.BatchSize)
	if err != nil {
		s.errors++
		slog.Warn("incident sweeper: failed to query due incidents",
			slog.String("component", "oncall"), slog.Any("error", err))
	} else {
		fullIncidents = len(dueIDs) == s.config.BatchSize
		for _, id := range dueIDs {
			if err := s.incident.AdvanceDue(id); err != nil {
				s.errors++
				slog.Warn("incident sweeper: escalation failed",
					slog.String("component", "oncall"),
					slog.Int("incident_id", id),
					slog.Any("error", err),
				)
				continue
			}
			s.stepsAdvanced++
		}
	}

	stateIDs, err := s.repo.FindDueNotificationStateIDs(now, s.config.BatchSize)
	if err != nil {
		s.errors++
		slog.Warn("incident sweeper: failed to query scheduled notifications",
			slog.String("component", "oncall"), slog.Any("error", err))
		return fullIncidents
	}
	for _, id := range stateIDs {
		if err := s.incident.DispatchDueNotification(id); err != nil {
			s.errors++
			slog.Warn("incident sweeper: scheduled notification failed",
				slog.String("component", "oncall"),
				slog.Int("notification_state_id", id),
				slog.Any("error", err),
			)
			continue
		}
		s.notificationsSent++
	}
	return fullIncidents || len(stateIDs) == s.config.BatchSize
}
