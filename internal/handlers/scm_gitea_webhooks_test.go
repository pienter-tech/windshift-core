package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/middleware"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/sso"
)

type giteaWebhookFixture struct {
	db      database.Database
	handler *SCMItemLinksHandler
	user    *models.User
	repoID  int
	key     string
	secret  string
}

// newGiteaWebhookFixture links Forgejo repository 42 to a workspace whose
// admin has generated webhook details through the settings endpoint.
func newGiteaWebhookFixture(t *testing.T) *giteaWebhookFixture {
	t.Helper()
	db, err := database.NewSQLiteDBWithPoolSizes(filepath.Join(t.TempDir(), "windshift.db"), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatal(err)
	}

	insert := func(query string, args ...any) int {
		t.Helper()
		result, err := db.ExecWrite(query, args...)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return int(id)
	}
	userID := insert(`INSERT INTO users (email, username, first_name, last_name) VALUES ('admin@example.test', 'synthetic-admin', 'Synthetic', 'Admin')`)
	workspaceID := insert(`INSERT INTO workspaces (name, key) VALUES ('Forgejo', 'FGJ')`)
	insert(`INSERT INTO user_workspace_roles (user_id, workspace_id, role_id, granted_by)
		SELECT ?, ?, id, ? FROM workspace_roles WHERE name = 'Administrator'`, userID, workspaceID, userID)
	providerID := insert(`INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled) VALUES ('forgejo', 'Forgejo', 'gitea', 'pat', true)`)
	connectionID := insert(`INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id) VALUES (?, ?)`, workspaceID, providerID)
	repoID := insert(`INSERT INTO workspace_repositories (workspace_scm_connection_id, repository_external_id, repository_name, repository_url) VALUES (?, '42', 'pienter/app', 'https://git.example/pienter/app')`, connectionID)

	config := services.DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	config.PreWarmActive = false
	permissionService, err := services.NewPermissionService(db, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = permissionService.Close() })

	f := &giteaWebhookFixture{
		db:      db,
		handler: NewSCMItemLinksHandler(db, sso.NewSecretEncryption("synthetic-webhook-test-secret"), permissionService, "https://windshift.example"),
		user:    &models.User{ID: userID, Username: "synthetic-admin"},
		repoID:  repoID,
	}
	response := f.rotate(t)
	f.key = response.CallbackURL[strings.LastIndex(response.CallbackURL, "/")+1:]
	f.secret = response.Secret
	return f
}

func (f *giteaWebhookFixture) adminRequest(method string) *http.Request {
	req := httptest.NewRequest(method, "/api/workspace-repositories/"+strconv.Itoa(f.repoID)+"/webhook", nil)
	req.SetPathValue("repoId", strconv.Itoa(f.repoID))
	return req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, f.user))
}

