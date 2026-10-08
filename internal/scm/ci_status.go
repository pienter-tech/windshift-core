package scm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"

	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/utils"
)

// CI status on pull request links (WCORE-68) is display-only: the sync, the
// link refresh and CI webhook deliveries write the ci_* columns of
// item_scm_links and publish a link change for the item view, and nothing
// else. No action event is emitted for a CI result.

// ciStatusLocks serializes reading and storing one repository's CI status.
// The scheduler and the webhook handler each own a SyncService, and CI
// deliveries for one run arrive close together, so without the lock an older
// provider answer could overwrite a newer one.
var ciStatusLocks sync.Map // workspace repository ID -> *sync.Mutex

func lockRepositoryCIStatus(repoID int) func() {
	value, _ := ciStatusLocks.LoadOrStore(repoID, &sync.Mutex{})
	mu, ok := value.(*sync.Mutex)
	if !ok {
		// Programmer error — ciStatusLocks is populated only by this
		// function and only ever stores *sync.Mutex.
		panic(fmt.Sprintf("ciStatusLocks: unexpected value type %T", value))
	}
	mu.Lock()
	return mu.Unlock
}

// refreshPullRequestCI stores the combined CI status of an observed pull
// request's head on its links. Every observation of an open pull request reads
// it, which keeps it current through the repository sync and the link
// refresh. A merged or closed pull request is read only until its head has a
// final status. Failures are logged, not returned: CI status must never fail
// the sync or the refresh it rides on.
func (s *SyncService) refreshPullRequestCI(ctx context.Context, provider Provider, owner, repo string, repoID int, pr PullRequest) {
	ciProvider, ok := provider.(CIStatusProvider)
	if !ok || pr.HeadSHA == "" {
		return
	}
	externalID := strconv.Itoa(pr.Number)
	if pr.IsMerged || pr.State == "closed" {
		settled, err := s.pullRequestCISettled(ctx, repoID, externalID, pr.HeadSHA)
		if err != nil {
			slog.Warn("Failed to read stored CI status", slog.String("component", "scm"), slog.Int("pr", pr.Number), slog.Any("error", err))
			return
		}
		if settled {
			return
		}
	}
	if err := s.updatePullRequestCI(ctx, ciProvider, owner, repo, repoID, externalID, pr.HeadSHA); err != nil {
		slog.Warn("Failed to refresh pull request CI status", slog.String("component", "scm"), slog.Int("pr", pr.Number), slog.Any("error", err))
	}
}

// pullRequestCISettled reports whether every link to the pull request already
// holds a final CI status read for headSHA.
func (s *SyncService) pullRequestCISettled(ctx context.Context, repoID int, externalID, headSHA string) (bool, error) {
	var unsettled int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM item_scm_links
		WHERE workspace_repository_id = ? AND link_type = ? AND external_id = ?
		  AND (ci_head_sha IS NULL OR ci_head_sha != ? OR ci_state = ?)
	`, repoID, models.SCMLinkTypePullRequest, externalID, headSHA, CIStatePending).Scan(&unsettled)
	if err != nil {
		return false, err
	}
	return unsettled == 0, nil
}

// updatePullRequestCI reads the combined CI status of headSHA and stores it on
// every link to the pull request.
func (s *SyncService) updatePullRequestCI(ctx context.Context, ciProvider CIStatusProvider, owner, repo string, repoID int, externalID, headSHA string) error {
	unlock := lockRepositoryCIStatus(repoID)
	defer unlock()
	status, err := ciProvider.GetCombinedCIStatus(ctx, owner, repo, headSHA)
	if err != nil {
		return fmt.Errorf("get CI status of %s: %w", headSHA, err)
	}
	return s.storePullRequestCI(ctx, repoID, externalID, headSHA, status)
}

// storePullRequestCI writes a CI status onto the pull request's links. A nil
// status means no CI reported on headSHA and clears the stored state. Items
// whose displayed status changed get a link change published.
func (s *SyncService) storePullRequestCI(ctx context.Context, repoID int, externalID, headSHA string, status *CIStatus) error {
	var state, ciURL string
	if status != nil {
		state, ciURL = status.State, status.URL
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, item_id, ci_state, ci_url, ci_head_sha FROM item_scm_links
		WHERE workspace_repository_id = ? AND link_type = ? AND external_id = ?
	`, repoID, models.SCMLinkTypePullRequest, externalID)
	if err != nil {
		return fmt.Errorf("read PR links for CI status: %w", err)
	}
	type storedCI struct {
		linkID, itemID      int
		state, url, headSHA sql.NullString
	}
	var stored []storedCI
	for rows.Next() {
		var row storedCI
		if err := rows.Scan(&row.linkID, &row.itemID, &row.state, &row.url, &row.headSHA); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan PR link CI status: %w", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate PR links for CI status: %w", err)
	}
	_ = rows.Close()

	changedItems := make(map[int]bool)
	for _, row := range stored {
		displayChanged := row.state.String != state || row.url.String != ciURL
		if !displayChanged && row.headSHA.String == headSHA {
			continue
		}
		if displayChanged {
			_, err = s.db.ExecWriteContext(ctx, `
				UPDATE item_scm_links SET ci_state = ?, ci_url = ?, ci_head_sha = ?, ci_updated_at = CURRENT_TIMESTAMP
				WHERE id = ?
			`, nullIfEmpty(state), nullIfEmpty(ciURL), headSHA, row.linkID)
			changedItems[row.itemID] = true
		} else {
			_, err = s.db.ExecWriteContext(ctx, `UPDATE item_scm_links SET ci_head_sha = ? WHERE id = ?`, headSHA, row.linkID)
		}
		if err != nil {
			return fmt.Errorf("store CI status on PR link %d: %w", row.linkID, err)
		}
	}
	for itemID := range changedItems {
		services.PublishItemChange(itemID, services.ItemChangeLink)
	}
	return nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// RefreshPullRequestCIForCommit re-reads the CI status of the repository's
