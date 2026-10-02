package sla

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"windshift/internal/models"
)

// Defaults for the due-work loop.
const (
	DefaultSafetyInterval = 15 * time.Minute
	DefaultLease          = 2 * time.Minute
	DefaultBatchSize      = 50
	DefaultMaxAttempts    = 8
	maxBackoff            = 5 * time.Minute
	baseBackoff           = 30 * time.Second
)

// Clock is the loop's time source. Tests inject a controllable clock.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production clock.
type SystemClock struct{}

// Now returns the current wall-clock time.
func (SystemClock) Now() time.Time { return time.Now() }

// Timer abstracts time.Timer so tests can fire the loop's waits deterministically.
type Timer interface {
	Chan() <-chan time.Time
	Stop()
}

// TimerFactory creates a Timer for a duration.
type TimerFactory func(time.Duration) Timer

type systemTimer struct{ timer *time.Timer }

func (t systemTimer) Chan() <-chan time.Time { return t.timer.C }
func (t systemTimer) Stop()                  { t.timer.Stop() }

// SystemTimerFactory returns timers backed by time.Timer.
func SystemTimerFactory(d time.Duration) Timer { return systemTimer{timer: time.NewTimer(d)} }

// JobStore is the durable persistence the loop needs.
type JobStore interface {
	NextDueAt(ctx context.Context) (time.Time, bool, error)
	ClaimDueJobs(ctx context.Context, now time.Time, lease time.Duration, owner string, limit int) ([]models.SLAJob, error)
	// RenewJobsLease extends the lease of the given jobs and returns the ids
	// this owner still holds; the rest were reclaimed or re-armed and must
	// not run.
	RenewJobsLease(ctx context.Context, jobIDs []int64, owner string, until time.Time) ([]int64, error)
	RescheduleJob(ctx context.Context, jobID int64, dueAt time.Time, lastError string) error
	FailJob(ctx context.Context, jobID int64, lastError string) error
	// RescheduleOwnedJob and FailOwnedJob are the lease-fenced error paths
	// for claimed jobs.
	RescheduleOwnedJob(ctx context.Context, jobID int64, owner, lastError string, dueAt time.Time) error
	FailOwnedJob(ctx context.Context, jobID int64, owner string, lastError string) error
	HasConfiguration(ctx context.Context) (bool, error)
}

// JobRunner executes a single claimed job in its own transaction. A successful
// run must delete or re-arm the job; the loop only reschedules on error.
type JobRunner interface {
	RunJob(ctx context.Context, job models.SLAJob) error
}

// LoopConfig configures the due-work loop.
type LoopConfig struct {
	SafetyInterval time.Duration
	Lease          time.Duration
	BatchSize      int
	MaxAttempts    int
	Owner          string
}

// WithDefaults fills unset fields.
func (c LoopConfig) WithDefaults() LoopConfig {
	if c.SafetyInterval <= 0 {
		c.SafetyInterval = DefaultSafetyInterval
	}
	if c.Lease <= 0 {
		c.Lease = DefaultLease
	}
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultBatchSize
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = DefaultMaxAttempts
	}
	if c.Owner == "" {
		c.Owner = "sla-loop"
	}
	return c
}

// Loop is the single process-wide goroutine that fires deadline and
// recalculation jobs. It is data-driven: it blocks until the earliest due_at,
// a nudge, or the safety interval.
type Loop struct {
	store  JobStore
	runner JobRunner
	clock  Clock
	timers TimerFactory
	config LoopConfig

	nudge chan time.Time

	mu          sync.Mutex
	active      bool
	checkConfig bool

	// idleCh is a test seam: it is signaled after the loop goes idle with no
	// configuration. Production leaves it nil.
	idleCh chan struct{}
}

// NewLoop constructs a due-work loop.
func NewLoop(store JobStore, runner JobRunner, clock Clock, timers TimerFactory, config LoopConfig) *Loop {
	if clock == nil {
		clock = SystemClock{}
	}
	if timers == nil {
		timers = SystemTimerFactory
	}
	return &Loop{
		store:  store,
		runner: runner,
		clock:  clock,
		timers: timers,
		config: config.WithDefaults(),
		nudge:  make(chan time.Time, 1),
		// A restart must read the table immediately in case jobs survived a crash.
		active:      true,
		checkConfig: true,
	}
}

// Nudge reports that work may have been armed at dueAt. It never blocks.
func (l *Loop) Nudge(dueAt time.Time) {
	l.mu.Lock()
	l.active = true
	l.checkConfig = true
	l.mu.Unlock()
	select {
	case l.nudge <- dueAt:
	default:
	}
}

// Owner returns the lease owner string this loop claims jobs under, so the
// engine can fence claimed-job retirement to its own claims.
func (l *Loop) Owner() string { return l.config.Owner }

func (l *Loop) isActive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}

func (l *Loop) setActive(active bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active = active
}

func (l *Loop) takeConfigCheck() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	check := l.checkConfig
	l.checkConfig = false
	return check
}