func (f *giteaWebhookFixture) rotate(t *testing.T) scmWebhookConfigResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	f.handler.RotateWebhookSecret(recorder, f.adminRequest(http.MethodPost))
	if recorder.Code != http.StatusOK {
		t.Fatalf("rotate: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response scmWebhookConfigResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func giteaSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func giteaPullRequestBody(repositoryID int64) []byte {
	body, _ := json.Marshal(map[string]any{
		"action":       "closed",
		"number":       7,
		"pull_request": map[string]any{"number": 7, "merged": true},
		"repository":   map[string]any{"id": repositoryID, "full_name": "pienter/app"},
	})
	return body
}

// deliver posts body to the fixture's receiver with the given headers.
func (f *giteaWebhookFixture) deliver(body []byte, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/scm/webhooks/gitea/"+f.key, bytes.NewReader(body))
	req.SetPathValue("webhookKey", f.key)
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	f.handler.ReceiveGiteaWebhook(recorder, req)
	return recorder
}

func (f *giteaWebhookFixture) deliveryCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM scm_webhook_deliveries`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// waitForDelivery waits for the background work of a delivery to finish and
// returns its recorded outcome.
func (f *giteaWebhookFixture) waitForDelivery(t *testing.T, deliveryID string) (string, string, string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var eventType, status, errorMessage string
		err := f.db.QueryRow(`SELECT event_type, status, COALESCE(error_message, '') FROM scm_webhook_deliveries WHERE delivery_id = ?`, deliveryID).Scan(&eventType, &status, &errorMessage)
		if err != nil {
			t.Fatal(err)
		}
		if status != "pending" {
			return eventType, status, errorMessage
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivery %s still pending", deliveryID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGiteaWebhookSettingsShowCallbackURLAndRotateSecret(t *testing.T) {
	f := newGiteaWebhookFixture(t)
	if f.secret == "" {
		t.Fatal("rotate did not return the secret to paste into Forgejo")
	}

	recorder := httptest.NewRecorder()
	f.handler.GetWebhookConfig(recorder, f.adminRequest(http.MethodGet))
	if recorder.Code != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var config scmWebhookConfigResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	wantURL := "https://windshift.example/api/scm/webhooks/gitea/" + f.key
	if !config.Configured || config.CallbackURL != wantURL || config.Secret != "" {
		t.Fatalf("unexpected config: %#v", config)
	}
	if strings.Join(config.Events, ",") != "push,create,release,pull_request,pull_request_comment,status,action_run_success,action_run_failure,issues,issue_assign,issue_label,issue_milestone,issue_comment" {
		t.Fatalf("unexpected events: %#v", config.Events)
	}

	rotated := f.rotate(t)
	if rotated.CallbackURL != wantURL || rotated.Secret == "" || rotated.Secret == f.secret {
		t.Fatalf("rotation must keep the URL and replace the secret: %#v", rotated)
	}
	body := giteaPullRequestBody(42)
	if recorder := f.deliver(body, map[string]string{"X-Forgejo-Event": "pull_request", "X-Forgejo-Signature": giteaSignature(f.secret, body)}); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("old secret still accepted after rotation: status=%d", recorder.Code)
	}
}

func TestGiteaWebhookPullRequestSchedulesRepositorySync(t *testing.T) {
	for _, forge := range []string{"Forgejo", "Gitea"} {
		t.Run(forge, func(t *testing.T) {
			f := newGiteaWebhookFixture(t)
			body := giteaPullRequestBody(42)
			recorder := f.deliver(body, map[string]string{
				"X-" + forge + "-Event":     "pull_request",
				"X-" + forge + "-Signature": giteaSignature(f.secret, body),
				"X-" + forge + "-Delivery":  "delivery-1",
			})
			if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"accepted":true`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}

			// The fixture's connection has no credentials, so the scheduled
			// sync loads this repository and then fails to build its provider.
			eventType, status, errorMessage := f.waitForDelivery(t, "delivery-1")
			if eventType != "pull_request" || status != "failed" || !strings.Contains(errorMessage, "failed to create provider") {
				t.Fatalf("sync was not scheduled for the repository: event=%q status=%q error=%q", eventType, status, errorMessage)
			}
		})
	}
}

// giteaRefBodies are the push, create, and release deliveries the receiver
// syncs the repository for, keyed by case name, with their event and a
// fragment their delivery summary must contain.
func giteaRefBodies(repositoryID int64) map[string]struct {
	event   string
	body    []byte
	summary string
} {
	repository := map[string]any{"id": repositoryID, "full_name": "pienter/app"}
	push, _ := json.Marshal(map[string]any{
		"ref": "refs/heads/feature/FGJ-1-save", "before": "abc123", "after": "def456",
		"commits":       []map[string]any{{"id": "def456", "message": "FGJ-1 Fix save"}},
		"total_commits": 1,
		"repository":    repository,
	})
	branch, _ := json.Marshal(map[string]any{"sha": "def456", "ref": "feature/FGJ-1-save", "ref_type": "branch", "repository": repository})
	tag, _ := json.Marshal(map[string]any{"sha": "def456", "ref": "v1.2.0", "ref_type": "tag", "repository": repository})
	release, _ := json.Marshal(map[string]any{
		"action":     "published",
		"release":    map[string]any{"id": 5, "tag_name": "v1.2.0", "name": "v1.2.0"},
		"repository": repository,
	})
	return map[string]struct {
		event   string
		body    []byte
		summary string
	}{
		"push":           {"push", push, `"after":"def456"`},
		"branch created": {"create", branch, `"ref_type":"branch"`},
		"tag created":    {"create", tag, `"ref_type":"tag"`},
		"release":        {"release", release, `"tag":"v1.2.0"`},
	}
}

