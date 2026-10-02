package sla

import (
	"sync"
	"time"
)

// TestClock is a manually controlled clock for end-to-end tests. The engine and
// the due-work loop read Now; the server's test hook sets or advances it. It is
// only ever constructed when WINDSHIFT_E2E_TEST_HOOKS is enabled.
type TestClock struct {
	mu  sync.RWMutex
	now time.Time
}

// NewTestClock starts a test clock at the given instant.
func NewTestClock(start time.Time) *TestClock {
	return &TestClock{now: start}
}

// Now returns the current controlled instant.
func (c *TestClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

// Set replaces the controlled instant.
func (c *TestClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// Advance moves the controlled instant forward.
func (c *TestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
