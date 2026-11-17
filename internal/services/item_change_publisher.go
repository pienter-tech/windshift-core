package services

import "sync"

// ItemChangePublisher receives item-change notifications after a mutation has
// committed. The notification is deliberately coarse: subscribers reload the
// affected item, so the publisher carries no change taxonomy.
//
// Implementations must be safe for concurrent use: PublishItemChange is called
// from request goroutines, schedulers, and background workers.
type ItemChangePublisher interface {
	// PublishItemChange announces that the item identified by itemID changed. It
	// must be cheap and non-blocking; a hub implementation fans out to in-memory
	// subscribers without touching the database.
	PublishItemChange(itemID int)
}

type noopItemChangePublisher struct{}

func (noopItemChangePublisher) PublishItemChange(int) {}

var (
	itemChangePubMu sync.RWMutex
	itemChangePub   ItemChangePublisher = noopItemChangePublisher{}
)

// SetItemChangePublisher installs the process-wide item-change publisher. It is
// called once during server startup and may be swapped by tests. Passing nil
// restores the no-op default.
func SetItemChangePublisher(p ItemChangePublisher) {
	itemChangePubMu.Lock()
	defer itemChangePubMu.Unlock()
	if p == nil {
		p = noopItemChangePublisher{}
	}
	itemChangePub = p
}

// PublishItemChange routes an item-change notification to the installed
// publisher. It is the single entry point every mutation chokepoint calls, so
// coverage is auditable from one symbol.
//
// IMPORTANT: call this only AFTER the underlying database mutation has
// committed — never inside an open transaction that might still roll back. For
// destructive writes (delete), capture the item id (and any parent id) BEFORE
// the write and publish afterwards.
//
// itemID <= 0 is ignored, so callers can pass an optional parent id
// unconditionally.
func PublishItemChange(itemID int) {
	if itemID <= 0 {
		return
	}
	itemChangePubMu.RLock()
	p := itemChangePub
	itemChangePubMu.RUnlock()
	p.PublishItemChange(itemID)
}

// PublishItemDeletion preserves the deleted item's workspace for stream authorization.
func PublishItemDeletion(itemID, workspaceID int) {
	if itemID <= 0 {
		return
	}
	itemChangePubMu.RLock()
	p := itemChangePub
	itemChangePubMu.RUnlock()
	if publisher, ok := p.(interface{ PublishItemDeletion(int, int) }); ok {
		publisher.PublishItemDeletion(itemID, workspaceID)
		return
	}
	p.PublishItemChange(itemID)
}
