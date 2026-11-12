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
// webhook; giteaWebhookAction must handle each of them. Forgejo has no commit
// status event and reports Forgejo Actions results as action_run_* events;
// Gitea sends commit statuses as the status event. The issue_assign,
// issue_label, and issue_milestone events are delivered as issues, and
// pull_request_comment as issue_comment with the issue marked as a pull
// request.
var giteaWebhookEvents = []string{
	"push", "create", "release", "pull_request", "pull_request_comment",
	"status", "action_run_success", "action_run_failure",
	"issues", "issue_assign", "issue_label", "issue_milestone", "issue_comment",
}

type giteaWebhookRepository struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
}

type giteaWebhookPayload struct {
	Action string `json:"action"`
	Number int    `json:"number"`
	// Ref is set on push and create deliveries; Before and After on push,
	// RefType ("branch" or "tag") on create.
	Ref     string `json:"ref"`
	Before  string `json:"before"`
	After   string `json:"after"`
	RefType string `json:"ref_type"`
	Release struct {
		TagName string `json:"tag_name"`
	} `json:"release"`
	PullRequest struct {
		Merged bool `json:"merged"`
	} `json:"pull_request"`
	// Issue and IsPull are set on issues and issue_comment deliveries; a pull
	// request's comments also arrive as issue_comment.
	Issue struct {
		Number      int       `json:"number"`
		PullRequest *struct{} `json:"pull_request"`
	} `json:"issue"`
	IsPull     bool                   `json:"is_pull"`
	Repository giteaWebhookRepository `json:"repository"`
	// SHA and State are set on a Gitea commit status delivery; SHA also on
	// a create delivery, as the commit the new ref points at.
	SHA   string `json:"sha"`
	State string `json:"state"`
	// Run is set on Forgejo action_run_* and workflow_run_*/workflow_job_*
	// deliveries, which carry their repository inside the run.
	Run struct {
		CommitSHA  string                 `json:"commit_sha"`
		Status     string                 `json:"status"`
		Repository giteaWebhookRepository `json:"repository"`
	} `json:"run"`
	// WorkflowRun and WorkflowJob are set on Gitea workflow_run and
	// workflow_job deliveries.
	WorkflowRun struct {
		HeadSHA string `json:"head_sha"`
	} `json:"workflow_run"`
	WorkflowJob struct {
		HeadSHA string `json:"head_sha"`
	} `json:"workflow_job"`
}

// repository returns the repository a delivery of eventType is about. Only
// Forgejo Actions run deliveries name it inside the run.
func (p giteaWebhookPayload) repository(eventType string) giteaWebhookRepository {
	if p.Repository.ID == 0 && isGiteaCIEvent(eventType) {
		return p.Run.Repository
	}
	return p.Repository
}

// commitSHA returns the commit a CI delivery reports on, or "" when the
// payload names none.
func (p giteaWebhookPayload) commitSHA() string {
	for _, sha := range []string{p.SHA, p.Run.CommitSHA, p.WorkflowRun.HeadSHA, p.WorkflowJob.HeadSHA} {
		if sha != "" {
			return sha
		}
	}
	return ""
}

