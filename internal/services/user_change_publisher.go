package services

import "sync"

// UserChangePublisher receives coarse per-user invalidation events after a
// mutation has committed. The in-memory SSE hub implements it.
type UserChangePublisher interface {
	// PublishUserChange announces that the user's own data may have changed.
	// It must be cheap and non-blocking.
	PublishUserChange(userID int)
}

type noopUserChangePublisher struct{}

func (noopUserChangePublisher) PublishUserChange(int) {}

var (
	userChangePubMu sync.RWMutex
	userChangePub   UserChangePublisher = noopUserChangePublisher{}
)

// SetUserChangePublisher installs the process-wide user-change publisher.
// Passing nil restores the no-op default.
func SetUserChangePublisher(p UserChangePublisher) {
	userChangePubMu.Lock()
	defer userChangePubMu.Unlock()
	if p == nil {
		p = noopUserChangePublisher{}
	}
	userChangePub = p
}

// PublishUserChange routes a per-user invalidation to the installed publisher.
// Call only after the underlying mutation has committed. userID <= 0 is ignored.
func PublishUserChange(userID int) {
	if userID <= 0 {
		return
	}
	userChangePubMu.RLock()
	p := userChangePub
	userChangePubMu.RUnlock()
	p.PublishUserChange(userID)
}
