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
