package db

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchemaAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	d1, err := Open(path, testKey)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var count int
	if err := d1.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='users'",
	).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected users table to exist, got count=%d", count)
	}
	d1.Close()

	// Reopening must not fail or re-apply the migration.
	d2, err := Open(path, testKey)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d2.Close()

	var applied int
	if err := d2.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&applied); err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	files, _ := migrationFiles.ReadDir("migrations")
	if applied != len(files) {
		t.Fatalf("expected %d applied migrations, got %d", len(files), applied)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path, testKey)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	_, err = d.Exec("INSERT INTO teams (id, name, created_at) VALUES (1, 'x', datetime('now'))")
	if err != nil {
		t.Fatalf("insert team: %v", err)
	}
	_, err = d.Exec("INSERT INTO memberships (user_id, team_id, role) VALUES (999, 1, 'viewer')")
	if err == nil {
		t.Fatal("expected foreign key violation for missing user_id")
	}
}
