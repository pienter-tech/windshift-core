package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
)

// manualWebhookProvider describes a provider whose repository webhook an admin
// pastes in by hand: the callback path segment and the events to select.
type manualWebhookProvider struct {
	name   string
	path   string
	events []string
}

var manualWebhookProviders = map[string]manualWebhookProvider{
	string(models.SCMProviderTypeGitLab): {name: "GitLab", path: "gitlab", events: gitLabWebhookEvents},
	string(models.SCMProviderTypeGitea):  {name: "Gitea/Forgejo", path: "gitea", events: giteaWebhookEvents},
}

type scmWebhookConfigResponse struct {
	Configured     bool       `json:"configured"`
	CallbackURL    string     `json:"callback_url,omitempty"`
	Secret         string     `json:"secret,omitempty"`
	Events         []string   `json:"events"`
	Active         bool       `json:"active"`
	LastDeliveryAt *time.Time `json:"last_delivery_at,omitempty"`
}

func (h *SCMItemLinksHandler) webhookRepoAdmin(w http.ResponseWriter, r *http.Request, repoID int) (string, bool) {
	user, ok := RequireAuth(w, r)
	if !ok {
		return "", false
	}
	access, err := h.webhookRepo.GetRepositoryAccess(r.Context(), repoID)
	if errors.Is(err, repository.ErrNotFound) {
		respondNotFound(w, r, "workspace_repository")
		return "", false
	}
	if err != nil {
		respondInternalError(w, r, err)
		return "", false
	}
	if !RequireWorkspacePermission(w, r, user.ID, access.WorkspaceID, models.PermissionWorkspaceAdmin, h.permissionService) {
		return "", false
	}
	return access.ProviderType, true
}

// webhookRepoProvider authorizes a workspace admin and returns the manual
// webhook setup for the repository's provider.
func (h *SCMItemLinksHandler) webhookRepoProvider(w http.ResponseWriter, r *http.Request, repoID int) (manualWebhookProvider, bool) {
	providerType, ok := h.webhookRepoAdmin(w, r, repoID)
	if !ok {
		return manualWebhookProvider{}, false
	}
	provider, ok := manualWebhookProviders[providerType]
	if !ok {
		respondBadRequest(w, r, "Webhooks are only available for GitLab and Gitea/Forgejo repositories")
		return manualWebhookProvider{}, false
	}
	return provider, true
}

func (h *SCMItemLinksHandler) webhookCallbackURL(r *http.Request, provider manualWebhookProvider, key string) string {
	base := h.baseURL
	if base == "" {
		scheme := "https"
		if r.TLS == nil {
			if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
				scheme = forwarded
			} else {
				scheme = "http"
			}
		}
		host := r.Host
		if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
			host = forwarded
		}
		base = scheme + "://" + host
	}
	return strings.TrimRight(base, "/") + "/api/scm/webhooks/" + provider.path + "/" + key
}

// GetWebhookConfig returns manual setup information without exposing the secret.
func (h *SCMItemLinksHandler) GetWebhookConfig(w http.ResponseWriter, r *http.Request) {
	repoID, ok := requireIDParam(w, r, "repoId")
	if !ok {
		return
	}
	provider, ok := h.webhookRepoProvider(w, r, repoID)
	if !ok {
		return
	}
	config, err := h.webhookRepo.GetConfig(r.Context(), repoID)
	if errors.Is(err, repository.ErrNotFound) {
		respondJSONOK(w, scmWebhookConfigResponse{Configured: false, Events: provider.events})
		return
	}
	if err != nil {
		respondInternalError(w, r, err)
		return
	}
	response := scmWebhookConfigResponse{
		Configured:     true,
		CallbackURL:    h.webhookCallbackURL(r, provider, config.WebhookKey),
		Events:         provider.events,
		Active:         config.Active,
		LastDeliveryAt: config.LastDeliveryAt,
	}
	respondJSONOK(w, response)
}

