package db

// Backup copies for the self-backup job:
//
//	Snapshot      VACUUM INTO: a consistent copy while the app keeps running
//	              (encrypted under the live database's key)
//	Rekey         the same copy under a new key, swapped in at next Open
//	OpenReadOnly  opens a copy without migrating it, for the test restore
//	Integrity     PRAGMA integrity_check ("ok" when sound)
//	Migrations    number of applied migrations (copy and live must match)
//	Count         rows of one known table

import (
	"database/sql"
	"fmt"
)

// integrityOK is SQLite's answer for a sound database.
const integrityOK = "ok"

// Snapshot writes a consistent copy of the database to path.
func Snapshot(d *sql.DB, path string) error {
	key := keyOf(d)
	if key == nil {
		return ErrKey
	}
	_, err := d.Exec("VACUUM INTO ?", fileURI(path, key))
	return err
}

// Rekey writes a copy of the database at path under newKey. Open swaps
// it in once the process runs with the new key; writes after Rekey are
// not in the copy, so restart right away.
func Rekey(d *sql.DB, path string, newKey []byte) error {
	if len(newKey) != keyLen {
		return ErrKey
	}
	next := path + rekeyedSuffix
	if err := removeIfExists(next); err != nil {
		return err
	}
	_, err := d.Exec("VACUUM INTO ?", fileURI(next, newKey))
	return err
}

// OpenReadOnly opens a Snapshot of live without writing to it.
func OpenReadOnly(live *sql.DB, path string) (*sql.DB, error) {
	key := keyOf(live)
	if key == nil {
		return nil, ErrKey
	}
	return sql.Open(driverName, dsn(path, key, "&mode=ro"))
}

func keyOf(d *sql.DB) []byte {
	keysMu.Lock()
	defer keysMu.Unlock()
	return keys[d]
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
