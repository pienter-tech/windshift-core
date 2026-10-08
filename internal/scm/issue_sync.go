package scm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"windshift/internal/database"
	"windshift/internal/itemevents"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/sso"
)

// IssueSyncService handles synchronization of GitHub and Gitea/Forgejo
// issues into Windshift items.
type IssueSyncService struct {
	db          database.Database
	encryption  *sso.SecretEncryption
	syncMu      sync.Mutex
	userService interface {
		GetByID(int) (*models.User, error)
	}
	// resolveProviderOverride lets tests substitute the provider of a
	// connection; nil in production.
	resolveProviderOverride func(ctx context.Context, connectionID int) (Provider, error)
}

// issueSyncConfigLocks serializes syncs of one config across the scheduler,
// manual triggers, and webhook deliveries: each config ID maps to a
// one-slot channel held for the duration of a sync.
var issueSyncConfigLocks sync.Map

// lockIssueSyncConfig waits until no other sync of configID runs, or ctx
// ends, and returns the release function.
func lockIssueSyncConfig(ctx context.Context, configID int) (func(), error) {
	slot, _ := issueSyncConfigLocks.LoadOrStore(configID, make(chan struct{}, 1))
	lock, ok := slot.(chan struct{})
	if !ok {
		// Programmer error — issueSyncConfigLocks is populated only by this
		// function and only ever stores chan struct{}.
		panic(fmt.Sprintf("issueSyncConfigLocks: unexpected value type %T", slot))
	}
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for running issue sync: %w", ctx.Err())
	}
}

// pushStatusLockWait bounds how long a status push waits for a running
// inbound sync of its config before pushing without the lock. It is shorter
// than the push's own 30s budget, so the remote write still has time left.
// A variable so tests can shorten it.
var pushStatusLockWait = 20 * time.Second

// issueSyncJob is one enabled config with the repository it syncs.
type issueSyncJob struct {
	config       models.IssueSyncConfig
	repoName     string
	connectionID int
}

// issueForgeName names the forge of a provider in synced comment headers.
func issueForgeName(providerType models.SCMProviderType) string {
	switch providerType {
	case models.SCMProviderTypeGitea:
		return "Gitea/Forgejo"
	case models.SCMProviderTypeGitLab:
		return "GitLab"
	default:
		return "GitHub"
	}
}

func (s *IssueSyncService) providerForConnection(ctx context.Context, connectionID int) (Provider, error) {
	if s.resolveProviderOverride != nil {
		return s.resolveProviderOverride(ctx, connectionID)
	}
	credResolver := &CredentialResolver{db: s.db, encryption: s.encryption}
	return credResolver.GetProviderForConnection(ctx, connectionID)
}

// SetUserService sets the user service for looking up comment authors.
func (s *IssueSyncService) SetUserService(us interface {
	GetByID(int) (*models.User, error)
}) {
	s.userService = us
}

// NewIssueSyncService creates a new IssueSyncService.
func NewIssueSyncService(db database.Database, encryption *sso.SecretEncryption) *IssueSyncService {
	return &IssueSyncService{db: db, encryption: encryption}
}

