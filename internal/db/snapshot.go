package db

// Backup copies for the self-backup job:
//
//	Snapshot      VACUUM INTO: a consistent copy while the app keeps running
//	OpenReadOnly  opens a copy without migrating it, for the test restore
//	Integrity     PRAGMA integrity_check ("ok" when sound)
//	Migrations    number of applied migrations (copy and live must match)
//	Count         rows of one known table

import (
	"database/sql"
	"fmt"
	"net/url"
)

// integrityOK is SQLite's answer for a sound database.
const integrityOK = "ok"

// Snapshot writes a consistent copy of the database to path.
func Snapshot(d *sql.DB, path string) error {
	_, err := d.Exec("VACUUM INTO ?", path)
	return err
}

// OpenReadOnly opens a database file without writing to it.
func OpenReadOnly(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(%d)", url.PathEscape(path), busyTimeoutMS)
	return sql.Open("sqlite", dsn)
}

// Integrity reports whether SQLite finds the file sound; the message
// names the first problem otherwise.
func Integrity(q Queryer) (bool, string, error) {
	var msg string
	if err := q.QueryRow("PRAGMA integrity_check").Scan(&msg); err != nil {
		return false, "", err
	}
	return msg == integrityOK, msg, nil
}

// Migrations counts the applied migrations.
func Migrations(q Queryer) (int, error) {
	var n int
	err := q.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&n)
	return n, err
}

// countable are the tables a restore test compares.
var countable = map[string]bool{"users": true, "spaces": true, "connections": true, "widgets": true, "boards": true, "hints": true}

// Count returns the rows of a known table.
func Count(q Queryer, table string) (int, error) {
	if !countable[table] {
		return 0, fmt.Errorf("db: unknown table %q", table)
	}
	var n int
	err := q.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
	return n, err
}
