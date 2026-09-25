package linkstatus_test

import (
	"context"
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
	"dashboard/internal/repos/data"
	"dashboard/internal/services/linkstatus"
)

func TestCheckBarsAndDownDays(t *testing.T) {
	crypto.Init("test-master-key")
	d, err := db.Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()

	sp := &model.Space{Kind: enums.SpacePersonal, Name: "x", Version: 1}
	if err := content.AddSpace(d, sp); err != nil {
		t.Fatal(err)
	}
	add := func(key, url string) int64 {
		w := &model.Widget{SpaceID: sp.ID, Key: key, Type: "link", Title: key, Version: 1, UpdatedAt: time.Now().UTC(),
			Config: map[string]any{"url": url, "status": "http"}}
		if err := content.AddWidget(d, w); err != nil {
			t.Fatal(err)
		}
		return w.ID
	}
	good, dead := add("good", up.URL), add("dead", "http://127.0.0.1:1")

	if err := linkstatus.Check(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC()
	bars, ok := linkstatus.Bars(d, good, today)
	if !ok || len(bars.Bars) != 30 || bars.Bars[29].State != linkstatus.BarUp || bars.Share != 1 {
		t.Fatalf("good: %+v", bars)
	}

	// Three earlier days without a single success, then today.
	for i := 1; i <= 3; i++ {
		if err := data.RecordStatus(d, dead, today.AddDate(0, 0, -i).Format("2006-01-02"), false, 0); err != nil {
			t.Fatal(err)
		}
	}
	if n := linkstatus.DownDays(d, dead, today); n != 4 {
		t.Fatalf("down days: %d", n)
	}
	if n := linkstatus.DownDays(d, good, today); n != 0 {
		t.Fatalf("good down days: %d", n)
	}
}
