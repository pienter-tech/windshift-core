package services

import "sync"

// WorkspaceChangePublisher receives coarse workspace-scope invalidation events
// after a mutation has committed. The in-memory SSE hub implements it; the
// process default is a no-op.
//
// Implementations must be safe for concurrent use: PublishWorkspaceChange is
// called from request goroutines, schedulers and background workers.
type WorkspaceChangePublisher interface {
	// PublishWorkspaceChange announces that items in the workspace may have
	// changed. It must be cheap and non-blocking.
	PublishWorkspaceChange(workspaceID int)
}

type noopWorkspaceChangePublisher struct{}

func (noopWorkspaceChangePublisher) PublishWorkspaceChange(int) {}

var (
	workspaceChangePubMu sync.RWMutex
	workspaceChangePub   WorkspaceChangePublisher = noopWorkspaceChangePublisher{}
)

// SetWorkspaceChangePublisher installs the process-wide workspace-change
// publisher. It is called once during server startup and may be swapped by
// tests. Passing nil restores the no-op default.
func SetWorkspaceChangePublisher(p WorkspaceChangePublisher) {
	workspaceChangePubMu.Lock()
	defer workspaceChangePubMu.Unlock()
	if p == nil {
		p = noopWorkspaceChangePublisher{}
	}
	workspaceChangePub = p
}

// PublishWorkspaceChange routes a workspace invalidation to the installed
// publisher.
//
// IMPORTANT: call this only AFTER the underlying database mutation has
// committed. workspaceID <= 0 is ignored.
func PublishWorkspaceChange(workspaceID int) {
	if workspaceID <= 0 {
		return
	}
	workspaceChangePubMu.RLock()
	p := workspaceChangePub
	workspaceChangePubMu.RUnlock()
	p.PublishWorkspaceChange(workspaceID)
}