func randomWebhookValue(bytesCount int) (string, error) {
	raw := make([]byte, bytesCount)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// RotateWebhookSecret creates or rotates the one-time manual setup secret.
func (h *SCMItemLinksHandler) RotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	repoID, ok := requireIDParam(w, r, "repoId")
	if !ok {
		return
	}
	provider, ok := h.webhookRepoProvider(w, r, repoID)
	if !ok {
		return
	}
	secret, err := randomWebhookValue(32)
	if err != nil {
		respondInternalError(w, r, err)
		return
	}
	encrypted, err := h.encryption.Encrypt(secret)
	if err != nil {
		respondInternalError(w, r, err)
		return
	}
	newKey, err := randomWebhookValue(18)
	if err != nil {
		respondInternalError(w, r, err)
		return
	}
	events, err := json.Marshal(provider.events)
	if err != nil {
		respondInternalError(w, r, err)
		return
	}
	key, err := h.webhookRepo.RotateConfig(r.Context(), repoID, newKey, encrypted, string(events))
	if err != nil {
		respondInternalError(w, r, err)
		return
	}
	respondJSONOK(w, scmWebhookConfigResponse{Configured: true, CallbackURL: h.webhookCallbackURL(r, provider, key), Secret: secret, Events: provider.events, Active: true})
}

func (h *SCMItemLinksHandler) DeleteWebhookConfig(w http.ResponseWriter, r *http.Request) {
	repoID, ok := requireIDParam(w, r, "repoId")
	if !ok {
		return
	}
	if _, ok := h.webhookRepoAdmin(w, r, repoID); !ok {
		return
	}
	if err := h.webhookRepo.DeleteConfig(r.Context(), repoID); err != nil {
		respondInternalError(w, r, err)
		return
	}
	respondJSONOK(w, map[string]bool{"deleted": true})
}

// webhookTarget loads the webhook behind the request's key and checks that it
// belongs to a repository of the expected provider. It returns the decrypted
// secret the delivery must prove it knows.
func (h *SCMItemLinksHandler) webhookTarget(w http.ResponseWriter, r *http.Request, providerType models.SCMProviderType) (repository.SCMWebhookTarget, string, bool) {
	target, err := h.webhookRepo.GetTargetByKey(r.Context(), r.PathValue("webhookKey"))
	if errors.Is(err, repository.ErrNotFound) {
		respondNotFound(w, r, "scm_webhook")
		return repository.SCMWebhookTarget{}, "", false
	}
	if err != nil {
		respondInternalError(w, r, err)
		return repository.SCMWebhookTarget{}, "", false
	}
	if target.ProviderType != string(providerType) {
		respondBadRequest(w, r, "Invalid webhook provider")
		return repository.SCMWebhookTarget{}, "", false
	}
	secret, err := h.encryption.Decrypt(target.EncryptedSecret)
	if err != nil {
		respondInternalError(w, r, err)
		return repository.SCMWebhookTarget{}, "", false
	}
	return target, secret, true
}

// acceptWebhookDelivery records a verified delivery, acknowledges it, and runs
// process in the background unless the delivery ID was already seen.
func (h *SCMItemLinksHandler) acceptWebhookDelivery(
	w http.ResponseWriter,
	r *http.Request,
	target repository.SCMWebhookTarget,
	deliveryID, eventType string,
	summary map[string]any,
	process func(context.Context) error,
) {
	summaryJSON, _ := json.Marshal(summary)
	inserted, err := h.webhookRepo.RecordPendingDelivery(r.Context(), target.ID, deliveryID, eventType, string(summaryJSON))
	if err != nil {
		respondInternalError(w, r, err)
		return
	}
	respondJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
	if !inserted {
		return
	}

	providerName := manualWebhookProviders[target.ProviderType].name
	baseCtx := context.WithoutCancel(r.Context())
	go func() {
		started := time.Now()
		ctx, cancel := context.WithTimeout(baseCtx, 45*time.Second)
		defer cancel()
		processErr := process(ctx)
		status, errorMessage := "processed", ""
		if processErr != nil {
			status, errorMessage = "failed", processErr.Error()
			slog.Warn(providerName+" webhook sync failed", slog.Int("repository_id", target.WorkspaceRepositoryID), slog.Any("error", processErr))
		}
		updateErr := h.webhookRepo.CompleteDelivery(ctx, target.ID, deliveryID, status, errorMessage, time.Since(started))
		if updateErr != nil {
			slog.Warn(providerName+" webhook delivery update failed", slog.Any("error", updateErr), slog.String("delivery_id", deliveryID))
		}
	}()
}