// isGiteaCIEvent reports whether eventType reports CI progress: a Gitea
// commit status, or a Forgejo Actions run or job change.
func isGiteaCIEvent(eventType string) bool {
	return eventType == "status" ||
		strings.HasPrefix(eventType, "action_run_") ||
		strings.HasPrefix(eventType, "workflow_run") ||
		strings.HasPrefix(eventType, "workflow_job")
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

// isGiteaIssueEvent reports whether eventType reports a change to an issue
// or its comments.
func isGiteaIssueEvent(eventType string) bool {
	return eventType == "issues" || eventType == "issue_comment"
}

// isPullRequest reports whether an issue delivery is about a pull request.
func (p giteaWebhookPayload) isPullRequest() bool {
	return p.IsPull || p.Issue.PullRequest != nil
}

// issueNumber returns the issue an issue delivery is about.
func (p giteaWebhookPayload) issueNumber() int {
	if p.Issue.Number != 0 {
		return p.Issue.Number
	}
	return p.Number
}

// isGiteaRepositorySyncEvent reports whether eventType changes what the
// repository sync reads: pushed commits, created branches and tags,
// releases, and pull requests.
func isGiteaRepositorySyncEvent(eventType string) bool {
	switch eventType {
	case "push", "create", "release", "pull_request":
		return true
	default:
		return false
	}
}

// giteaWebhookHandled reports whether Windshift acts on eventType.
func giteaWebhookHandled(eventType string) bool {
	return isGiteaRepositorySyncEvent(eventType) || isGiteaCIEvent(eventType) || isGiteaIssueEvent(eventType)
}

// giteaWebhookAction returns the work a delivery of eventType schedules for
// the repository, or nil when Windshift ignores the event. Only repository
// syncs are shared between deliveries: a CI refresh reads the status of one
// commit, and runs of the issue sync of a config wait for each other.
func (h *SCMItemLinksHandler) giteaWebhookAction(eventType string, repoID int, payload giteaWebhookPayload) *webhookWork {
	switch {
	case isGiteaRepositorySyncEvent(eventType):
		// The sync links branches, commits, and pull requests and emits
		// their events, including the tag and release ones, so the delivery
		// itself emits nothing and a missed delivery is caught by polling.
		return repositorySyncWork
	case isGiteaCIEvent(eventType):
		// CI status is display-only: the refresh re-reads the combined
		// status of the pull requests whose head is the reported commit and
		// emits no events. Polling still catches a missed delivery.
		sha := payload.commitSHA()
		return &webhookWork{process: func(ctx context.Context) error {
			return h.syncService.RefreshPullRequestCIForCommit(ctx, repoID, sha)
		}}
	case isGiteaIssueEvent(eventType):
		// Pull request comments are not issue sync's: the repository sync
		// reads the comments of open linked pull requests.
		if payload.isPullRequest() {
			return repositorySyncWork
		}
		if h.issueSync == nil {
			return nil
		}
		// The issue sync of the repository's config picks the change up
		// with every other one since its last run. Polling still catches a
		// missed delivery.
		return &webhookWork{process: func(ctx context.Context) error {
			return h.issueSync.SyncRepository(ctx, repoID)
		}}
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
	if !giteaWebhookHandled(eventType) {
		respondJSON(w, http.StatusAccepted, map[string]bool{"ignored": true})
		return
	}
	var payload giteaWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		respondBadRequest(w, r, "Invalid Gitea webhook JSON")
		return
	}
	repository := payload.repository(eventType)
	if strconv.FormatInt(repository.ID, 10) != target.RepositoryExternalID {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "repository mismatch"})
		return
	}
	action := h.giteaWebhookAction(eventType, target.WorkspaceRepositoryID, payload)
	if action == nil {
		respondJSON(w, http.StatusAccepted, map[string]bool{"ignored": true})
		return
	}
	deliveryID := giteaWebhookHeader(r, "Delivery")
	if deliveryID == "" {
		digest := sha256.Sum256(body)
		deliveryID = hex.EncodeToString(digest[:])
	}
	summary := map[string]any{"event": eventType, "repository_id": repository.ID, "path": repository.FullName, "action": payload.Action}
	switch {
	case eventType == "push":
		summary["ref"] = payload.Ref
		summary["before"] = payload.Before
		summary["after"] = payload.After
	case eventType == "create":
		summary["ref"] = payload.Ref
		summary["ref_type"] = payload.RefType
		summary["sha"] = payload.SHA
	case eventType == "release":
		summary["tag"] = payload.Release.TagName
	case isGiteaCIEvent(eventType):
		summary["sha"] = payload.commitSHA()
		summary["state"] = payload.State
		if payload.State == "" {
			summary["state"] = payload.Run.Status
		}
	case isGiteaIssueEvent(eventType):
		summary["number"] = payload.issueNumber()
		summary["pull_request"] = payload.isPullRequest()
	default:
		summary["number"] = payload.Number
		summary["merged"] = payload.PullRequest.Merged
	}
	h.acceptWebhookDelivery(w, r, target, deliveryID, eventType, summary, action)
}
