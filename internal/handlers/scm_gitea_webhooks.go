package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"windshift/internal/models"
)

// giteaWebhookEvents are the Gitea/Forgejo events to select on the repository
// webhook; giteaWebhookAction must handle each of them.
var giteaWebhookEvents = []string{"pull_request"}

type giteaWebhookPayload struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		Merged bool `json:"merged"`
	} `json:"pull_request"`
	Repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// giteaWebhookHeader reads a delivery header under its Forgejo name, falling
// back to the Gitea name both forges send.
func giteaWebhookHeader(r *http.Request, name string) string {
	if value := strings.TrimSpace(r.Header.Get("X-Forgejo-" + name)); value != "" {
		return value
	}
	return strings.TrimSpace(r.Header.Get("X-Gitea-" + name))
}

// validGiteaSignature reports whether signature is the hex HMAC-SHA256 of
// body keyed with secret, as Gitea and Forgejo sign deliveries.
func validGiteaSignature(secret string, body []byte, signature string) bool {
	provided, err := hex.DecodeString(signature)
	if err != nil || len(provided) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), provided)
}

// giteaWebhookAction returns the work a delivery of eventType schedules for
// the repository, or nil when Windshift ignores the event.
func (h *SCMItemLinksHandler) giteaWebhookAction(eventType string, repoID int) func(context.Context) error {
	switch eventType {
	case "pull_request":
		// The sync emits scm_pr_linked and scm_pr_merged, so the delivery
		// itself emits nothing and a missed delivery is caught by polling.
		return func(ctx context.Context) error {
			return h.syncService.SyncRepository(ctx, repoID)
		}
	default:
		return nil
	}
}

// ReceiveGiteaWebhook validates and deduplicates a Gitea or Forgejo delivery,
// then schedules the work for its event. Polling remains the recovery path.
func (h *SCMItemLinksHandler) ReceiveGiteaWebhook(w http.ResponseWriter, r *http.Request) {
	target, secret, ok := h.webhookTarget(w, r, models.SCMProviderTypeGitea)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondBadRequest(w, r, "Webhook payload is too large or unreadable")
		return
	}
	if !validGiteaSignature(secret, body, giteaWebhookHeader(r, "Signature")) {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid webhook signature"})
		return
	}

	eventType := strings.ToLower(giteaWebhookHeader(r, "Event"))
	action := h.giteaWebhookAction(eventType, target.WorkspaceRepositoryID)
	if action == nil {
		respondJSON(w, http.StatusAccepted, map[string]bool{"ignored": true})
		return
	}
	var payload giteaWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		respondBadRequest(w, r, "Invalid Gitea webhook JSON")
		return
	}
	if strconv.FormatInt(payload.Repository.ID, 10) != target.RepositoryExternalID {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "repository mismatch"})
		return
	}
	deliveryID := giteaWebhookHeader(r, "Delivery")
	if deliveryID == "" {
		digest := sha256.Sum256(body)
		deliveryID = hex.EncodeToString(digest[:])
	}
	summary := map[string]any{"event": eventType, "repository_id": payload.Repository.ID, "path": payload.Repository.FullName, "number": payload.Number, "action": payload.Action, "merged": payload.PullRequest.Merged}
	h.acceptWebhookDelivery(w, r, target, deliveryID, eventType, summary, action)
}