func TestGiteaWebhookRefDeliverySchedulesRepositorySync(t *testing.T) {
	for name, tc := range giteaRefBodies(42) {
		t.Run(name, func(t *testing.T) {
			f := newGiteaWebhookFixture(t)
			recorder := f.deliver(tc.body, map[string]string{
				"X-Forgejo-Event":     tc.event,
				"X-Forgejo-Signature": giteaSignature(f.secret, tc.body),
				"X-Forgejo-Delivery":  "ref-1",
			})
			if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"accepted":true`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			// The fixture's connection has no credentials, so the scheduled
			// sync loads this repository and then fails to build its provider.
			eventType, status, errorMessage := f.waitForDelivery(t, "ref-1")
			if eventType != tc.event || status != "failed" || !strings.Contains(errorMessage, "failed to create provider") {
				t.Fatalf("sync was not scheduled for the repository: event=%q status=%q error=%q", eventType, status, errorMessage)
			}
			var summary string
			if err := f.db.QueryRow(`SELECT payload_summary FROM scm_webhook_deliveries WHERE delivery_id = 'ref-1'`).Scan(&summary); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(summary, tc.summary) {
				t.Fatalf("summary lacks %s: %s", tc.summary, summary)
			}
		})
	}
}

func TestGiteaWebhookRefDeliveryForOtherRepositoryIsRejected(t *testing.T) {
	for name, tc := range giteaRefBodies(43) {
		t.Run(name, func(t *testing.T) {
			f := newGiteaWebhookFixture(t)
			recorder := f.deliver(tc.body, map[string]string{"X-Forgejo-Event": tc.event, "X-Forgejo-Signature": giteaSignature(f.secret, tc.body)})
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status=%d want 403 body=%s", recorder.Code, recorder.Body.String())
			}
			if count := f.deliveryCount(t); count != 0 {
				t.Fatalf("recorded %d deliveries, want none", count)
			}
		})
	}
}

// giteaCIBodies are the CI deliveries the receiver acts on: a Gitea commit
// status, which names its repository at the top level, and a Forgejo Actions
// run, which names it inside the run.
func giteaCIBodies(repositoryID int64) map[string][]byte {
	status, _ := json.Marshal(map[string]any{
		"sha": "abc123", "state": "success", "context": "ci / test",
		"target_url": "https://git.example/pienter/app/actions/runs/3",
		"repository": map[string]any{"id": repositoryID, "full_name": "pienter/app"},
	})
	actionRun, _ := json.Marshal(map[string]any{
		"action":       "failure",
		"prior_status": "running",
		"run": map[string]any{
			"commit_sha": "abc123", "status": "failure",
			"html_url":   "https://git.example/pienter/app/actions/runs/3",
			"repository": map[string]any{"id": repositoryID, "full_name": "pienter/app"},
		},
	})
	return map[string][]byte{"status": status, "action_run_failure": actionRun}
}

func TestGiteaWebhookCIDeliverySchedulesCIRefresh(t *testing.T) {
	for event, body := range giteaCIBodies(42) {
		t.Run(event, func(t *testing.T) {
			f := newGiteaWebhookFixture(t)
			recorder := f.deliver(body, map[string]string{
				"X-Forgejo-Event":     event,
				"X-Forgejo-Signature": giteaSignature(f.secret, body),
				"X-Forgejo-Delivery":  "ci-1",
			})
			if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"accepted":true`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			// The fixture's connection has no credentials, so the scheduled
			// CI refresh loads this repository and then fails to build its
			// provider.
			eventType, status, errorMessage := f.waitForDelivery(t, "ci-1")
			if eventType != event || status != "failed" || !strings.Contains(errorMessage, "failed to create provider") {
				t.Fatalf("CI refresh was not scheduled: event=%q status=%q error=%q", eventType, status, errorMessage)
			}
			var summary string
			if err := f.db.QueryRow(`SELECT payload_summary FROM scm_webhook_deliveries WHERE delivery_id = 'ci-1'`).Scan(&summary); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(summary, `"sha":"abc123"`) {
				t.Fatalf("summary does not name the commit: %s", summary)
			}
		})
	}
}

func TestGiteaWebhookCIDeliveryForOtherRepositoryIsRejected(t *testing.T) {
	for event, body := range giteaCIBodies(43) {
		t.Run(event, func(t *testing.T) {
			f := newGiteaWebhookFixture(t)
			recorder := f.deliver(body, map[string]string{"X-Forgejo-Event": event, "X-Forgejo-Signature": giteaSignature(f.secret, body)})
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status=%d want 403 body=%s", recorder.Code, recorder.Body.String())
			}
			if count := f.deliveryCount(t); count != 0 {
				t.Fatalf("recorded %d deliveries, want none", count)
			}
		})
	}
}