func (s *IssueSyncService) HasEnabledSyncConfig(ctx context.Context) (bool, error) {
	var enabled bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM issue_sync_configs isc
			JOIN workspace_repositories wr ON wr.id = isc.workspace_repository_id
			JOIN workspace_scm_connections wsc ON wsc.id = wr.workspace_scm_connection_id
			WHERE isc.sync_enabled = ? AND wr.is_active = ? AND wsc.enabled = ?
		)
	`, true, true, true).Scan(&enabled)
	if err != nil {
		return false, fmt.Errorf("check enabled issue sync configs: %w", err)
	}
	return enabled, nil
}

// SyncAll finds all enabled issue sync configs and syncs each one.
func (s *IssueSyncService) SyncAll(ctx context.Context) error {
	if !s.syncMu.TryLock() {
		slog.Info("Issue sync skipped: previous run still active")
		return nil
	}
	defer s.syncMu.Unlock()

	jobs, err := s.loadSyncJobs(ctx, "isc.sync_enabled = ? AND wr.is_active = ? AND wsc.enabled = ?", true, true, true)
	if err != nil {
		return err
	}
	for i := range jobs {
		if err := s.runSyncJob(ctx, &jobs[i]); err != nil {
			slog.Error("issue sync failed", "config_id", jobs[i].config.ID, "repo", jobs[i].repoName, "error", err)
		}
	}
	return nil
}

// SyncRepository syncs the enabled issue sync config of a workspace
// repository, if it has one. Webhook deliveries for the repository's issues
// call it; the scheduled SyncAll remains the fallback.
func (s *IssueSyncService) SyncRepository(ctx context.Context, workspaceRepositoryID int) error {
	jobs, err := s.loadSyncJobs(ctx,
		"isc.workspace_repository_id = ? AND isc.sync_enabled = ? AND wr.is_active = ? AND wsc.enabled = ?",
		workspaceRepositoryID, true, true, true)
	if err != nil {
		return err
	}
	for i := range jobs {
		if err := s.runSyncJob(ctx, &jobs[i]); err != nil {
			return err
		}
	}
	return nil
}

// loadSyncJobs loads the issue sync configs matching filter, a condition on
// issue_sync_configs isc, workspace_repositories wr, and
// workspace_scm_connections wsc.
func (s *IssueSyncService) loadSyncJobs(ctx context.Context, filter string, args ...any) ([]issueSyncJob, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT isc.id, isc.workspace_repository_id, isc.status_mapping, isc.reverse_status_mapping,
			   isc.label_sync_mode, isc.label_mappings, isc.filter_labels,
			   isc.assignee_mappings, isc.milestone_mappings,
			   isc.default_item_type_id, isc.default_priority_id, isc.sync_comments,
			   isc.last_full_sync_at,
			   wr.repository_name, wr.workspace_scm_connection_id,
			   wsc.workspace_id
		FROM issue_sync_configs isc
		JOIN workspace_repositories wr ON wr.id = isc.workspace_repository_id
		JOIN workspace_scm_connections wsc ON wsc.id = wr.workspace_scm_connection_id
		WHERE `+filter, args...)
	if err != nil {
		return nil, fmt.Errorf("query issue sync configs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var jobs []issueSyncJob
	for rows.Next() {
		var j issueSyncJob
		var lastSync sql.NullTime
		var defaultItemType, defaultPriority sql.NullInt64
		if err := rows.Scan(
			&j.config.ID, &j.config.WorkspaceRepositoryID,
			&j.config.StatusMapping, &j.config.ReverseStatusMapping,
			&j.config.LabelSyncMode, &j.config.LabelMappings, &j.config.FilterLabels,
			&j.config.AssigneeMappings, &j.config.MilestoneMappings,
			&defaultItemType, &defaultPriority, &j.config.SyncComments,
			&lastSync,
			&j.repoName, &j.connectionID, &j.config.WorkspaceID,
		); err != nil {
			slog.Error("scan issue sync config", "error", err)
			continue
		}
		if lastSync.Valid {
			j.config.LastFullSyncAt = &lastSync.Time
		}
		if defaultItemType.Valid {
			v := int(defaultItemType.Int64)
			j.config.DefaultItemTypeID = &v
		}
		if defaultPriority.Valid {
			v := int(defaultPriority.Int64)
			j.config.DefaultPriorityID = &v
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate issue sync configs: %w", err)
	}
	return jobs, nil
}

// runSyncJob runs one config's sync once no other sync of it is running, and
// records the outcome on the config. last_full_sync_at becomes the time the
// sync started, so an issue changed while the sync ran is listed again by
// the next one.
func (s *IssueSyncService) runSyncJob(ctx context.Context, j *issueSyncJob) error {
	release, err := lockIssueSyncConfig(ctx, j.config.ID)
	if err != nil {
		return err
	}
	defer release()

	started := time.Now()
	if err := s.syncJob(ctx, j); err != nil {
		s.recordSyncError(j.config.ID, err.Error())
		return err
	}
	now := time.Now()
	_, _ = s.db.ExecWriteContext(ctx,
		"UPDATE issue_sync_configs SET last_full_sync_at = ?, last_sync_error = NULL, updated_at = ? WHERE id = ?",
		started, now, j.config.ID)
	return nil
}

func (s *IssueSyncService) syncJob(ctx context.Context, j *issueSyncJob) error {
	provider, err := s.providerForConnection(ctx, j.connectionID)
	if err != nil {
		return fmt.Errorf("resolve provider: %w", err)
	}
	issueProvider, ok := provider.(IssueProvider)
	if !ok {
		return fmt.Errorf("provider does not support issue sync")
	}
	return s.syncConfig(ctx, issueProvider, &j.config, j.repoName)
}

// syncConfig syncs a single issue sync configuration.
func (s *IssueSyncService) syncConfig(ctx context.Context, provider IssueProvider, config *models.IssueSyncConfig, repoName string) error {
	parts := strings.SplitN(repoName, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid repository name: %s", repoName)
	}
	owner, repo := parts[0], parts[1]

	filterLabels := parseFilterLabels(config.FilterLabels)

	opts := ListIssueOptions{
		State:   "all",
		Since:   config.LastFullSyncAt,
		PerPage: 100,
	}
	// The filter keeps issues with any of its labels, and Windshift applies
	// it below. The forges read a labels list differently (GitHub: all of
	// them; Gitea/Forgejo: any of them, ignoring labels the repository lacks),
	// so the list is only narrowed by the forge for a single label, where
	// both readings agree.
	if len(filterLabels) == 1 {
		opts.Labels = filterLabels
	}

	// Paginate through all issues. Per-issue errors are collected and returned
	// together so callers do NOT advance last_full_sync_at when any issue fails
	// — otherwise a broken issue would be silently skipped forever (the next
	// pull uses last_full_sync_at as the "since" cutoff).
	var issueErrs []error
	page := 1
	for {
		opts.Page = page
		var issues []Issue
		var hasNext bool
		var err error
		if paginated, ok := provider.(PaginatedIssueProvider); ok {
			issues, hasNext, err = paginated.ListIssuesPage(ctx, owner, repo, opts)
		} else {
			// Compatibility fallback for providers that only expose the original
			// issue-list contract. Such providers must not remove entries before
			// returning the page if they rely on the length pagination signal.
			issues, err = provider.ListIssues(ctx, owner, repo, opts)
			hasNext = len(issues) == opts.PerPage
		}
		if err != nil {
			if errors.Is(err, ErrRateLimited) {
				return fmt.Errorf("list issues page %d: %w", page, err)
			}
			return fmt.Errorf("list issues page %d: %w", page, err)
		}

		for i := range issues {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("sync interrupted: %w", err)
			}
			if len(filterLabels) > 0 && !issueHasAnyLabel(&issues[i], filterLabels) {
				continue
			}
			if err := s.syncIssue(ctx, provider, config, owner, repo, &issues[i]); err != nil {
				slog.Error("sync issue", "config_id", config.ID, "issue_number", issues[i].Number, "error", err)
				issueErrs = append(issueErrs, fmt.Errorf("issue #%d: %w", issues[i].Number, err))
			}
		}

		if !hasNext {
			break
		}
		page++
	}

	if len(issueErrs) > 0 {
		return fmt.Errorf("sync config %d: %d issue(s) failed: %w", config.ID, len(issueErrs), errors.Join(issueErrs...))
	}
	return nil
}

// parseFilterLabels reads a config's filter_labels JSON array, dropping
// blank entries. An empty or unreadable value means no filter.
func parseFilterLabels(raw string) []string {
	var labels []string
	if raw == "" || raw == "[]" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		return nil
	}
	kept := labels[:0]
	for _, label := range labels {
		if label = strings.TrimSpace(label); label != "" {
			kept = append(kept, label)
		}
	}
	return kept
}

