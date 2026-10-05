package database

import (
	"path/filepath"
	"strings"
	"testing"
)

const milestoneActivityMigrationVersion = "20261005_milestone_activity"

func sqliteObjectCount(t *testing.T, db Database, kind, name string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type=? AND name=?", kind, name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestMilestoneActivitySchemaFreshInstallAndUpgrade(t *testing.T) {
	db, err := NewSQLiteDB(filepath.Join(t.TempDir(), "windshift-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize SQLite schema: %v", err)
	}
	if sqliteObjectCount(t, db, "table", "milestone_history") != 1 {
		t.Fatal("expected milestone_history on a fresh install")
	}
	if sqliteObjectCount(t, db, "index", "idx_item_history_milestones") != 1 {
		t.Fatal("expected idx_item_history_milestones on a fresh install")
	}
	var stamped int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=?", milestoneActivityMigrationVersion).Scan(&stamped); err != nil {
		t.Fatal(err)
	}
	if stamped != 1 {
		t.Fatalf("migration %s was not stamped on a fresh install", milestoneActivityMigrationVersion)
	}

	// Simulate an existing install from before the migration: no table, no
	// item_history index, and no stamp. The catalog must create both.
	if _, err := db.Exec(`
		DROP TABLE milestone_history;
		DROP INDEX idx_item_history_milestones;
		DELETE FROM schema_migrations WHERE version = '` + milestoneActivityMigrationVersion + `';
	`); err != nil {
		t.Fatal(err)
	}
	if err := runPendingMigrations(db, Catalog); err != nil {
		t.Fatalf("run milestone activity migration: %v", err)
	}
	if sqliteObjectCount(t, db, "table", "milestone_history") != 1 {
		t.Fatal("expected the migration to create milestone_history")
	}
	for _, index := range []string{"idx_milestone_history_milestone", "idx_milestone_history_user", "idx_item_history_milestones"} {
		if sqliteObjectCount(t, db, "index", index) != 1 {
			t.Fatalf("expected index %s after migration", index)
		}
	}
}

func TestMilestoneActivityMigrationBackendParity(t *testing.T) {
	var migration *Migration
	for i := range Catalog {
		if Catalog[i].Version == milestoneActivityMigrationVersion {
			migration = &Catalog[i]
		}
	}
	if migration == nil {
		t.Fatalf("migration %s is missing from the catalog", milestoneActivityMigrationVersion)
	}
	for name, body := range map[string]string{"sqlite": migration.SQLite, "postgres": migration.Postgres} {
		for _, fragment := range []string{
			"CREATE TABLE milestone_history",
			"REFERENCES milestones(id) ON DELETE CASCADE",
			"REFERENCES users(id) ON DELETE SET NULL",
			"idx_milestone_history_milestone",
			"idx_item_history_milestones",
			"WHERE field_name = 'milestones'",
		} {
			if !strings.Contains(body, fragment) {
				t.Fatalf("%s migration body is missing %q", name, fragment)
			}
		}
	}
	if !strings.Contains(milestoneHistorySchemaPostgres, "milestone_history") || !strings.Contains(milestoneHistorySchema, "milestone_history") {
		t.Fatal("expected milestone_history in both canonical schema files")
	}
	if !strings.Contains(itemsSchema, "idx_item_history_milestones") || !strings.Contains(itemsSchemaPostgres, "idx_item_history_milestones") {
		t.Fatal("expected idx_item_history_milestones in both canonical items schema files")
	}
}