// Run drives the loop until ctx is canceled.
func (l *Loop) Run(ctx context.Context) {
	slog.Debug("SLA due-work loop started",
		slog.String("component", "sla"),
		slog.Duration("safety_interval", l.config.SafetyInterval),
		slog.String("owner", l.config.Owner))
	for {
		if !l.isActive() {
			select {
			case <-ctx.Done():
				return
			case <-l.nudge:
				l.setActive(true)
			}
			continue
		}

		next, ok, err := l.store.NextDueAt(ctx)
		if err != nil {
			slog.Warn("SLA loop failed to read due jobs", slog.String("component", "sla"), slog.Any("error", err))
			if !l.wait(ctx, l.config.SafetyInterval) {
				return
			}
			continue
		}
		if ok {
			wait := next.Sub(l.clock.Now())
			if wait <= 0 {
				wait = 0
			} else if wait > l.config.SafetyInterval {
				// Never sleep past the safety interval: a replica crash or a
				// host suspend must not defer a deadline indefinitely.
				wait = l.config.SafetyInterval
			}
			if !l.wait(ctx, wait) {
				return
			}
			if err := l.RunOnce(ctx); err != nil {
				slog.Warn("SLA loop job batch failed", slog.String("component", "sla"), slog.Any("error", err))
			}
			continue
		}

		// Re-check configuration only at startup and after a nudge, so the
		// steady-state idle cost stays at one MIN read per safety interval.
		if l.takeConfigCheck() {
			configured, err := l.store.HasConfiguration(ctx)
			if err != nil {
				slog.Warn("SLA loop failed to check configuration", slog.String("component", "sla"), slog.Any("error", err))
				if !l.wait(ctx, l.config.SafetyInterval) {
					return
				}
				continue
			}
			if !configured {
				// No SLA anywhere: block on the nudge channel and issue no queries.
				l.setActive(false)
				if l.idleCh != nil {
					select {
					case l.idleCh <- struct{}{}:
					default:
					}
				}
				continue
			}
		}
		if !l.wait(ctx, l.config.SafetyInterval) {
			return
		}
	}
}

// RunOnce claims and runs one batch of due jobs. Every claimed job's lease
// is renewed before it runs: a batch that waits behind slow siblings would
// otherwise become reclaimable mid-batch, letting another instance run the
// same job while this one still holds a stale claim.
func (l *Loop) RunOnce(ctx context.Context) error {
	now := l.clock.Now()
	jobs, err := l.store.ClaimDueJobs(ctx, now, l.config.Lease, l.config.Owner, l.config.BatchSize)
	if err != nil {
		return fmt.Errorf("claim due jobs: %w", err)
	}
	owned := make(map[int64]bool, len(jobs))
	for _, job := range jobs {
		owned[job.ID] = true
	}
	for _, job := range jobs {
		if !owned[job.ID] {
			continue
		}
		// Renew everything this instance still expects to run, then drop the
		// jobs whose lease was lost to another claim or an inline re-arm.
		pending := make([]int64, 0, len(owned))
		for _, candidate := range jobs {
			if owned[candidate.ID] {
				pending = append(pending, candidate.ID)
			}
		}
		renewed, err := l.store.RenewJobsLease(ctx, pending, l.config.Owner, l.clock.Now().Add(l.config.Lease))
		if err != nil {
			return fmt.Errorf("renew job leases: %w", err)
		}
		stillOwned := make(map[int64]bool, len(renewed))
		for _, id := range renewed {
			stillOwned[id] = true
		}
		owned = stillOwned
		if !owned[job.ID] {
			continue
		}
		if err := l.runner.RunJob(ctx, job); err != nil {
			l.handleJobError(ctx, job, err)
		} else {
			delete(owned, job.ID)
		}
	}
	return nil
}

func (l *Loop) handleJobError(ctx context.Context, job models.SLAJob, runErr error) {
	message := runErr.Error()
	if job.Attempts >= l.config.MaxAttempts {
		if err := l.store.FailOwnedJob(ctx, job.ID, l.config.Owner, message); err != nil {
			slog.Error("SLA loop failed to park job", slog.String("component", "sla"), slog.Int64("job_id", job.ID), slog.Any("error", err))
		}
		return
	}
	if err := l.store.RescheduleOwnedJob(ctx, job.ID, l.config.Owner, message, l.clock.Now().Add(Backoff(job.Attempts))); err != nil {
		slog.Error("SLA loop failed to reschedule job", slog.String("component", "sla"), slog.Int64("job_id", job.ID), slog.Any("error", err))
	}
}

// Backoff returns the retry delay for a job that has already been attempted
// attempts times, capped at five minutes.
func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := baseBackoff
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= maxBackoff {
			return maxBackoff
		}
	}
	if delay > maxBackoff {
		return maxBackoff
	}
	return delay
}

// wait blocks for d, returning false when ctx is canceled. A nudge wakes it
// early so an earlier deadline or a newly configured workspace is noticed
// without waiting out the timer.
func (l *Loop) wait(ctx context.Context, d time.Duration) bool {
	if d < 0 {
		d = 0
	}
	timer := l.timers(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.Chan():
		return true
	case <-l.nudge:
		return true
	}
}