// issueHasAnyLabel reports whether the issue has at least one of labels.
// Names compare case-insensitively, as GitHub treats label names.
func issueHasAnyLabel(issue *Issue, labels []string) bool {
	for _, have := range issue.Labels {
		for _, want := range labels {
			if strings.EqualFold(have.Name, want) {
				return true
			}
		}
	}
	return false
}

// syncIssue syncs a single issue to a Windshift item.
func (s *IssueSyncService) syncIssue(ctx context.Context, provider IssueProvider, config *models.IssueSyncConfig, owner, repo string, issue *Issue) error {
	var syncItemID int
	var itemID int
	var lastGHUpdated sql.NullTime
	var syncLock bool

	err := s.db.QueryRowContext(ctx,
		"SELECT id, item_id, last_github_updated_at, sync_lock FROM issue_sync_items WHERE issue_sync_config_id = ? AND github_issue_number = ?",
		config.ID, issue.Number,
	).Scan(&syncItemID, &itemID, &lastGHUpdated, &syncLock)

	if errors.Is(err, sql.ErrNoRows) {
		if err := s.createItemFromIssue(ctx, config, issue); err != nil {
			return err
		}
		if config.SyncComments {
			var newSyncItemID, newItemID int
			if lookupErr := s.db.QueryRowContext(ctx,
				"SELECT id, item_id FROM issue_sync_items WHERE issue_sync_config_id = ? AND github_issue_number = ?",
				config.ID, issue.Number,
			).Scan(&newSyncItemID, &newItemID); lookupErr == nil {
				s.syncComments(ctx, provider, owner, repo, issue.Number, newSyncItemID, newItemID)
			}
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("lookup sync item: %w", err)
	}

	// If sync_lock is set, this was recently pushed back from Windshift — skip once and clear lock
	if syncLock {
		now := time.Now()
		_, _ = s.db.ExecWriteContext(ctx,
			"UPDATE issue_sync_items SET sync_lock = ?, last_github_updated_at = ?, last_synced_at = ?, updated_at = ? WHERE id = ?",
			false, issue.UpdatedAt, now, now, syncItemID)
		return nil
	}

	if lastGHUpdated.Valid && !issue.UpdatedAt.After(lastGHUpdated.Time) {
		// Comments have their own update cadence.
		if config.SyncComments {
			s.syncComments(ctx, provider, owner, repo, issue.Number, syncItemID, itemID)
		}
		return nil
	}

	if err := s.updateItemFromIssue(ctx, config, issue, itemID, syncItemID); err != nil {
		return err
	}

	if config.SyncComments {
		s.syncComments(ctx, provider, owner, repo, issue.Number, syncItemID, itemID)
	}

	return nil
}

// createItemFromIssue creates a new Windshift item from an issue.
func (s *IssueSyncService) createItemFromIssue(ctx context.Context, config *models.IssueSyncConfig, issue *Issue) error {
	statusID := s.resolveStatusID(config, issue.State)

	assigneeID := s.resolveAssigneeID(config, issue)

	milestoneID := s.resolveMilestoneID(config, issue)

	input := services.ItemCreateInput{
		WorkspaceID: config.WorkspaceID,
		ItemTypeID:  config.DefaultItemTypeID,
		Title:       issue.Title,
		Description: issue.Body,
		StatusID:    statusID,
		PriorityID:  config.DefaultPriorityID,
		AssigneeID:  assigneeID,
	}

	milestoneIDs := []int{}
	if milestoneID != nil {
		milestoneIDs = append(milestoneIDs, *milestoneID)
	}
	input.MilestoneIDs = milestoneIDs
	item, err := services.NewExternalItemReconciliationService(s.db).Create(ctx, services.ExternalItemCreateRequest{
		Policy: services.GitHubIssueSyncReconciliationPolicy(),
		Input:  input,
		AfterCreate: func(ctx context.Context, tx database.Tx, itemID int) error {
			now := time.Now()
			if _, err := tx.ExecContext(ctx, `
			INSERT INTO issue_sync_items (
				issue_sync_config_id, item_id, github_issue_number, github_issue_id,
				github_issue_url, last_synced_at, last_github_updated_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
				config.ID, itemID, issue.Number, issue.ID,
				issue.URL, now, issue.UpdatedAt, now, now,
			); err != nil {
				return fmt.Errorf("insert sync item: %w", err)
			}

			// Keep item, labels, and sync metadata atomic.
			if err := s.syncLabels(ctx, tx, config, issue, itemID); err != nil {
				return fmt.Errorf("sync labels: %w", err)
			}
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("create item from issue: %w", err)
	}

	slog.Info("created item from issue",
		"config_id", config.ID, "issue_number", issue.Number, "item_id", item.ID)

	return nil
}

// updateItemFromIssue updates an existing Windshift item from a changed issue.
func (s *IssueSyncService) updateItemFromIssue(ctx context.Context, config *models.IssueSyncConfig, issue *Issue, itemID, syncItemID int) error {
	statusID := s.resolveStatusID(config, issue.State)
	assigneeID := s.resolveAssigneeID(config, issue)
	milestoneID := s.resolveMilestoneID(config, issue)

	milestoneIDs := []int{}
	if milestoneID != nil {
		milestoneIDs = append(milestoneIDs, *milestoneID)
	}
	var statusValue any
	if statusID != nil {
		statusValue = *statusID
	}
	var assigneeValue any
	if assigneeID != nil {
		assigneeValue = *assigneeID
	}
	_, err := services.NewExternalItemReconciliationService(s.db).Update(ctx, services.ExternalItemUpdateRequest{
		Policy: services.GitHubIssueSyncReconciliationPolicy(),
		ItemID: itemID,
		UpdateData: map[string]any{
			"title":         issue.Title,
			"description":   issue.Body,
			"status_id":     statusValue,
			"assignee_id":   assigneeValue,
			"milestone_ids": milestoneIDs,
		},
		AfterUpdate: func(ctx context.Context, tx database.Tx, _, _ *models.Item) error {
			now := time.Now()
			result, err := tx.ExecContext(ctx,
				"UPDATE issue_sync_items SET last_synced_at = ?, last_github_updated_at = ?, updated_at = ? WHERE id = ?",
				now, issue.UpdatedAt, now, syncItemID)
			if err != nil {
				return fmt.Errorf("update sync item %d: %w", syncItemID, err)
			}
			rowsAffected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("count updated sync items: %w", err)
			}
			if rowsAffected != 1 {
				return fmt.Errorf("sync item %d not found", syncItemID)
			}
			if err := s.syncLabels(ctx, tx, config, issue, itemID); err != nil {
				return fmt.Errorf("sync labels: %w", err)
			}
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("update item from issue: %w", err)
	}

	slog.Info("updated item from issue",
		"config_id", config.ID, "issue_number", issue.Number, "item_id", itemID)

	return nil
}

// PushStatusToIssue pushes a Windshift status change back to the linked issue.
func (s *IssueSyncService) PushStatusToIssue(ctx context.Context, itemID, newStatusID int) {
	var syncItemID int
	var configID int
	var issueNumber int
	var repoName string
	var connectionID int
	var reverseMapping string

	err := s.db.QueryRowContext(ctx, `
		SELECT isi.id, isi.issue_sync_config_id, isi.github_issue_number,
			   wr.repository_name, wr.workspace_scm_connection_id,
			   isc.reverse_status_mapping
		FROM issue_sync_items isi
		JOIN issue_sync_configs isc ON isc.id = isi.issue_sync_config_id
		JOIN workspace_repositories wr ON wr.id = isc.workspace_repository_id
		WHERE isi.item_id = ? AND isc.sync_enabled = ?
	`, itemID, true).Scan(&syncItemID, &configID, &issueNumber, &repoName, &connectionID, &reverseMapping)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("lookup sync item for pushback", "item_id", itemID, "error", err)
		}
		return
	}

	var statusMap map[string]string
	if err := json.Unmarshal([]byte(reverseMapping), &statusMap); err != nil {
		slog.Error("parse reverse status mapping", "config_id", configID, "error", err)
		return
	}

	issueState, ok := statusMap[strconv.Itoa(newStatusID)]
	if !ok {
		return // No mapping for this status
	}

	// Resolve provider. Note: we deliberately do NOT set sync_lock yet — the lock
	// is the signal to the next inbound sync to skip one cycle (loopback
	// prevention), so we only set it if we actually issue the issue update.
	// Setting it earlier and bailing on preflight would wedge the item.
	provider, err := s.providerForConnection(ctx, connectionID)
	if err != nil {
		slog.Error("resolve provider for status pushback", "config_id", configID, "error", err)
		return
	}

	issueProvider, ok := provider.(IssueProvider)
	if !ok {
		slog.Error("provider does not support issues for pushback", "config_id", configID)
		return
	}

	parts := strings.SplitN(repoName, "/", 2)
	if len(parts) != 2 {
		return
	}

	// Hold the config's sync lock across claiming sync_lock and the remote
	// write: an inbound sync running in between would consume sync_lock
	// against the issue as it was before the write, and the sync after the
	// write would then map the new issue state back over the item's status.
	// If no lock comes within pushStatusLockWait (a long inbound sync), push
	// without it: the race is rare and visible, a dropped push is neither.
	lockCtx, cancelLock := context.WithTimeout(ctx, pushStatusLockWait)
	release, err := lockIssueSyncConfig(lockCtx, configID)
	cancelLock()
	switch {
	case err == nil:
		defer release()
	case ctx.Err() != nil:
		slog.Error("push status to issue", "config_id", configID, "issue", issueNumber, "state", issueState, "error", err)
		return
	default:
		slog.Warn("push status to issue without the sync lock: an inbound sync is still running",
			"config_id", configID, "issue", issueNumber, "state", issueState, "waited", pushStatusLockWait)
	}

	// Preflight passed — claim the lock immediately before the remote write.
	_, _ = s.db.ExecWriteContext(ctx,
		"UPDATE issue_sync_items SET sync_lock = ?, updated_at = ? WHERE id = ?",
		true, time.Now(), syncItemID)

	_, err = issueProvider.UpdateIssue(ctx, parts[0], parts[1], issueNumber, UpdateIssueOptions{
		State: &issueState,
	})
	if err != nil {
		slog.Error("push status to issue", "config_id", configID, "issue", issueNumber, "state", issueState, "error", err)
		// Clear lock on failure so next sync can pick it up
		_, _ = s.db.ExecWriteContext(ctx,
			"UPDATE issue_sync_items SET sync_lock = ?, updated_at = ? WHERE id = ?",
			false, time.Now(), syncItemID)
	}
}

// PushCommentToIssue pushes a Windshift comment to the linked issue.
func (s *IssueSyncService) PushCommentToIssue(ctx context.Context, itemID, commentID, authorID int, commentBody string) {
	if s.userService != nil {
		if user, err := s.userService.GetByID(authorID); err == nil {
			authorName := strings.TrimSpace(user.FullName)
			if authorName == "" {
				authorName = user.Username
			}
			if authorName != "" {
				commentBody = fmt.Sprintf("**%s** commented in Windshift:\n\n%s", authorName, commentBody)
			}
		}
	}
	var syncItemID int
	var issueNumber int
	var repoName string
	var connectionID int

	err := s.db.QueryRowContext(ctx, `
		SELECT isi.id, isi.github_issue_number, wr.repository_name, wr.workspace_scm_connection_id
		FROM issue_sync_items isi
		JOIN issue_sync_configs isc ON isc.id = isi.issue_sync_config_id
		JOIN workspace_repositories wr ON wr.id = isc.workspace_repository_id
		WHERE isi.item_id = ? AND isc.sync_enabled = ? AND isc.sync_comments = ?
	`, itemID, true, true).Scan(&syncItemID, &issueNumber, &repoName, &connectionID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("lookup sync item for comment pushback", "item_id", itemID, "error", err)
		}
		return
	}

	provider, err := s.providerForConnection(ctx, connectionID)
	if err != nil {
		slog.Error("resolve provider for comment pushback", "error", err)
		return
	}

	issueProvider, ok := provider.(IssueProvider)
	if !ok {
		return
	}

	parts := strings.SplitN(repoName, "/", 2)
	if len(parts) != 2 {
		return
	}

	ghCommentID, err := issueProvider.CreateIssueComment(ctx, parts[0], parts[1], issueNumber, commentBody)
	if err != nil {
		slog.Error("push comment to issue", "issue", issueNumber, "error", err)
		return
	}

	now := time.Now()
	_, _ = s.db.ExecWriteContext(ctx, `
		INSERT INTO issue_sync_comments (issue_sync_item_id, comment_id, github_comment_id, github_updated_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, syncItemID, commentID, ghCommentID, now, now, now)
}

// PushCommentUpdateToIssue pushes a Windshift comment edit to the linked issue comment.
func (s *IssueSyncService) PushCommentUpdateToIssue(ctx context.Context, commentID, authorID int, newBody string) {
	var ghCommentID int64
	var issueNumber int
	var repoName string
	var connectionID int

	err := s.db.QueryRowContext(ctx, `
		SELECT isc2.github_comment_id, isi.github_issue_number, wr.repository_name, wr.workspace_scm_connection_id
		FROM issue_sync_comments isc2
		JOIN issue_sync_items isi ON isi.id = isc2.issue_sync_item_id
		JOIN issue_sync_configs isc ON isc.id = isi.issue_sync_config_id
		JOIN workspace_repositories wr ON wr.id = isc.workspace_repository_id
		WHERE isc2.comment_id = ? AND isc.sync_enabled = ? AND isc.sync_comments = ?
	`, commentID, true, true).Scan(&ghCommentID, &issueNumber, &repoName, &connectionID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("lookup sync comment for update pushback", "comment_id", commentID, "error", err)
		}
		return
	}

	if s.userService != nil {
		if user, err := s.userService.GetByID(authorID); err == nil {
			authorName := strings.TrimSpace(user.FullName)
			if authorName == "" {
				authorName = user.Username
			}
			if authorName != "" {
				newBody = fmt.Sprintf("**%s** commented in Windshift:\n\n%s", authorName, newBody)
			}
		}
	}

	provider, err := s.providerForConnection(ctx, connectionID)
	if err != nil {
		slog.Error("resolve provider for comment update pushback", "error", err)
		return
	}

	issueProvider, ok := provider.(IssueProvider)
	if !ok {
		return
	}

	parts := strings.SplitN(repoName, "/", 2)
	if len(parts) != 2 {
		return
	}

	if err := issueProvider.UpdateIssueComment(ctx, parts[0], parts[1], issueNumber, ghCommentID, newBody); err != nil {
		slog.Error("push comment update to issue", "github_comment_id", ghCommentID, "error", err)
	}
}

// syncComments pulls issue comments into Windshift.
func (s *IssueSyncService) syncComments(ctx context.Context, provider IssueProvider, owner, repo string, issueNumber, syncItemID, itemID int) {
	// Fetch remotely before opening the transaction.
	comments, err := provider.ListIssueComments(ctx, owner, repo, issueNumber)
	if err != nil {
		slog.Error("list issue comments", "issue_number", issueNumber, "error", err)
		return
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		slog.Error("begin comment sync tx", "issue_number", issueNumber, "error", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	// All comment-table writes go through CommentService (the single
	// comment-write chokepoint); the tx-aware variants keep the comment and its
	// sync-tracking row atomic. We publish once after commit below.
	commentSvc := services.NewCommentService(s.db)
	commentsChanged := false
	forgeName := issueForgeName(provider.GetType())
	actor := itemevents.Integration(string(provider.GetType()), "scm")
	for _, ghComment := range comments {
		if strings.Contains(ghComment.Body, "commented in Windshift:") && strings.HasPrefix(ghComment.Body, "**") {
			continue
		}

		var trackingID int
		var existingCommentID sql.NullInt64
		var lastGHUpdated sql.NullTime

		err := tx.QueryRowContext(ctx,
			"SELECT id, comment_id, github_updated_at FROM issue_sync_comments WHERE issue_sync_item_id = ? AND github_comment_id = ?",
			syncItemID, ghComment.ID,
		).Scan(&trackingID, &existingCommentID, &lastGHUpdated)

		if errors.Is(err, sql.ErrNoRows) {
			body := fmt.Sprintf("**@%s** commented on %s:\n\n%s", ghComment.User.Username, forgeName, ghComment.Body)
			now := time.Now()

			wsCommentID, insertErr := commentSvc.CreateInTx(ctx, tx, itemID, 0, body, now, actor)
			if insertErr != nil {
				slog.Error("insert synced comment", "github_comment_id", ghComment.ID, "error", insertErr)
				continue
			}

			_, _ = tx.ExecContext(ctx, `
				INSERT INTO issue_sync_comments (issue_sync_item_id, comment_id, github_comment_id, github_updated_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?)
			`, syncItemID, wsCommentID, ghComment.ID, ghComment.UpdatedAt, now, now)

			commentsChanged = true
			continue
		}
		if err != nil {
			slog.Error("lookup sync comment", "github_comment_id", ghComment.ID, "error", err)
			continue
		}

		if !existingCommentID.Valid {
			continue // Windshift comment was deleted, skip
		}
		if lastGHUpdated.Valid && !ghComment.UpdatedAt.After(lastGHUpdated.Time) {
			continue // No changes
		}

		body := fmt.Sprintf("**@%s** commented on %s:\n\n%s", ghComment.User.Username, forgeName, ghComment.Body)
		now := time.Now()
		_ = commentSvc.UpdateContentInTx(ctx, tx, int(existingCommentID.Int64), body, now)
		_, _ = tx.ExecContext(ctx,
			"UPDATE issue_sync_comments SET github_updated_at = ?, updated_at = ? WHERE id = ?",
			ghComment.UpdatedAt, now, trackingID)
		commentsChanged = true
	}

	if commentsChanged {
		if err := repository.NewItemRepository(s.db).TouchActivity(tx, itemID, time.Now()); err != nil {
			slog.Error("bump item activity for synced comments", "item_id", itemID, "error", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		slog.Error("commit comment sync tx", "issue_number", issueNumber, "error", err)
		return
	}

	// Live-update publish (WI-483): issue-sourced comments committed; refresh
	// the item's comment list for anyone viewing it.
	if commentsChanged {
		services.PublishItemChange(itemID, services.ItemChangeComment)
	}
}

// GetSyncConfigForWorkspace returns the sync config for a workspace, if any.
func (s *IssueSyncService) GetSyncConfigForWorkspace(ctx context.Context, workspaceID int) (*models.IssueSyncConfig, error) {
	var config models.IssueSyncConfig
	var lastSync sql.NullTime
	var lastError sql.NullString
	var defaultItemType, defaultPriority sql.NullInt64
	var createdBy sql.NullInt64

	err := s.db.QueryRowContext(ctx, `
		SELECT isc.id, isc.workspace_repository_id, isc.sync_enabled,
			   isc.status_mapping, isc.reverse_status_mapping,
			   isc.label_sync_mode, isc.label_mappings, isc.filter_labels,
			   isc.assignee_mappings, isc.milestone_mappings,
			   isc.default_item_type_id, isc.default_priority_id, isc.sync_comments,
			   isc.last_full_sync_at, isc.last_sync_error,
			   isc.created_by, isc.created_at, isc.updated_at,
			   wr.repository_name
		FROM issue_sync_configs isc
		JOIN workspace_repositories wr ON wr.id = isc.workspace_repository_id
		JOIN workspace_scm_connections wsc ON wsc.id = wr.workspace_scm_connection_id
		WHERE wsc.workspace_id = ?
		LIMIT 1
	`, workspaceID).Scan(
		&config.ID, &config.WorkspaceRepositoryID, &config.SyncEnabled,
		&config.StatusMapping, &config.ReverseStatusMapping,
		&config.LabelSyncMode, &config.LabelMappings, &config.FilterLabels,
		&config.AssigneeMappings, &config.MilestoneMappings,
		&defaultItemType, &defaultPriority, &config.SyncComments,
		&lastSync, &lastError,
		&createdBy, &config.CreatedAt, &config.UpdatedAt,
		&config.RepositoryName,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if lastSync.Valid {
		config.LastFullSyncAt = &lastSync.Time
	}
	if lastError.Valid {
		config.LastSyncError = lastError.String
	}
	if defaultItemType.Valid {
		v := int(defaultItemType.Int64)
		config.DefaultItemTypeID = &v
	}
	if defaultPriority.Valid {
		v := int(defaultPriority.Int64)
		config.DefaultPriorityID = &v
	}
	if createdBy.Valid {
		v := int(createdBy.Int64)
		config.CreatedBy = &v
	}
	config.WorkspaceID = workspaceID

	// Get synced item count
	_ = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM issue_sync_items WHERE issue_sync_config_id = ?", config.ID,
	).Scan(&config.SyncedItemCount)

	return &config, nil
}

// VerifyRepositoryInWorkspace returns true when the given workspace repository
// belongs to the given workspace.
func (s *IssueSyncService) VerifyRepositoryInWorkspace(ctx context.Context, workspaceRepositoryID, workspaceID int) (bool, error) {
	var repoWorkspaceID int
	err := s.db.QueryRowContext(ctx, `
		SELECT wsc.workspace_id FROM workspace_repositories wr
		JOIN workspace_scm_connections wsc ON wsc.id = wr.workspace_scm_connection_id
		WHERE wr.id = ?
	`, workspaceRepositoryID).Scan(&repoWorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return repoWorkspaceID == workspaceID, nil
}

// CreateSyncConfig inserts a new issue sync configuration row, applying the
// caller-supplied request defaults. Returns the new config ID.
func (s *IssueSyncService) CreateSyncConfig(ctx context.Context, createdByUserID int, req models.IssueSyncConfigRequest) (int, error) {
	// Enforce one config per workspace. The workspace-scoped Get/Update/Delete
	// endpoints assume a single config; the schema's UNIQUE is only per repo,
	// so without this guard a workspace could end up with multiple configs and
	// GetSyncConfigForWorkspace's LIMIT 1 would silently pick whichever the DB
	// returned first. Look up the target workspace via the requested repo.
	var existingID int
	err := s.db.QueryRowContext(ctx, `
		SELECT isc.id
		FROM issue_sync_configs isc
		JOIN workspace_repositories wr_existing ON wr_existing.id = isc.workspace_repository_id
		JOIN workspace_scm_connections wsc_existing ON wsc_existing.id = wr_existing.workspace_scm_connection_id
		JOIN workspace_repositories wr_target ON wr_target.id = ?
		JOIN workspace_scm_connections wsc_target ON wsc_target.id = wr_target.workspace_scm_connection_id
		WHERE wsc_existing.workspace_id = wsc_target.workspace_id
		LIMIT 1
	`, req.WorkspaceRepositoryID).Scan(&existingID)
	if err == nil {
		return 0, ErrSyncConfigExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("check existing sync config: %w", err)
	}

	if req.StatusMapping == "" {
		req.StatusMapping = "{}"
	}
	if req.ReverseStatusMapping == "" {
		req.ReverseStatusMapping = "{}"
	}
	if req.LabelSyncMode == "" {
		req.LabelSyncMode = models.IssueSyncLabelNone
	}
	if req.LabelMappings == "" {
		req.LabelMappings = "[]"
	}
	if req.FilterLabels == "" {
		req.FilterLabels = "[]"
	}
	if req.AssigneeMappings == "" {
		req.AssigneeMappings = "{}"
	}
	if req.MilestoneMappings == "" {
		req.MilestoneMappings = "{}"
	}

	var configID int
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO issue_sync_configs (
			workspace_repository_id, sync_enabled,
			status_mapping, reverse_status_mapping,
			label_sync_mode, label_mappings, filter_labels,
			assignee_mappings, milestone_mappings,
			default_item_type_id, default_priority_id,
			sync_comments, created_by
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id
	`,
		req.WorkspaceRepositoryID, req.SyncEnabled,
		req.StatusMapping, req.ReverseStatusMapping,
		req.LabelSyncMode, req.LabelMappings, req.FilterLabels,
		req.AssigneeMappings, req.MilestoneMappings,
		req.DefaultItemTypeID, req.DefaultPriorityID,
		req.SyncComments, createdByUserID,
	).Scan(&configID)
	return configID, err
}

// UpdateSyncConfig updates the writable fields on a sync config row.
func (s *IssueSyncService) UpdateSyncConfig(ctx context.Context, configID int, req models.IssueSyncConfigRequest) error {
	_, err := s.db.ExecWriteContext(ctx, `
		UPDATE issue_sync_configs SET
			sync_enabled = ?, status_mapping = ?, reverse_status_mapping = ?,
			label_sync_mode = ?, label_mappings = ?, filter_labels = ?,
			assignee_mappings = ?, milestone_mappings = ?,
			default_item_type_id = ?, default_priority_id = ?,
			sync_comments = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`,
		req.SyncEnabled, req.StatusMapping, req.ReverseStatusMapping,
		req.LabelSyncMode, req.LabelMappings, req.FilterLabels,
		req.AssigneeMappings, req.MilestoneMappings,
		req.DefaultItemTypeID, req.DefaultPriorityID,
		req.SyncComments, configID,
	)
	return err
}

// DeleteSyncConfig removes a sync config row. Cascades clean up linked
// issue_sync_items rows.
func (s *IssueSyncService) DeleteSyncConfig(ctx context.Context, configID int) error {
	_, err := s.db.ExecWriteContext(ctx, "DELETE FROM issue_sync_configs WHERE id = ?", configID)
	return err
}

// GetSyncedItems returns all synced items for a config.
func (s *IssueSyncService) GetSyncedItems(ctx context.Context, configID int) ([]models.IssueSyncItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT isi.id, isi.issue_sync_config_id, isi.item_id,
			   isi.github_issue_number, isi.github_issue_id, isi.github_issue_url,
			   isi.last_synced_at, isi.last_github_updated_at, isi.sync_lock,
			   isi.created_at, isi.updated_at
		FROM issue_sync_items isi
		WHERE isi.issue_sync_config_id = ?
		ORDER BY isi.github_issue_number
	`, configID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]models.IssueSyncItem, 0)
	itemIDs := make([]int, 0)
	for rows.Next() {
		var item models.IssueSyncItem
		var lastSync, lastGH sql.NullTime
		if err := rows.Scan(
			&item.ID, &item.IssueSyncConfigID, &item.ItemID,
			&item.GitHubIssueNumber, &item.GitHubIssueID, &item.GitHubIssueURL,
			&lastSync, &lastGH, &item.SyncLock,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if lastSync.Valid {
			item.LastSyncedAt = &lastSync.Time
		}
		if lastGH.Valid {
			item.LastGitHubUpdatedAt = &lastGH.Time
		}
		items = append(items, item)
		itemIDs = append(itemIDs, item.ItemID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	itemDetails, err := repository.NewItemRepository(s.db).FindByIDsWithDetails(itemIDs)
	if err != nil {
		return nil, fmt.Errorf("load synced item details: %w", err)
	}
	detailsByID := make(map[int]*models.Item, len(itemDetails))
	for _, item := range itemDetails {
		detailsByID[item.ID] = item
	}
	for i := range items {
		if item := detailsByID[items[i].ItemID]; item != nil {
			items[i].ItemTitle = item.Title
			items[i].WorkspaceItemNumber = item.WorkspaceItemNumber
			items[i].WorkspaceKey = item.WorkspaceKey
		}
	}
	return items, nil
}

// TriggerSync runs a single sync for a specific config.
func (s *IssueSyncService) TriggerSync(ctx context.Context, configID int) error {
	jobs, err := s.loadSyncJobs(ctx, "isc.id = ?", configID)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if len(jobs) == 0 {
		return fmt.Errorf("load config: %w", sql.ErrNoRows)
	}
	return s.runSyncJob(ctx, &jobs[0])
}

// Helper methods

func (s *IssueSyncService) resolveStatusID(config *models.IssueSyncConfig, issueState string) *int {
	var mapping map[string]int
	if err := json.Unmarshal([]byte(config.StatusMapping), &mapping); err != nil {
		return nil
	}
	if id, ok := mapping[issueState]; ok {
		return &id
	}
	return nil
}

func (s *IssueSyncService) resolveAssigneeID(config *models.IssueSyncConfig, issue *Issue) *int {
	if len(issue.Assignees) == 0 {
		return nil
	}
	var mapping map[string]int
	if err := json.Unmarshal([]byte(config.AssigneeMappings), &mapping); err != nil {
		return nil
	}
	// Use first matching assignee
	for _, a := range issue.Assignees {
		if id, ok := mapping[a.Username]; ok {
			return &id
		}
	}
	return nil
}

func (s *IssueSyncService) resolveMilestoneID(config *models.IssueSyncConfig, issue *Issue) *int {
	if issue.Milestone == nil {
		return nil
	}
	var mapping map[string]int
	if err := json.Unmarshal([]byte(config.MilestoneMappings), &mapping); err != nil {
		return nil
	}
	key := strconv.Itoa(issue.Milestone.Number)
	if id, ok := mapping[key]; ok {
		return &id
	}
	return nil
}

func (s *IssueSyncService) syncLabels(ctx context.Context, tx database.Tx, config *models.IssueSyncConfig, issue *Issue, itemID int) error {
	switch config.LabelSyncMode {
	case "", models.IssueSyncLabelNone:
		return nil
	case models.IssueSyncLabelMapped:
		// Use explicit mappings
		var mappings []models.LabelMapping
		if raw := strings.TrimSpace(config.LabelMappings); raw != "" {
			if err := json.Unmarshal([]byte(raw), &mappings); err != nil {
				return fmt.Errorf("parse label mappings: %w", err)
			}
		}
		// Without mappings there is nothing to map to: leave the item's
		// labels alone rather than replacing them with an empty set.
		if len(mappings) == 0 {
			return nil
		}

		// Build lookup: issue label name → windshift label ID
		ghToWS := make(map[string]int)
		for _, m := range mappings {
			ghToWS[m.GitHubLabel] = m.WindshiftLabelID
		}

		labelIDs := make([]int, 0, len(issue.Labels))
		for _, l := range issue.Labels {
			if wsLabelID, ok := ghToWS[l.Name]; ok {
				labelIDs = append(labelIDs, wsLabelID)
			}
		}
		if err := repository.NewLabelRepository(s.db).ReplaceItemLabelsTx(ctx, tx, itemID, labelIDs); err != nil {
			return fmt.Errorf("replace mapped labels: %w", err)
		}
	case models.IssueSyncLabelMirror:
		labelRepo := repository.NewLabelRepository(s.db)
		labelIDs := make([]int, 0, len(issue.Labels))
		for _, l := range issue.Labels {
			color := l.Color
			if color == "" {
				color = "808080"
			}
			labelID, err := labelRepo.EnsureByNameTx(ctx, tx, l.Name, color)
			if err != nil {
				return fmt.Errorf("ensure mirrored label %q: %w", l.Name, err)
			}
			labelIDs = append(labelIDs, labelID)
		}
		if err := labelRepo.ReplaceItemLabelsTx(ctx, tx, itemID, labelIDs); err != nil {
			return fmt.Errorf("replace mirrored labels: %w", err)
		}
	default:
		return fmt.Errorf("unsupported label sync mode %q", config.LabelSyncMode)
	}
	return nil
}

func (s *IssueSyncService) recordSyncError(configID int, errMsg string) {
	_, _ = s.db.ExecWrite(
		"UPDATE issue_sync_configs SET last_sync_error = ?, updated_at = ? WHERE id = ?",
		errMsg, time.Now(), configID)
}

// GetRepoLabels fetches the labels of a linked repository for the mapping UI.
// workspaceID gates the lookup: the repo must belong to that workspace, otherwise
// ErrRepositoryNotInWorkspace is returned (handlers map this to 404).
func (s *IssueSyncService) GetRepoLabels(ctx context.Context, workspaceID, workspaceRepoID int) ([]IssueLabel, error) {
	belongs, err := s.VerifyRepositoryInWorkspace(ctx, workspaceRepoID, workspaceID)
	if err != nil {
		return nil, err
	}
	if !belongs {
		return nil, ErrRepositoryNotInWorkspace
	}

	provider, repoName, err := s.resolveProviderForRepo(ctx, workspaceRepoID)
	if err != nil {
		return nil, err
	}

	issueProvider, ok := provider.(IssueProvider)
	if !ok {
		return nil, fmt.Errorf("provider does not support issues")
	}

	parts := strings.SplitN(repoName, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repository name: %s", repoName)
	}

	return issueProvider.ListRepoLabels(ctx, parts[0], parts[1])
}

// GetRepoMilestones fetches the milestones of a linked repository for the
// mapping UI. See GetRepoLabels for the workspaceID gating contract.
func (s *IssueSyncService) GetRepoMilestones(ctx context.Context, workspaceID, workspaceRepoID int) ([]IssueMilestone, error) {
	belongs, err := s.VerifyRepositoryInWorkspace(ctx, workspaceRepoID, workspaceID)
	if err != nil {
		return nil, err
	}
	if !belongs {
		return nil, ErrRepositoryNotInWorkspace
	}

	provider, repoName, err := s.resolveProviderForRepo(ctx, workspaceRepoID)
	if err != nil {
		return nil, err
	}

	issueProvider, ok := provider.(IssueProvider)
	if !ok {
		return nil, fmt.Errorf("provider does not support issues")
	}

	parts := strings.SplitN(repoName, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repository name: %s", repoName)
	}

	return issueProvider.ListRepoMilestones(ctx, parts[0], parts[1])
}

func (s *IssueSyncService) resolveProviderForRepo(ctx context.Context, workspaceRepoID int) (Provider, string, error) {
	var repoName string
	var connectionID int

	err := s.db.QueryRowContext(ctx, `
		SELECT wr.repository_name, wr.workspace_scm_connection_id
		FROM workspace_repositories wr
		WHERE wr.id = ?
	`, workspaceRepoID).Scan(&repoName, &connectionID)
	if err != nil {
		return nil, "", fmt.Errorf("lookup repo: %w", err)
	}

	provider, err := s.providerForConnection(ctx, connectionID)
	if err != nil {
		return nil, "", err
	}

	return provider, repoName, nil
}