func TestGiteaWebhookDeduplicatesDeliveries(t *testing.T) {
	f := newGiteaWebhookFixture(t)
	body := giteaPullRequestBody(42)
	headers := map[string]string{"X-Forgejo-Event": "pull_request", "X-Forgejo-Signature": giteaSignature(f.secret, body), "X-Forgejo-Delivery": "delivery-1"}
	for range 2 {
		if recorder := f.deliver(body, headers); recorder.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	f.waitForDelivery(t, "delivery-1")
	if count := f.deliveryCount(t); count != 1 {
		t.Fatalf("redelivery recorded %d deliveries, want 1", count)
	}
}

func TestGiteaWebhookRejectsOrIgnoresWithoutScheduling(t *testing.T) {
	cases := []struct {
		name       string
		event      string
		repository int64
		signature  func(secret string, body []byte) string
		wantStatus int
	}{
		{"missing signature", "pull_request", 42, func(string, []byte) string { return "" }, http.StatusUnauthorized},
		{"wrong secret", "pull_request", 42, func(_ string, body []byte) string { return giteaSignature("not-the-secret", body) }, http.StatusUnauthorized},
		{"malformed signature", "pull_request", 42, func(string, []byte) string { return "not-hex" }, http.StatusUnauthorized},
		{"other repository", "pull_request", 43, giteaSignature, http.StatusForbidden},
		{"unhandled event", "delete", 42, giteaSignature, http.StatusAccepted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newGiteaWebhookFixture(t)
			body := giteaPullRequestBody(tc.repository)
			recorder := f.deliver(body, map[string]string{"X-Forgejo-Event": tc.event, "X-Forgejo-Signature": tc.signature(f.secret, body)})
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d want %d body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if count := f.deliveryCount(t); count != 0 {
				t.Fatalf("recorded %d deliveries, want none", count)
			}
		})
	}
}

// recordedIssueSync records the repositories whose issue sync a delivery ran.
type recordedIssueSync struct {
	mu    sync.Mutex
	repos []int
}

func (r *recordedIssueSync) SyncRepository(_ context.Context, workspaceRepositoryID int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.repos = append(r.repos, workspaceRepositoryID)
	return nil
}

func (r *recordedIssueSync) synced() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.repos...)
}

// giteaIssueBody is an issues or issue_comment delivery for issue #3, or for
// pull request #3 when isPull is set.
func giteaIssueBody(event string, isPull bool) []byte {
	issue := map[string]any{"number": 3, "title": "Crash on save", "state": "open"}
	if isPull {
		issue["pull_request"] = map[string]any{"merged": false}
	}
	payload := map[string]any{
		"action":     "edited",
		"issue":      issue,
		"repository": map[string]any{"id": 42, "full_name": "pienter/app"},
	}
	if event == "issues" {
		payload["number"] = 3
		payload["action"] = "label_updated"
	} else {
		payload["comment"] = map[string]any{"id": 77, "body": "Same here"}
		payload["is_pull"] = isPull
	}
	body, _ := json.Marshal(payload)
	return body
}

