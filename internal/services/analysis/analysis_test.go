package analysis_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	data "dashboard/internal/repos/data"
	"dashboard/internal/services/analysis"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	crypto.Init("test-master-key")
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func encryptedSecret(t *testing.T, secret string) []byte {
	t.Helper()
	enc, err := crypto.Encrypt(secret, crypto.PurposeCredential, nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return enc
}

func addSpace(t *testing.T, q db.Queryer) int64 {
	t.Helper()
	sp := &model.Space{Kind: enums.SpacePersonal, Name: "x", Version: 1}
	if err := content.AddSpace(q, sp); err != nil {
		t.Fatalf("add space: %v", err)
	}
	return sp.ID
}

func TestRunAllReportsConnectorDown(t *testing.T) {
	d := openTestDB(t)
	sid := addSpace(t, d)
	conn := &model.Connection{
		SpaceID: sid, Key: "kimai", Name: "Kimai", Service: "kimai", URL: "http://127.0.0.1:1",
		CredentialMode: enums.CredentialShared, VerifyTLS: true, CreatedAt: time.Now().UTC(),
		SecretEnc: encryptedSecret(t, "tok"),
	}
	if err := content.AddConnection(d, conn); err != nil {
		t.Fatalf("add connection: %v", err)
	}

	fresh, err := analysis.RunAll(context.Background(), d, time.Now().UTC())
	if err != nil {
		t.Fatalf("run all: %v", err)
	}
	if fresh == 0 {
		t.Fatal("expected at least the connector_down hint to be fresh")
	}

	var count int
	if err := d.QueryRow(
		"SELECT COUNT(*) FROM hints WHERE rule = 'system.connector_down' AND resolved_at IS NULL",
	).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 open connector_down hint, got %d", count)
	}
}

func TestRunAllProducesKimaiHints(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/timesheets", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total-Pages", "1")
		w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	mux.HandleFunc("/api/customers", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	mux.HandleFunc("/api/timesheets/active", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	mux.HandleFunc("/api/holiday/absences", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := openTestDB(t)
	sid := addSpace(t, d)
	conn := &model.Connection{
		SpaceID: sid, Key: "kimai", Name: "Kimai", Service: "kimai", URL: srv.URL,
		CredentialMode: enums.CredentialShared, VerifyTLS: true, CreatedAt: time.Now().UTC(),
		SecretEnc: encryptedSecret(t, "tok"),
	}
	if err := content.AddConnection(d, conn); err != nil {
		t.Fatalf("add connection: %v", err)
	}

	fresh, err := analysis.RunAll(context.Background(), d, time.Now().UTC())
	if err != nil {
		t.Fatalf("run all: %v", err)
	}
	// With zero timesheets, kimai.missing_day fires for every recent
	// workday: definitely at least one fresh hint, and no connector_down.
	if fresh == 0 {
		t.Fatal("expected fresh hints from kimai.missing_day")
	}
	var down int
	d.QueryRow("SELECT COUNT(*) FROM hints WHERE rule = 'system.connector_down' AND resolved_at IS NULL").Scan(&down)
	if down != 0 {
		t.Fatalf("expected no connector_down hint for a working connection, got %d", down)
	}

	// Running again must not duplicate the same hints (fingerprint reuse).
	var totalOpen int
	d.QueryRow("SELECT COUNT(*) FROM hints WHERE resolved_at IS NULL").Scan(&totalOpen)
	if _, err := analysis.RunAll(context.Background(), d, time.Now().UTC()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var totalOpen2 int
	d.QueryRow("SELECT COUNT(*) FROM hints WHERE resolved_at IS NULL").Scan(&totalOpen2)
	if totalOpen2 != totalOpen {
		t.Fatalf("expected stable hint count across runs, got %d then %d", totalOpen, totalOpen2)
	}

	// The trend widget reads these back via repos/data.Points.
	today := time.Now().UTC()
	points, err := data.Points(d, "1:0", "month_min", today.AddDate(0, 0, -1).Format("2006-01-02"))
	if err != nil {
		t.Fatalf("points: %v", err)
	}
	if len(points) != 1 || points[0].Value != 0 {
		t.Fatalf("expected one month_min snapshot of 0, got %+v", points)
	}
}
