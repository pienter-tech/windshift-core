package repository

import (
	"path/filepath"
	"reflect"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

func TestListItemSCMLinkSummaries(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "scm-link-summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatal(err)
	}

	insert := func(query string, args ...any) int {
		t.Helper()
		result, err := db.Exec(query, args...)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return int(id)
	}
	workspaceID := insert(`INSERT INTO workspaces (name, key) VALUES ('Catch up', 'CMU')`)
	itemID := insert(`INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, 1, 'Fix importer', 'a0')`, workspaceID)
	otherItemID := insert(`INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, 2, 'Other', 'a1')`, workspaceID)
	providerID := insert(`INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled) VALUES ('forgejo', 'Forgejo', 'gitea', 'pat', true)`)
	connectionID := insert(`INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id) VALUES (?, ?)`, workspaceID, providerID)
	repoID := insert(`INSERT INTO workspace_repositories (workspace_scm_connection_id, repository_external_id, repository_name, repository_url) VALUES (?, '1', 'pienter/app', 'https://git.example/pienter/app')`, connectionID)

	link := func(item int, linkType, externalID string, title, state any) {
		t.Helper()
		insert(`INSERT INTO item_scm_links (item_id, workspace_repository_id, link_type, external_id, title, state) VALUES (?, ?, ?, ?, ?, ?)`,
			item, repoID, linkType, externalID, title, state)
	}
	link(itemID, "pull_request", "12", "CMU-1 Fix importer", "open")
	link(itemID, "pull_request", "13", nil, nil)
	link(itemID, "branch", "feature/CMU-1-fix-importer", nil, nil)
	link(itemID, "commit", "0123456789abcdef0123456789abcdef01234567", "CMU-1 Parse empty rows", nil)
	link(otherItemID, "branch", "feature/CMU-2-other", nil, nil)

	got, err := NewSCMWorkspaceRepository(db).ListItemSCMLinkSummaries(itemID)
	if err != nil {
		t.Fatal(err)
	}
	want := []ItemSCMLinkSummary{
		{LinkType: models.SCMLinkTypePullRequest, ExternalID: "12", Title: "CMU-1 Fix importer", State: "open"},
		{LinkType: models.SCMLinkTypePullRequest, ExternalID: "13"},
		{LinkType: models.SCMLinkTypeBranch, ExternalID: "feature/CMU-1-fix-importer"},
		{LinkType: models.SCMLinkTypeCommit, ExternalID: "0123456789abcdef0123456789abcdef01234567", Title: "CMU-1 Parse empty rows"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected summaries:\n got %+v\nwant %+v", got, want)
	}
}

func TestListItemSCMLinks(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "scm-links.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatal(err)
	}

	insert := func(query string, args ...any) int {
		t.Helper()
		result, err := db.Exec(query, args...)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return int(id)
	}
	workspaceID := insert(`INSERT INTO workspaces (name, key) VALUES ('Autopilot', 'AP')`)
	itemID := insert(`INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, 1, 'Find PR', 'a0')`, workspaceID)
	otherItemID := insert(`INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, 2, 'Other', 'a1')`, workspaceID)
	providerID := insert(`INSERT INTO scm_providers (slug, name, provider_type, auth_method, enabled) VALUES ('forgejo', 'Forgejo', 'gitea', 'pat', true)`)
	connectionID := insert(`INSERT INTO workspace_scm_connections (workspace_id, scm_provider_id) VALUES (?, ?)`, workspaceID, providerID)
	repoID := insert(`INSERT INTO workspace_repositories (workspace_scm_connection_id, repository_external_id, repository_name, repository_url) VALUES (?, '1', 'pienter/app', 'https://git.example/pienter/app')`, connectionID)

	branchID := insert(`INSERT INTO item_scm_links (item_id, workspace_repository_id, link_type, external_id, created_at, updated_at)
		VALUES (?, ?, 'branch', 'feature/AP-1-find-pr', '2026-10-01 10:00:00', '2026-10-01 10:00:00')`, itemID, repoID)
	prID := insert(`INSERT INTO item_scm_links (item_id, workspace_repository_id, link_type, external_id, external_url, title, state, author_name, detection_source, ci_state, ci_url, is_mention, created_at, updated_at)
		VALUES (?, ?, 'pull_request', '42', 'https://git.example/pienter/app/pulls/42', 'AP-1 Find PR', 'open', 'korneel', 'branch_name', 'success', 'https://ci.example/runs/7', false, '2026-10-02 10:00:00', '2026-10-02 11:00:00')`, itemID, repoID)
	insert(`INSERT INTO item_scm_links (item_id, workspace_repository_id, link_type, external_id) VALUES (?, ?, 'branch', 'feature/AP-2-other')`, otherItemID, repoID)

	got, err := NewSCMWorkspaceRepository(db).ListItemSCMLinks(itemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d links, want 2: %+v", len(got), got)
	}
	pr, branch := got[0], got[1]
	if pr.ID != prID || branch.ID != branchID {
		t.Fatalf("links not newest first: got ids %d, %d", pr.ID, branch.ID)
	}
	if pr.ItemID != itemID || pr.WorkspaceRepositoryID != repoID || pr.LinkType != "pull_request" || pr.ExternalID != "42" ||
		pr.ExternalURL != "https://git.example/pienter/app/pulls/42" || pr.Title != "AP-1 Find PR" || pr.State != "open" ||
		pr.AuthorName != "korneel" || pr.DetectionSource != "branch_name" || pr.CIState != "success" ||
		pr.CIURL != "https://ci.example/runs/7" || pr.IsMention ||
		pr.RepositoryName != "pienter/app" || pr.RepositoryURL != "https://git.example/pienter/app" ||
		pr.ProviderType != "gitea" || pr.AuthMethod != "pat" {
		t.Fatalf("unexpected pull request link: %+v", pr)
	}
	if branch.ExternalID != "feature/AP-1-find-pr" || branch.ExternalURL != "" || branch.Title != "" || branch.State != "" || branch.CIState != "" {
		t.Fatalf("unexpected branch link: %+v", branch)
	}

	empty, err := NewSCMWorkspaceRepository(db).ListItemSCMLinks(otherItemID + 100)
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("want an empty non-nil slice, got %#v", empty)
	}
}
