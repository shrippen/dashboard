package sources_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"andon/internal/sources"
)

// kintsugiStatus is a trimmed /api/status answer of Kintsugi.
const kintsugiStatus = `{"version": 1,
 "open": [{"id": 7, "kind": "acquisition", "title": "Agentur anschreiben", "created_at": "2026-09-20T09:00:00+02:00"}],
 "counts": {"new": 1, "accepted": 2, "rejected": 1, "snoozed": 0, "done": 1},
 "acceptance_rate": 75, "gaps_open": 3,
 "last_run": {"status": "failed", "detail": "LLM nicht erreichbar", "created": 0, "at": "2026-09-26T09:00:00"},
 "research": {"enabled": true, "budget_usd": 5.0, "used_usd": 1.25}}`

func TestKintsugiFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		w.Write([]byte(kintsugiStatus))
	}))
	defer srv.Close()

	out, err := sources.KintsugiData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL + "/", Secret: "tok", VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.KintsugiDataset)
	if len(d.Open) != 1 || d.Open[0].URL != srv.URL+"/vorschlaege#s-7" || d.Open[0].Created.Day() != 20 {
		t.Fatalf("open: %+v", d.Open)
	}
	if d.New != 1 || d.Accepted != 2 || d.Rate != 75 || d.GapsOpen != 3 || !d.Research || d.UsedUSD != 1.25 {
		t.Fatalf("counts: %+v", d)
	}
	if want := time.Date(2026, 9, 20, 7, 0, 0, 0, time.UTC); !d.Open[0].Created.Equal(want) {
		t.Fatalf("created %v, want %v", d.Open[0].Created, want)
	}
	// "at" has no zone (older Kintsugi): read as local time.
	if want := time.Date(2026, 9, 26, 9, 0, 0, 0, time.Local); d.LastRun == nil || !d.LastRun.At.Equal(want) {
		t.Fatalf("run at: %+v", d.LastRun)
	}
	if d.LastRun.Status != sources.KintsugiRunFailed {
		t.Fatalf("run: %+v", d.LastRun)
	}

	if _, err := (sources.KintsugiData{}).Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "wrong", VerifyTLS: true}); err == nil {
		t.Fatal("wrong token accepted")
	}
}
