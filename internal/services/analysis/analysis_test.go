package analysis_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/db/dbtest"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	data "dashboard/internal/repos/data"
	"dashboard/internal/services/analysis"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	crypto.Init("test-master-key")
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), dbtest.Key)
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

// TestLinkTilesReachCrossRules: link tiles of the space are compared with
// the space's Uptime Kuma monitors.
func TestLinkTilesReachCrossRules(t *testing.T) {
	d := openTestDB(t)
	sid := addSpace(t, d)
	conn := &model.Connection{SpaceID: sid, Key: "kuma", Name: "Kuma", Service: "uptimekuma", URL: "demo://uptimekuma",
		CredentialMode: enums.CredentialShared, VerifyTLS: true, CreatedAt: time.Now().UTC()}
	if err := content.AddConnection(d, conn); err != nil {
		t.Fatal(err)
	}
	tile := &model.Widget{SpaceID: sid, Key: "wiki", Type: "link", Title: "Wiki", Config: map[string]any{"url": "https://wiki.lan"},
		Version: 1, UpdatedAt: time.Now().UTC()}
	if err := content.AddWidget(d, tile); err != nil {
		t.Fatal(err)
	}

	if _, err := analysis.RunAll(context.Background(), d, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var count int
	d.QueryRow("SELECT COUNT(*) FROM hints WHERE rule = 'kuma.unmonitored' AND resolved_at IS NULL").Scan(&count)
	if count != 1 {
		t.Fatalf("expected one kuma.unmonitored hint, got %d", count)
	}
}

// Two connections failing on one host give one outage hint instead of two
// connector hints.
func TestRunAllBundlesOutage(t *testing.T) {
	d := openTestDB(t)
	sid := addSpace(t, d)
	for _, svc := range []string{"kimai", "gitea"} {
		conn := &model.Connection{
			SpaceID: sid, Key: svc, Name: svc, Service: svc, URL: "http://127.0.0.1:1",
			CredentialMode: enums.CredentialShared, VerifyTLS: true, CreatedAt: time.Now().UTC(),
			SecretEnc: encryptedSecret(t, "tok"),
		}
		if err := content.AddConnection(d, conn); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := analysis.RunAll(context.Background(), d, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	count := func(rule string) int {
		var n int
		if err := d.QueryRow("SELECT COUNT(*) FROM hints WHERE rule = ? AND resolved_at IS NULL", rule).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("system.outage") != 1 || count("system.connector_down") != 0 {
		t.Fatalf("outage %d, connector %d", count("system.outage"), count("system.connector_down"))
	}
}

// TestRunAllRecordsHistory: each run stores today's key figures; a new
// version becomes an update event.
func TestRunAllRecordsHistory(t *testing.T) {
	d := openTestDB(t)
	sid := addSpace(t, d)
	conn := &model.Connection{SpaceID: sid, Key: "immich", Name: "Immich", Service: "immich", URL: "demo://immich",
		CredentialMode: enums.CredentialShared, VerifyTLS: true, CreatedAt: time.Now().UTC()}
	if err := content.AddConnection(d, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := analysis.RunAll(context.Background(), d, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	series, err := data.SamplesSince(d, sid, 0, "2000-01-01")
	if err != nil || len(series["immich.items"]) != 1 {
		t.Fatalf("samples: %v %v", series, err)
	}

	// A different stored version means the service was updated since.
	if err := data.SetVersion(d, sid, 0, "Immich", "v0.9", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := analysis.RunAll(context.Background(), d, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	events, err := data.EventsSince(d, []int64{sid}, 0, time.Now().Add(-time.Hour), 10)
	if err != nil || len(events) != 1 || events[0].Subject != "Immich" || events[0].Kind != "update" {
		t.Fatalf("events: %+v %v", events, err)
	}
}

// TestRunAllFetchesInParallel: connections are fetched side by side, so
// the first run at start fills the cache in the time of one service.
func TestRunAllFetchesInParallel(t *testing.T) {
	const hold = 100 * time.Millisecond
	var mu sync.Mutex
	inflight, peak := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inflight++
		peak = max(peak, inflight)
		mu.Unlock()

		time.Sleep(hold)

		mu.Lock()
		inflight--
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	d := openTestDB(t)
	sid := addSpace(t, d)
	for _, key := range []string{"kimai-a", "kimai-b"} {
		conn := &model.Connection{
			SpaceID: sid, Key: key, Name: key, Service: "kimai", URL: srv.URL,
			CredentialMode: enums.CredentialShared, VerifyTLS: true, CreatedAt: time.Now().UTC(),
			SecretEnc: encryptedSecret(t, "tok"),
		}
		if err := content.AddConnection(d, conn); err != nil {
			t.Fatalf("add connection: %v", err)
		}
	}

	if _, err := analysis.RunAll(context.Background(), d, time.Now().UTC()); err != nil {
		t.Fatalf("run all: %v", err)
	}
	if peak < 2 {
		t.Fatalf("peak concurrent fetches = %d, want 2", peak)
	}
}

// Daily key figures older than the longest trend span are dropped.
func TestPrunePointsKeepsTrendSpan(t *testing.T) {
	d := openTestDB(t)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	old, kept := now.AddDate(-3, 0, 0).Format(time.DateOnly), now.AddDate(-1, 0, 0).Format(time.DateOnly)
	for _, day := range []string{old, kept} {
		if err := data.PutPoint(d, "1:0", "open_amount", day, 1); err != nil {
			t.Fatal(err)
		}
	}

	if err := analysis.PrunePoints(d, now); err != nil {
		t.Fatal(err)
	}
	points, err := data.Points(d, "1:0", "open_amount", "2000-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Day != kept {
		t.Fatalf("points: %+v", points)
	}
}
