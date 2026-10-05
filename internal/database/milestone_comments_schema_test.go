package database

import (
	"path/filepath"
	"strings"
	"testing"
)

const milestoneCommentsMigrationVersion = "20261005_milestone_comments"

func milestoneCommentsTableCount(t *testing.T, db Database) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='milestone_comments'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestMilestoneCommentsSchemaFreshInstallAndUpgrade(t *testing.T) {
	db, err := NewSQLiteDB(filepath.Join(t.TempDir(), "windshift-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize SQLite schema: %v", err)
	}
	if milestoneCommentsTableCount(t, db) != 1 {
		t.Fatal("expected milestone_comments on a fresh install")
	}
	var stamped int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=?", milestoneCommentsMigrationVersion).Scan(&stamped); err != nil {
		t.Fatal(err)
	}
	if stamped != 1 {
		t.Fatalf("migration %s was not stamped on a fresh install", milestoneCommentsMigrationVersion)
	}

	// Simulate an existing install from before the migration: no table and
	// no stamp. The catalog must create the table on the next start.
	if _, err := db.Exec(`
		DROP TABLE milestone_comments;
		DELETE FROM schema_migrations WHERE version = '` + milestoneCommentsMigrationVersion + `';
	`); err != nil {
		t.Fatal(err)
	}
	if err := runPendingMigrations(db, Catalog); err != nil {
		t.Fatalf("run milestone comments migration: %v", err)
	}
	if milestoneCommentsTableCount(t, db) != 1 {
		t.Fatal("expected the migration to create milestone_comments")
	}
	for _, index := range []string{"idx_milestone_comments_milestone", "idx_milestone_comments_author"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("expected index %s after migration", index)
		}
	}
}

func TestMilestoneCommentsMigrationBackendParity(t *testing.T) {
	var migration *Migration
	for i := range Catalog {
		if Catalog[i].Version == milestoneCommentsMigrationVersion {
			migration = &Catalog[i]
		}
	}
	if migration == nil {
		t.Fatalf("migration %s is missing from the catalog", milestoneCommentsMigrationVersion)
	}
	for name, body := range map[string]string{"sqlite": migration.SQLite, "postgres": migration.Postgres} {
		for _, fragment := range []string{"CREATE TABLE milestone_comments", "REFERENCES milestones(id) ON DELETE CASCADE", "REFERENCES users(id) ON DELETE CASCADE", "idx_milestone_comments_milestone"} {
			if !strings.Contains(body, fragment) {
				t.Fatalf("%s migration body is missing %q", name, fragment)
			}
		}
	}
	if !strings.Contains(milestoneCommentsSchemaPostgres, "milestone_comments") || !strings.Contains(milestoneCommentsSchema, "milestone_comments") {
		t.Fatal("expected milestone_comments in both canonical schema files")
	}
}