// pull requests whose head is sha; an empty sha means every open pull request.
// A CI webhook delivery schedules it. Links already read for sha are answered
// directly; other open pull requests are fetched to learn their head, because
// a delivery can arrive before the sync has seen the push that triggered it.
// It writes only CI status and never changes link state or emits events.
func (s *SyncService) RefreshPullRequestCIForCommit(ctx context.Context, repoID int, sha string) error {
	var repositoryName string
	var connectionID int
	err := s.db.QueryRowContext(ctx, `
		SELECT repository_name, workspace_scm_connection_id FROM workspace_repositories WHERE id = ?
	`, repoID).Scan(&repositoryName, &connectionID)
	if err != nil {
		return fmt.Errorf("failed to get repository info: %w", err)
	}
	owner, repo, ok := utils.SplitRepositoryPath(repositoryName)
	if !ok {
		return fmt.Errorf("invalid repository name format: %s", repositoryName)
	}
	provider, err := s.resolveProvider(ctx, connectionID)
	if err != nil {
		return err
	}
	ciProvider, ok := provider.(CIStatusProvider)
	if !ok {
		return nil
	}

	// Open pull requests, plus any link already read for sha (a pull request
	// merged while its CI was still running).
	query := `
		SELECT external_id, external_url, ci_head_sha FROM item_scm_links
		WHERE workspace_repository_id = ? AND link_type = ?
		  AND (state IS NULL OR state = ?`
	args := []any{repoID, models.SCMLinkTypePullRequest, models.SCMLinkStateOpen}
	if sha != "" {
		query += ` OR ci_head_sha = ?`
		args = append(args, sha)
	}
	rows, err := s.db.QueryContext(ctx, query+`)
		ORDER BY id`, args...)
	if err != nil {
		return fmt.Errorf("query PR links for CI refresh: %w", err)
	}
	type candidate struct {
		externalID, externalURL string
		knownHead               bool
	}
	var candidates []*candidate
	byExternalID := make(map[string]*candidate)
	for rows.Next() {
		var externalID string
		var externalURL, headSHA sql.NullString
		if err := rows.Scan(&externalID, &externalURL, &headSHA); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan PR link for CI refresh: %w", err)
		}
		c := byExternalID[externalID]
		if c == nil {
			c = &candidate{externalID: externalID, externalURL: externalURL.String}
			byExternalID[externalID] = c
			candidates = append(candidates, c)
		}
		if sha != "" && headSHA.String == sha {
			c.knownHead = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate PR links for CI refresh: %w", err)
	}
	_ = rows.Close()

	var refreshErrs []error
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		head := sha
		if !c.knownHead {
			prNumber, _ := strconv.Atoi(c.externalID)
			if urlNumber := prNumberFromURL(c.externalURL); urlNumber > 0 {
				prNumber = urlNumber
			}
			pr, err := provider.GetPullRequest(ctx, owner, repo, prNumber)
			if err != nil {
				refreshErrs = append(refreshErrs, fmt.Errorf("PR #%d: %w", prNumber, err))
				continue
			}
			if pr.HeadSHA == "" || (sha != "" && pr.HeadSHA != sha) {
				continue
			}
			head = pr.HeadSHA
		}
		if err := s.updatePullRequestCI(ctx, ciProvider, owner, repo, repoID, c.externalID, head); err != nil {
			refreshErrs = append(refreshErrs, fmt.Errorf("PR %s: %w", c.externalID, err))
		}
	}
	return errors.Join(refreshErrs...)
}
