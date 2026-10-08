package handlers

import (
	"context"
	"sync"
	"time"
)

// repositorySyncTimeout bounds one repository sync started by a webhook
// delivery or from the settings.
const repositorySyncTimeout = 45 * time.Second

// repositorySyncs runs at most one sync per repository at a time. A request
// while a repository's sync runs queues one follow-up sync, which starts when
// the running one ends; requests while the follow-up is queued share it. A
// burst of requests therefore costs at most two syncs, and the last one reads
// the repository after every request in the burst arrived.
type repositorySyncs struct {
	sync    func(ctx context.Context, repoID int) error
	timeout time.Duration

	mu    sync.Mutex
	repos map[int]*repositorySyncState
}

// repositorySyncState is a repository whose sync runs, with its queued
// follow-up, if any.
type repositorySyncState struct {
	queued *repositorySyncRun
}

// repositorySyncRun is one sync of a repository that requests wait on.
type repositorySyncRun struct {
	done chan struct{}
	err  error
}

func newRepositorySyncs(sync func(ctx context.Context, repoID int) error, timeout time.Duration) *repositorySyncs {
	return &repositorySyncs{sync: sync, timeout: timeout, repos: make(map[int]*repositorySyncState)}
}

// schedule requests a sync of repoID and returns the run that serves the
// request. It starts the run when the repository is idle, and otherwise
// queues the follow-up run. shared reports whether the request joined a
// follow-up an earlier request had already queued.
func (s *repositorySyncs) schedule(repoID int) (run *repositorySyncRun, shared bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, running := s.repos[repoID]
	if !running {
		run = &repositorySyncRun{done: make(chan struct{})}
		s.repos[repoID] = &repositorySyncState{}
		go s.runFrom(repoID, run)
		return run, false
	}
	if state.queued != nil {
		return state.queued, true
	}
	state.queued = &repositorySyncRun{done: make(chan struct{})}
	return state.queued, false
}

// runFrom runs run and then each follow-up queued meanwhile, until the
// repository is idle. Each run gets a fresh context, independent of the
// requests waiting on it.
func (s *repositorySyncs) runFrom(repoID int, run *repositorySyncRun) {
	for run != nil {
		ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
		run.err = s.sync(ctx, repoID)
		cancel()
		close(run.done)

		s.mu.Lock()
		state := s.repos[repoID]
		run, state.queued = state.queued, nil
		if run == nil {
			delete(s.repos, repoID)
		}
		s.mu.Unlock()
	}
}

// wait returns the run's error once it has ended, or ctx's error when ctx
// ends first. The run carries on either way.
func (r *repositorySyncRun) wait(ctx context.Context) error {
	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