func TestGiteaWebhookIssueDeliverySchedulesIssueSync(t *testing.T) {
	for _, event := range []string{"issues", "issue_comment"} {
		t.Run(event, func(t *testing.T) {
			f := newGiteaWebhookFixture(t)
			issueSync := &recordedIssueSync{}
			f.handler.SetIssueSync(issueSync)
			body := giteaIssueBody(event, false)
			recorder := f.deliver(body, map[string]string{
				"X-Forgejo-Event":     event,
				"X-Forgejo-Signature": giteaSignature(f.secret, body),
				"X-Forgejo-Delivery":  "issue-1",
			})
			if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"accepted":true`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			eventType, status, errorMessage := f.waitForDelivery(t, "issue-1")
			if eventType != event || status != "processed" || errorMessage != "" {
				t.Fatalf("event=%q status=%q error=%q", eventType, status, errorMessage)
			}
			if got := issueSync.synced(); len(got) != 1 || got[0] != f.repoID {
				t.Fatalf("issue sync ran for %v, want repository %d", got, f.repoID)
			}
			var summary string
			if err := f.db.QueryRow(`SELECT payload_summary FROM scm_webhook_deliveries WHERE delivery_id = 'issue-1'`).Scan(&summary); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(summary, `"number":3`) {
				t.Fatalf("summary does not name the issue: %s", summary)
			}
		})
	}
}

// A pull request's comments arrive as issue_comment with the issue marked
// as a pull request; they sync the repository, whose sync reads the comments
// of linked pull requests, whether or not issue sync is wired.
func TestGiteaWebhookPullRequestCommentSchedulesRepositorySync(t *testing.T) {
	for _, wired := range []bool{true, false} {
		t.Run("issue sync wired="+strconv.FormatBool(wired), func(t *testing.T) {
			f := newGiteaWebhookFixture(t)
			issueSync := &recordedIssueSync{}
			if wired {
				f.handler.SetIssueSync(issueSync)
			}
			body := giteaIssueBody("issue_comment", true)
			recorder := f.deliver(body, map[string]string{
				"X-Forgejo-Event":     "issue_comment",
				"X-Forgejo-Signature": giteaSignature(f.secret, body),
				"X-Forgejo-Delivery":  "comment-1",
			})
			if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"accepted":true`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			// The fixture's connection has no credentials, so the scheduled
			// sync loads this repository and then fails to build its provider.
			eventType, status, errorMessage := f.waitForDelivery(t, "comment-1")
			if eventType != "issue_comment" || status != "failed" || !strings.Contains(errorMessage, "failed to create provider") {
				t.Fatalf("sync was not scheduled for the repository: event=%q status=%q error=%q", eventType, status, errorMessage)
			}
			if got := issueSync.synced(); len(got) != 0 {
				t.Fatalf("issue sync ran for %v", got)
			}
			var summary string
			if err := f.db.QueryRow(`SELECT payload_summary FROM scm_webhook_deliveries WHERE delivery_id = 'comment-1'`).Scan(&summary); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(summary, `"number":3`) || !strings.Contains(summary, `"pull_request":true`) {
				t.Fatalf("summary does not name the pull request: %s", summary)
			}
		})
	}
}

