package db

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

var (
	testKey  = []byte("0123456789abcdef0123456789abcdef")
	otherKey = []byte("fedcba9876543210fedcba9876543210")
)

const secretText = "geheim-umsatz-4711"

// putSecret stores a recognisable value so tests can grep the raw file.
func putSecret(t *testing.T, d *sql.DB) {
	t.Helper()
	if _, err := d.Exec("CREATE TABLE IF NOT EXISTS probe (v TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("INSERT INTO probe VALUES (?)", secretText); err != nil {
		t.Fatal(err)
	}
}

func readsSecret(t *testing.T, d *sql.DB) {
	t.Helper()
	var v string
	if err := d.QueryRow("SELECT v FROM probe").Scan(&v); err != nil || v != secretText {
		t.Fatalf("probe = %q, %v", v, err)
	}
}

// plainOnDisk reports whether any of the database's files leaks text.
func plainOnDisk(t *testing.T, path string) bool {
	t.Helper()
	for _, p := range []string{path, path + "-wal"} {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if bytes.Contains(raw, []byte(secretText)) || bytes.HasPrefix(raw, plainHeader) {
			return true
		}
	}
	return false
}

func TestFilesAreEncrypted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enc.db")
	d, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	putSecret(t, d)

	if plainOnDisk(t, path) {
		t.Fatal("database or WAL readable without key")
	}
	d.Close()

	if _, err := Open(path, otherKey); !errors.Is(err, ErrKey) {
		t.Fatalf("wrong key: err = %v, want ErrKey", err)
	}
}

func TestPlainDatabaseGetsEncrypted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	plain, err := sql.Open(driverName, "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	putSecret(t, plain)
	plain.Close()

	d, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	readsSecret(t, d)
	if plainOnDisk(t, path) {
		t.Fatal("old plaintext still on disk")
	}
}

func TestSnapshotAndRekey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live.db")
	d, err := Open(path, testKey)
	if err != nil {
		t.Fatal(err)
	}
	putSecret(t, d)

	copyPath := filepath.Join(dir, "copy.db")
	if err := Snapshot(d, copyPath); err != nil {
		t.Fatal(err)
	}
	if plainOnDisk(t, copyPath) {
		t.Fatal("snapshot readable without key")
	}
	copyDB, err := OpenReadOnly(d, copyPath)
	if err != nil {
		t.Fatal(err)
	}
	readsSecret(t, copyDB)
	copyDB.Close()

	if err := Rekey(d, path, otherKey); err != nil {
		t.Fatal(err)
	}
	d.Close()

	// The old key still opens the old file until the swap.
	old, err := Open(path, testKey)
	if err != nil {
		t.Fatalf("old key after rekey: %v", err)
	}
	old.Close()

	rekeyed, err := Open(path, otherKey)
	if err != nil {
		t.Fatalf("new key: %v", err)
	}
	defer rekeyed.Close()
	readsSecret(t, rekeyed)
	if _, err := os.Stat(path + rekeyedSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rekeyed copy left behind: %v", err)
	}
}

func TestReadRunsNextToWriter(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "rw.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	putSecret(t, d)

	// A write transaction holds the lock while a reader still gets the
	// last committed state.
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM probe"); err != nil {
		t.Fatal(err)
	}

	err = WithRead(d, func(r *sql.Tx) error {
		readsSecret(t, d)
		_, werr := r.Exec("DELETE FROM probe")
		if werr == nil {
			t.Error("write inside WithRead succeeded")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
