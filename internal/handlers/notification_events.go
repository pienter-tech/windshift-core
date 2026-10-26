package handlers

import (
	"fmt"
	"net/http"
	"time"
)

// NotificationEvents streams per-user notification invalidations as Server-Sent
// Events (WI-1625). GET /notifications/events.
//
// The topic is the authenticated caller's own user id, so no extra permission
// check is needed beyond authentication. The client maps any frame to its
// existing inbox reconciliation.
func (nh *NotificationHandler) NotificationEvents(w http.ResponseWriter, r *http.Request) {
	if nh.sseHub == nil {
		respondServiceUnavailable(w, r, "live updates are not enabled on this server")
		return
	}
	user, ok := RequireAuth(w, r)
	if !ok {
		return
	}
	release, ok := nh.sseHub.AcquireUserStream(user.ID)
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

	sub := nh.sseHub.SubscribeUser(user.ID)
	defer nh.sseHub.UnsubscribeUser(sub)

	_, _ = fmt.Fprintf(w, "retry: %d\n\n", sseRetryMillis(user.ID)) //nolint:gosec // G705: SSE control line, numeric only; response is text/event-stream, not HTML
	writeUserSSEEvent(w, "connected")
	flusher.Flush()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-sub.Events():
			writeUserSSEEvent(w, "changed")
			flusher.Flush()
		case <-heartbeat.C:
			if sub.TakeStale() {
				// A frame was dropped; force the client to reconcile.
				writeUserSSEEvent(w, "changed")
			}
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// writeUserSSEEvent writes one SSE frame naming the coarse event.
func writeUserSSEEvent(w http.ResponseWriter, event string) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: {}\n\n", event) //nolint:gosec // G705: event is a controlled constant; response is text/event-stream, not HTML
}
