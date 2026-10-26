package handlers

import (
	"fmt"
	"net/http"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
)

const sseHeartbeatInterval = 20 * time.Second

// SetSSEHub wires the in-memory hub that backs the item event stream. When nil
// (live updates disabled), the Events endpoint returns 503.
func (h *ItemHandler) SetSSEHub(hub *services.SSEHub) { h.sseHub = hub }

// Events streams coarse item-change events for one item as Server-Sent Events
// (WI-484). GET /items/{id}/events.
//
// The wire is deliberately coarse: `changed` means "reload the whole detail"
// and `deleted` means "the item is gone". The client does not decode a change
// taxonomy, so any mutation that publishes reaches every visible section and a
// dropped frame is harmless.
//
// Gated on item.view, returning 404 on a missing item or missing permission
// (the item-existence-non-leak invariant). The handler holds NO database
// connection while idle: after the connect-time permission check it blocks on
// the subscriber channel and a heartbeat ticker only.
func (h *ItemHandler) Events(w http.ResponseWriter, r *http.Request) {
	if h.sseHub == nil {
		respondServiceUnavailable(w, r, "live updates are not enabled on this server")
		return
	}
	itemID, ok := requireIDParam(w, r, "id")
	if !ok {
		return
	}
	user, ok := RequireAuth(w, r)
	if !ok {
		return
	}
	itemRepo := repository.NewItemRepository(h.db)
	workspaceID, err := itemRepo.GetWorkspaceID(itemID)
	if err != nil {
		respondNotFound(w, r, "Item")
		return
	}
	allowed, err := h.permissionService.HasWorkspacePermission(user.ID, workspaceID, models.PermissionItemView)
	if err != nil || !allowed {
		respondNotFound(w, r, "Item")
		return
	}

	// Recheck ownership against a workspace before writes. The workspace is
	// re-read on the event path (a move is a change), so heartbeats reuse the
	// cached id instead of querying the item row every tick.
	authorized := func(workspaceID int) bool {
		ok, err := h.permissionService.HasWorkspacePermission(user.ID, workspaceID, models.PermissionItemView)
		return err == nil && ok
	}

	release, ok := h.sseHub.AcquireUserStream(user.ID)
	if !ok {
		respondTooManyRequests(w, r, "Too many live update streams are already open for this account")
		return
	}
	defer release()

	flusher, ok := w.(http.Flusher)
	if !ok {
		respondServiceUnavailable(w, r, "streaming is unsupported on this connection")
		return
	}

	// SSE response headers. X-Accel-Buffering disables response buffering for
	// nginx-class proxies; the gzip wrapper is told to skip text/event-stream
	// (middleware/compression.go) so small frames are flushed immediately.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// The server's 30s WriteTimeout is a hard wall-clock deadline that would
	// sever this long-lived response; lift it for the stream.
	unbindStreamDeadlines(w)

	sub := h.sseHub.Subscribe(itemID)
	defer h.sseHub.Unsubscribe(sub)

	// Initial frame: a jittered reconnect hint (thundering-herd guard on a mass
	// disconnect) and a `connected` event so the client runs its first full
	// reconcile.
	_, _ = fmt.Fprintf(w, "retry: %d\n\n", sseRetryMillis(itemID)) //nolint:gosec // G705: SSE control line, numeric only; response is text/event-stream, not HTML
	writeSSEEvent(w, "connected", itemID)
	flusher.Flush()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()
	ctx := r.Context()

	for {
		select {
		case ev := <-sub.Events():
			if ev.Deleted {
				// Deletion carries the final workspace; the item row is already gone.
				if !authorized(ev.WorkspaceID) {
					return
				}
				writeSSEEvent(w, "deleted", ev.ItemID)
				flusher.Flush()
				return
			}
			// Re-resolve the workspace: the change may be a move, which can move
			// the item beyond this user's reach.
			current, err := itemRepo.GetWorkspaceID(itemID)
			if err != nil || !authorized(current) {
				return
			}
			workspaceID = current
			writeSSEEvent(w, "changed", itemID)
			flusher.Flush()
		case <-heartbeat.C:
			if !authorized(workspaceID) {
				return
			}
			if sub.TakeStale() {
				// A frame was dropped (buffer overflow) with no later change to
				// cover it; force the client to reconcile.
				writeSSEEvent(w, "changed", itemID)
			}
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// writeSSEEvent writes one SSE frame: an `event:` line naming the coarse event
// and a `data:` line carrying the item id and event name as JSON.
func writeSSEEvent(w http.ResponseWriter, event string, itemID int) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: {\"item_id\":%d,\"kind\":%q}\n\n", event, itemID, event) //nolint:gosec // G705: event is a controlled constant and itemID an int; response is text/event-stream, not HTML
}

// sseRetryMillis returns a reconnect delay in 3000–6999ms, spread by item id so
// a deploy/restart doesn't make every client reconnect at the same instant.
func sseRetryMillis(itemID int) int {
	return 3000 + (itemID*1327)%4000
}
