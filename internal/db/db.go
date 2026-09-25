// Package db opens the SQLite database and applies migrations.
//
// SQLite runs in WAL mode so readers (widget fragments) never block the
// single writer (scheduler, editor). The driver is modernc.org/sqlite, a
// pure-Go implementation with no cgo, so the binary cross-compiles to
// arm64 (Raspberry Pi) without a C toolchain.
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const busyTimeoutMS = 5000

// Open opens (and creates if needed) the sqlite database at path, sets the
// required pragmas and applies any pending migrations.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=foreign_keys(on)", path, busyTimeoutMS)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A single writer avoids SQLITE_BUSY under WAL; reads are cheap enough
	// at this scale (1-10 users) to serialize through one connection too.
	sqlDB.SetMaxOpenConns(1)

	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, err
	}
	if err := migrate(sqlDB); err != nil {
		return nil, err
	}
	return sqlDB, nil
}

func migrate(d *sql.DB) error {
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}

	names, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	list := make([]string, 0, len(names))
	for _, n := range names {
		list = append(list, n.Name())
	}
	sort.Strings(list)

	for _, name := range list {
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		var applied int
		row := d.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE name = ?", name)
		if err := row.Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}

		sqlBytes, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := d.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations (name) VALUES (?)", name); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
