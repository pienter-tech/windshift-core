package handlers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

// heldSyncs is a repository sync that blocks every run until the test
// releases it, and logs when runs start and end.
type heldSyncs struct {
	started chan int
	release chan struct{}

	mu    sync.Mutex
	log   []string
	calls int
	errs  []error // returned by the calls in order; nil past the end
}

func newHeldSyncs(errs ...error) *heldSyncs {
	return &heldSyncs{started: make(chan int, 16), release: make(chan struct{}), errs: errs}
}

func (h *heldSyncs) sync(ctx context.Context, repoID int) error {
	h.mu.Lock()
	call := h.calls
	h.calls++
	h.log = append(h.log, fmt.Sprintf("start %d", repoID))
	h.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("sync run without a timeout")
	}
	h.started <- repoID
	<-h.release

	h.mu.Lock()
	defer h.mu.Unlock()
	h.log = append(h.log, fmt.Sprintf("end %d", repoID))
	if call < len(h.errs) {
		return h.errs[call]
	}
	return nil
}

// awaitStart returns the repository of the next run to start.
func (h *heldSyncs) awaitStart(t *testing.T) int {
	t.Helper()
	select {
	case repoID := <-h.started:
		return repoID
	case <-time.After(5 * time.Second):
		t.Fatal("no sync started")
		return 0
	}
}

// assertNoStart fails when a run starts within a short wait.
func (h *heldSyncs) assertNoStart(t *testing.T) {
	t.Helper()
	select {
	case repoID := <-h.started:
		t.Fatalf("sync of repository %d started while another one ran", repoID)
	case <-time.After(50 * time.Millisecond):
	}
}

func (h *heldSyncs) runs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.log)
}

// queuedFor reports whether a follow-up sync of repoID is queued.
func (s *repositorySyncs) queuedFor(repoID int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.repos[repoID]
	return ok && state.queued != nil
}

func waitRun(t *testing.T, run *repositorySyncRun) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := run.wait(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("sync run did not end")
	}
	return err
}

func TestRepositorySyncsBurstRunsOneFollowUpAfterTheRunningSync(t *testing.T) {
	held := newHeldSyncs(nil, errors.New("provider unavailable"))
	syncs := newRepositorySyncs(held.sync, time.Minute)

	first, shared := syncs.schedule(1)
	if shared {
		t.Fatal("the first request joined a queued sync")
	}
	held.awaitStart(t)

	var followUp *repositorySyncRun
	for i := range 5 {
		run, shared := syncs.schedule(1)
		if run == first {
			t.Fatalf("request %d joined the running sync, which may have read the repository before it", i)
		}
		if i == 0 {
			followUp = run
		}
		if run != followUp || shared != (i > 0) {
			t.Fatalf("request %d: shared=%v, same follow-up=%v", i, shared, run == followUp)
		}
	}
	held.assertNoStart(t)

	held.release <- struct{}{}
	if err := waitRun(t, first); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	held.awaitStart(t)
	held.release <- struct{}{}
	if err := waitRun(t, followUp); err == nil || err.Error() != "provider unavailable" {
		t.Fatalf("follow-up sync error = %v", err)
	}

	if got, want := held.runs(), []string{"start 1", "end 1", "start 1", "end 1"}; !slices.Equal(got, want) {
		t.Fatalf("runs = %v, want %v", got, want)
	}

	// Once idle, the repository's next request starts a sync again.
	next, shared := syncs.schedule(1)
	if shared || next == followUp {
		t.Fatal("request after the burst did not start a new sync")
	}
	held.awaitStart(t)
	held.release <- struct{}{}
	if err := waitRun(t, next); err != nil {
		t.Fatalf("next sync: %v", err)
	}
}

func TestRepositorySyncsOfDifferentRepositoriesRunIndependently(t *testing.T) {
	held := newHeldSyncs()
	syncs := newRepositorySyncs(held.sync, time.Minute)

	runA, sharedA := syncs.schedule(1)
	runB, sharedB := syncs.schedule(2)
	if sharedA || sharedB || runA == runB {
		t.Fatal("syncs of two repositories were folded together")
	}
	started := []int{held.awaitStart(t), held.awaitStart(t)}
	slices.Sort(started)
	if !slices.Equal(started, []int{1, 2}) {
		t.Fatalf("started syncs of %v, want both repositories at once", started)
	}

	held.release <- struct{}{}
	held.release <- struct{}{}
	if err := waitRun(t, runA); err != nil {
		t.Fatal(err)
	}
	if err := waitRun(t, runB); err != nil {
		t.Fatal(err)
	}
	if syncs.queuedFor(1) || syncs.queuedFor(2) {
		t.Fatal("a follow-up sync was queued without a request")
	}
}

func TestRepositorySyncRunOutlivesAnAbandonedWait(t *testing.T) {
	held := newHeldSyncs()
	syncs := newRepositorySyncs(held.sync, time.Minute)

	run, _ := syncs.schedule(1)
	held.awaitStart(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run.wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("abandoned wait returned %v", err)
	}

	held.release <- struct{}{}
	if err := waitRun(t, run); err != nil {
		t.Fatalf("sync did not finish after its waiter left: %v", err)
	}
}
