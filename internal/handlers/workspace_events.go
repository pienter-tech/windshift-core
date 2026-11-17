package handlers

import (
	"fmt"
	"net/http"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
)

// WorkspaceEvents streams coarse workspace-scope invalidations as Server-Sent
// Events (WI-1624). GET /workspaces/{id}/events.
//
// Gated on item.view for the workspace, returning 404 on missing permission
// (the existence-non-leak invariant). The client maps any frame to its existing
// delta fetch, so the stream never carries item-level detail.
func (h *ItemHandler) WorkspaceEvents(w http.ResponseWriter, r *http.Request) {
	if h.sseHub == nil {
		respondServiceUnavailable(w, r, "live updates are not enabled on this server")
		return
	}
	workspaceID, ok := requireIDParam(w, r, "id")
	if !ok {
		return
	}
	user, ok := RequireAuth(w, r)
	if !ok {
		return
	}
	allowed, err := h.permissionService.HasWorkspacePermission(user.ID, workspaceID, models.PermissionItemView)
	if err != nil || !allowed {
		respondNotFound(w, r, "Workspace")
		return
	}
	h.streamWorkspaceEvents(w, r, user, []int{workspaceID}, workspaceID)
}

// CollectionEvents streams workspace invalidations for a collection's scope.
// GET /collections/{key}/events. A workspace collection subscribes to its one
// workspace; a global collection subscribes to every workspace the caller can
// view, so membership changes anywhere in scope are observed.
func (h *ItemHandler) CollectionEvents(w http.ResponseWriter, r *http.Request) {
	if h.sseHub == nil {
		respondServiceUnavailable(w, r, "live updates are not enabled on this server")
		return
	}
	collectionID, ok := requireIDParam(w, r, "key")
	if !ok {
		return
	}
	user, ok := RequireAuth(w, r)
	if !ok {
		return
	}
	collection, err := repository.NewCollectionRepository(h.db).GetByID(collectionID)
	if err != nil {
		respondNotFound(w, r, "Collection")
		return
	}

	var workspaceIDs []int
	if collection.WorkspaceID != nil {
		allowed, err := h.permissionService.HasWorkspacePermission(user.ID, *collection.WorkspaceID, models.PermissionItemView)
		if err != nil || !allowed {
			respondNotFound(w, r, "Collection")
			return
		}
		workspaceIDs = []int{*collection.WorkspaceID}
	} else {
		ids, err := GetAccessibleWorkspaceIDs(user, h.db, h.permissionService)
		if err != nil {
			respondServiceUnavailable(w, r, "could not resolve workspace access")
			return
		}
		workspaceIDs = ids
	}
	h.streamWorkspaceEvents(w, r, user, workspaceIDs, collectionID)
}

func (h *ItemHandler) streamWorkspaceEvents(w http.ResponseWriter, r *http.Request, user *models.User, workspaceIDs []int, seed int) {
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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	unbindStreamDeadlines(w)

	sub := h.sseHub.SubscribeWorkspaces(workspaceIDs)
	defer h.sseHub.UnsubscribeWorkspaces(sub)

	_, _ = fmt.Fprintf(w, "retry: %d\n\n", sseRetryMillis(seed)) //nolint:gosec // G705: SSE control line, numeric only; response is text/event-stream, not HTML
	writeWorkspaceSSEEvent(w, "connected", 0)
	flusher.Flush()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()
	ctx := r.Context()

	for {
		select {
		case ev := <-sub.Events():
			// Re-authorize the affected workspace before delivering, so a
			// permission revocation stops leaking invalidations.
			allowed, err := h.permissionService.HasWorkspacePermission(user.ID, ev.WorkspaceID, models.PermissionItemView)
			if err != nil || !allowed {
				continue
			}
			writeWorkspaceSSEEvent(w, "changed", ev.WorkspaceID)
			flusher.Flush()
		case <-heartbeat.C:
			if sub.TakeStale() {
				// A frame was dropped; force the client to reconcile.
				writeWorkspaceSSEEvent(w, "changed", 0)
			}
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// writeWorkspaceSSEEvent writes one SSE frame: an `event:` line naming the
// coarse event and a `data:` line carrying the workspace id and event name.
func writeWorkspaceSSEEvent(w http.ResponseWriter, event string, workspaceID int) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: {\"workspace_id\":%d,\"kind\":%q}\n\n", event, workspaceID, event) //nolint:gosec // G705: event is a controlled constant and workspaceID an int; response is text/event-stream, not HTML
}
