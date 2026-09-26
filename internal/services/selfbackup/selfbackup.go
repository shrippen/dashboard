// Package selfbackup copies Andon's own database every day and
// proves each copy restorable:
//
//	VACUUM INTO backups/andon-20260925-030000.db
//	open copy read-only ─► integrity ok, same migrations, rows, secrets decrypt
//	keep the newest Keep copies
package selfbackup

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"andon/internal/crypto"
	"andon/internal/db"
	"andon/internal/repos/content"
	"andon/internal/repos/misc"
	"andon/internal/services/access"
)

const (
	// Keep is how many copies stay on disk.
	Keep        = 7
	JobName     = "selfbackup"
	Interval    = 24 * time.Hour
	filePrefix  = "andon-"
	fileExt     = ".db"
	stampLayout = "20060102-150405"
	statusKey   = "selfbackup"
	dirMode     = 0o750
)

// Problems a test restore can find (catalog keys).
const (
	problemIntegrity  = "selfbackup.integrity"
	problemMigrations = "selfbackup.migrations"
	problemEmpty      = "selfbackup.empty"
	problemSecrets    = "selfbackup.secrets"
)

// ErrDenied means only admins may start a backup.
var ErrDenied = errors.New("error.denied")

// counted are the tables the restore test reports.
var counted = []string{"users", "spaces", "connections", "widgets", "boards", "hints"}

// Status is the outcome of the last run.
type Status struct {
	At      time.Time
	File    string
	Size    int64
	OK      bool
	Problem string // catalog key or error text, "" when OK
	Rows    map[string]int
	Secrets int // stored secrets that decrypt in the copy
}

// File is one copy on disk.
type File struct {
	Name string
	Size int64
	At   time.Time
}

const bytesPerMB = 1 << 20

// MB is the file size in mebibytes, for display.
func (f File) MB() float64 { return float64(f.Size) / bytesPerMB }

// Run writes a copy to dir, test-restores it and prunes old copies.
func Run(d *sql.DB, dir string, now time.Time) (Status, error) {
	status := Status{At: now.UTC(), Rows: map[string]int{}}
	path, err := snapshot(d, dir, now)
	if err != nil {
		status.Problem = err.Error()
		return status, save(d, status)
	}
	status.File = filepath.Base(path)
	if info, err := os.Stat(path); err == nil {
		status.Size = info.Size()
	}

	status.Problem = verify(d, path, &status)
	status.OK = status.Problem == ""
	if !status.OK {
		// A copy that fails the test must not push a good one out.
		_ = os.Remove(path)
	} else if err := prune(dir); err != nil {
		return status, err
	}
	return status, save(d, status)
}

// RunNow starts a backup on an admin's request.
func RunNow(d *sql.DB, who *access.Principal, dir string) (Status, error) {
	if !who.IsAdmin() {
		return Status{}, ErrDenied
	}
	return Run(d, dir, time.Now())
}

func snapshot(d *sql.DB, dir string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return "", err
	}
	path := filepath.Join(dir, filePrefix+now.UTC().Format(stampLayout)+fileExt)
	return path, db.Snapshot(d, path)
}

// verify opens the copy like a restore would and returns the first
// problem, "" if none.
func verify(live *sql.DB, path string, status *Status) string {
	copyDB, err := db.OpenReadOnly(live, path)
	if err != nil {
		return err.Error()
	}
	defer copyDB.Close()

	if ok, msg, err := db.Integrity(copyDB); err != nil || !ok {
		return problemIntegrity + ": " + msg
	}
	want, err := db.Migrations(live)
	if err != nil {
		return err.Error()
	}
	if got, err := db.Migrations(copyDB); err != nil || got != want {
		return problemMigrations
	}
	for _, table := range counted {
		n, err := db.Count(copyDB, table)
		if err != nil {
			return err.Error()
		}
		status.Rows[table] = n
	}
	if liveUsers, _ := db.Count(live, "users"); liveUsers > 0 && status.Rows["users"] == 0 {
		return problemEmpty
	}

	// Secrets only help if the master key still opens them.
	conns, err := content.AllConnections(copyDB)
	if err != nil {
		return err.Error()
	}
	for _, c := range conns {
		if len(c.SecretEnc) == 0 {
			continue
		}
		if _, err := crypto.Decrypt(c.SecretEnc, crypto.PurposeCredential); err != nil {
			return problemSecrets
		}
		status.Secrets++
	}
	return ""
}

// Files lists the copies, newest first.
func Files(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, filePrefix) || !strings.HasSuffix(name, fileExt) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		at, _ := time.Parse(stampLayout, strings.TrimSuffix(strings.TrimPrefix(name, filePrefix), fileExt))
		out = append(out, File{Name: name, Size: info.Size(), At: at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

func prune(dir string) error {
	files, err := Files(dir)
	if err != nil {
		return err
	}
	for i := Keep; i < len(files); i++ {
		if err := os.Remove(filepath.Join(dir, files[i].Name)); err != nil {
			return err
		}
	}
	return nil
}

func save(d *sql.DB, s Status) error {
	rows := map[string]any{}
	for k, v := range s.Rows {
		rows[k] = v
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		return misc.SetSetting(tx, statusKey, map[string]any{"at": db.TimeStr(s.At), "file": s.File, "size": s.Size,
			"ok": s.OK, "problem": s.Problem, "rows": rows, "secrets": s.Secrets})
	})
}

// Last returns the last run's status; nil before the first run.
func Last(d *sql.DB) (*Status, error) {
	raw, err := misc.Setting(d, statusKey)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	at, _ := db.ParseTime(asString(raw["at"]))
	s := &Status{At: at, File: asString(raw["file"]), Size: int64(asFloat(raw["size"])), Problem: asString(raw["problem"]),
		Secrets: int(asFloat(raw["secrets"])), Rows: map[string]int{}}
	s.OK, _ = raw["ok"].(bool)
	if rows, ok := raw["rows"].(map[string]any); ok {
		for k, v := range rows {
			s.Rows[k] = int(asFloat(v))
		}
	}
	return s, nil
}

func asString(v any) string { s, _ := v.(string); return s }

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}
