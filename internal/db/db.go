// Package db opens the SQLite database and applies migrations.
//
//	master key ──HKDF("database")──► file key
//	                                    │
//	andon.db, -wal, backups ◄── Adiantum VFS (4 KiB blocks, encrypted)
//
// SQLite runs in WAL mode: readers never wait for the writer. Write
// transactions begin IMMEDIATE, so writers queue on the lock (busy
// timeout) instead of failing on a late lock upgrade; WithRead opens a
// read-only transaction next to them. The driver is ncruces/go-sqlite3
// (SQLite compiled to WebAssembly, no cgo), so the binary still
// cross-compiles to arm64 (Raspberry Pi); Adiantum needs no AES
// instructions, which the Pi 4 lacks.
package db

import (
	"bytes"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/vfs/adiantum"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const (
	driverName    = "sqlite3"
	busyTimeoutMS = 5000
	// maxConns bounds the pool: one writer at a time plus concurrent
	// readers (widget fragments load in parallel).
	maxConns = 4
	keyLen   = 32
)

// plainHeader starts every unencrypted SQLite file.
var plainHeader = []byte("SQLite format 3\x00")

// rekeyedSuffix marks a copy under a new key, swapped in at the next Open.
const rekeyedSuffix = ".rekeyed"

// ErrKey means the key is missing or does not open the file.
var ErrKey = errors.New("db: missing or wrong database key")

var (
	keysMu sync.Mutex
	keys   = map[*sql.DB][]byte{}
)

// Open opens (and creates if needed) the encrypted sqlite database at
// path, sets the required pragmas and applies any pending migrations. A
// plaintext database from an older version is encrypted in place first.
func Open(path string, key []byte) (*sql.DB, error) {
	if len(key) != keyLen {
		return nil, ErrKey
	}
	if err := swapRekeyed(path, key); err != nil {
		return nil, err
	}
	if err := encryptPlain(path, key); err != nil {
		return nil, fmt.Errorf("db: encrypt: %w", err)
	}

	sqlDB, err := sql.Open(driverName, dsn(path, key, "&_txlock=immediate"))
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(maxConns)

	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		sqlDB.Close()
		return nil, keyErr(err)
	}
	if err := migrate(sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}

	keysMu.Lock()
	keys[sqlDB] = key
	keysMu.Unlock()
	return sqlDB, nil
}

// dsn is the encrypted file URI plus the per-connection pragmas.
func dsn(path string, key []byte, extra string) string {
	return fmt.Sprintf("%s&_pragma=busy_timeout(%d)&_pragma=foreign_keys(on)&_pragma=temp_store(memory)%s",
		fileURI(path, key), busyTimeoutMS, extra)
}

// fileURI names path under the Adiantum VFS, e.g. for VACUUM INTO.
func fileURI(path string, key []byte) string {
	return fmt.Sprintf("file:%s?vfs=adiantum&hexkey=%s", url.PathEscape(path), hex.EncodeToString(key))
}

// keyErr turns SQLite's "not a database" (wrong key) into ErrKey.
func keyErr(err error) error {
	if strings.Contains(err.Error(), "not a database") {
		return fmt.Errorf("%w: %v", ErrKey, err)
	}
	return err
}

// encryptPlain rewrites an unencrypted database under key. VACUUM INTO
// reads the WAL too, so the copy is complete; the old WAL goes with it.
func encryptPlain(path string, key []byte) error {
	head := make([]byte, len(plainHeader))
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = f.Read(head)
	f.Close()
	if err != nil || !bytes.Equal(head, plainHeader) {
		return nil
	}

	plain, err := sql.Open(driverName, "file:"+url.PathEscape(path))
	if err != nil {
		return err
	}
	tmp := path + ".encrypting"
	if err := removeIfExists(tmp); err != nil {
		plain.Close()
		return err
	}
	_, err = plain.Exec("VACUUM INTO ?", fileURI(tmp, key))
	plain.Close()
	if err != nil {
		return err
	}
	return replaceFile(tmp, path)
}

// swapRekeyed puts a copy written by Rekey in place once it opens with
// the current key (the operator restarted with the new master key).
func swapRekeyed(path string, key []byte) error {
	next := path + rekeyedSuffix
	if _, err := os.Stat(next); err != nil {
		return nil
	}
	probe, err := sql.Open(driverName, dsn(next, key, "&mode=ro"))
	if err != nil {
		return err
	}
	_, err = probe.Exec("SELECT COUNT(*) FROM sqlite_master")
	probe.Close()
	if err != nil {
		return nil // still the old key: keep using the current file
	}
	return replaceFile(next, path)
}

// replaceFile moves src over dst and drops dst's stale WAL files.
func replaceFile(src, dst string) error {
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := removeIfExists(dst + suffix); err != nil {
			return err
		}
	}
	return os.Rename(src, dst)
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
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