func TestGiteaWebhookIgnoresIssueDeliveryWithoutIssueSync(t *testing.T) {
	for _, event := range []string{"issues", "issue_comment"} {
		t.Run(event, func(t *testing.T) {
			f := newGiteaWebhookFixture(t)
			body := giteaIssueBody(event, false)
			recorder := f.deliver(body, map[string]string{"X-Forgejo-Event": event, "X-Forgejo-Signature": giteaSignature(f.secret, body)})
			if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"ignored":true`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if count := f.deliveryCount(t); count != 0 {
				t.Fatalf("recorded %d deliveries, want none", count)
			}
		})
	}
}

// holdRepositorySyncs replaces the fixture's repository syncs with ones the
// test releases run by run.
func (f *giteaWebhookFixture) holdRepositorySyncs(errs ...error) *heldSyncs {
	held := newHeldSyncs(errs...)
	f.handler.repositorySyncs = newRepositorySyncs(held.sync, time.Minute)
	return held
}

func (f *giteaWebhookFixture) deliverPush(t *testing.T, deliveryID string) {
	t.Helper()
	body := giteaRefBodies(42)["push"].body
	recorder := f.deliver(body, map[string]string{
		"X-Forgejo-Event":     "push",
		"X-Forgejo-Signature": giteaSignature(f.secret, body),
		"X-Forgejo-Delivery":  deliveryID,
	})
	if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"accepted":true`) {
		t.Fatalf("%s: status=%d body=%s", deliveryID, recorder.Code, recorder.Body.String())
	}
}

// assertDeliveryOutcome waits for a delivery's work and checks its outcome.
func (f *giteaWebhookFixture) assertDeliveryOutcome(t *testing.T, deliveryID, wantStatus, wantError string) {
	t.Helper()
	_, status, errorMessage := f.waitForDelivery(t, deliveryID)
	if status != wantStatus || errorMessage != wantError {
		t.Fatalf("%s: status=%q error=%q, want status=%q error=%q", deliveryID, status, errorMessage, wantStatus, wantError)
	}
}

// A burst of deliveries while the repository syncs runs one follow-up sync
// after it; each delivery records whether it ran a sync or was folded into
// the queued one.
func TestGiteaWebhookBurstCoalescesRepositorySyncs(t *testing.T) {
	f := newGiteaWebhookFixture(t)
	held := f.holdRepositorySyncs(nil, errors.New("provider unavailable"))

	f.deliverPush(t, "push-1")
	held.awaitStart(t)
	for _, deliveryID := range []string{"push-2", "push-3", "push-4"} {
		f.deliverPush(t, deliveryID)
	}
	held.assertNoStart(t)
	var status string
	if err := f.db.QueryRow(`SELECT status FROM scm_webhook_deliveries WHERE delivery_id = 'push-3'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("queued delivery recorded %q before its sync ran", status)
	}

	held.release <- struct{}{}
	f.assertDeliveryOutcome(t, "push-1", "processed", "")
	held.awaitStart(t)
	held.release <- struct{}{}
	f.assertDeliveryOutcome(t, "push-2", "failed", "provider unavailable")
	f.assertDeliveryOutcome(t, "push-3", "coalesced", "provider unavailable")
	f.assertDeliveryOutcome(t, "push-4", "coalesced", "provider unavailable")

	repo := strconv.Itoa(f.repoID)
	if got, want := strings.Join(held.runs(), ","), "start "+repo+",end "+repo+",start "+repo+",end "+repo; got != want {
		t.Fatalf("runs = %s, want %s", got, want)
	}
}

// A manual sync from the settings shares the repository's syncs with its
// webhook deliveries.
func TestManualRepositorySyncJoinsWebhookSyncs(t *testing.T) {
	f := newGiteaWebhookFixture(t)
	held := f.holdRepositorySyncs()

	f.deliverPush(t, "push-1")
	held.awaitStart(t)

	manual := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		req := f.adminRequest(http.MethodPost)
		f.handler.SyncWorkspaceRepository(recorder, req)
		manual <- recorder
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !f.handler.repositorySyncs.queuedFor(f.repoID) {
		if time.Now().After(deadline) {
			t.Fatal("manual sync did not queue a follow-up sync")
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.deliverPush(t, "push-2")

	held.release <- struct{}{}
	f.assertDeliveryOutcome(t, "push-1", "processed", "")
	held.awaitStart(t)
	held.release <- struct{}{}
	f.assertDeliveryOutcome(t, "push-2", "coalesced", "")
	select {
	case recorder := <-manual:
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"success":true`) {
			t.Fatalf("manual sync: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("manual sync did not answer after its sync ran")
	}
	if got := held.runs(); len(got) != 4 {
		t.Fatalf("runs = %v, want two syncs", got)
	}
}

// The GitLab receiver shares the same per-repository syncs.
func TestGitLabWebhookBurstCoalescesRepositorySyncs(t *testing.T) {
	f := newGiteaWebhookFixture(t)
	if _, err := f.db.ExecWrite(`UPDATE scm_providers SET provider_type = 'gitlab'`); err != nil {
		t.Fatal(err)
	}
	held := f.holdRepositorySyncs()
	deliver := func(deliveryID string) {
		t.Helper()
		body := []byte(`{"object_kind":"push","ref":"refs/heads/main","project":{"id":42,"path_with_namespace":"pienter/app"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/scm/webhooks/gitlab/"+f.key, bytes.NewReader(body))
		req.SetPathValue("webhookKey", f.key)
		req.Header.Set("X-Gitlab-Token", f.secret)
		req.Header.Set("X-Gitlab-Event-UUID", deliveryID)
		recorder := httptest.NewRecorder()
		f.handler.ReceiveGitLabWebhook(recorder, req)
		if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"accepted":true`) {
			t.Fatalf("%s: status=%d body=%s", deliveryID, recorder.Code, recorder.Body.String())
		}
	}

	deliver("gl-1")
	held.awaitStart(t)
	deliver("gl-2")
	deliver("gl-3")
	held.assertNoStart(t)
	held.release <- struct{}{}
	held.awaitStart(t)
	held.release <- struct{}{}
	f.assertDeliveryOutcome(t, "gl-1", "processed", "")
	f.assertDeliveryOutcome(t, "gl-2", "processed", "")
	f.assertDeliveryOutcome(t, "gl-3", "coalesced", "")
	if got := held.runs(); len(got) != 4 {
		t.Fatalf("runs = %v, want two syncs", got)
	}
}
