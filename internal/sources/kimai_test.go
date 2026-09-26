package sources_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dashboard/internal/sources"
)

func TestKimaiDataNormalizesSheetsAndProjects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/timesheets", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total-Pages", "1")
		w.Write([]byte(`[{
			"id": 1, "begin": "2026-01-05T09:00:00", "end": "2026-01-05T11:00:00",
			"duration": 7200, "rate": 150.0, "billable": true, "exported": false,
			"project": {"id": 10, "customer": {"id": 20, "name": "Acme"}},
			"activity": {"name": "Dev"}, "user": {"id": 5}
		}]`))
	})
	mux.HandleFunc("/api/projects/10", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id": 10, "name": "Website", "customer": {"id": 20}, "budget": 0, "timeBudget": 0}`))
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id": 10}]`))
	})
	mux.HandleFunc("/api/customers", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id": 20, "name": "Acme"}]`))
	})
	mux.HandleFunc("/api/timesheets/active", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/api/holiday/absences", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // plugin not installed
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := sources.KimaiData{}
	out, err := src.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "tok", VerifyTLS: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	data := out.(*sources.KimaiDataset)

	if len(data.Timesheets) != 1 {
		t.Fatalf("expected 1 timesheet, got %d", len(data.Timesheets))
	}
	sheet := data.Timesheets[0]
	if sheet.Minutes != 120 || sheet.CustomerID != 20 || sheet.Activity != "Dev" || !sheet.Billable {
		t.Fatalf("unexpected sheet: %+v", sheet)
	}
	if len(data.Customers) != 1 || data.Customers[0].Name != "Acme" {
		t.Fatalf("unexpected customers: %+v", data.Customers)
	}
	if data.HolidayBundle {
		t.Fatal("expected HolidayBundle false when the plugin 404s")
	}
}

func TestKimaiDataMissingCredential(t *testing.T) {
	src := sources.KimaiData{}
	_, err := src.Fetch(context.Background(), sources.Ctx{URL: "http://example.invalid"})
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
}

func TestKimaiTestReadsVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version": "2.30.0"}`))
	}))
	defer srv.Close()

	src := sources.KimaiTest{}
	out, err := src.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "tok", VerifyTLS: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if m := out.(map[string]any); m["version"] != "2.30.0" {
		t.Fatalf("expected version 2.30.0, got %+v", m)
	}
}

// Kimai writes timestamps with a colon-less offset ("…+0200"); a running
// timer must still know when it began.
func TestKimaiLiveParsesKimaiTimestamps(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/timesheets/active", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id": 7, "begin": "2026-09-26T13:04:00+0200", "project": {"id": 1, "name": "Server"}, "activity": {"id": 2, "name": "Wartung"}}]`))
	})
	mux.HandleFunc("/api/timesheets/recent", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	mux.HandleFunc("/api/timesheets", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total-Pages", "1")
		w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := sources.KimaiLiveSource{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "tok", VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	live := out.(*sources.KimaiLive)
	want := time.Date(2026, 9, 26, 11, 4, 0, 0, time.UTC)
	if len(live.Active) != 1 || !live.Active[0].Begin.Equal(want) {
		t.Fatalf("active: %+v", live.Active)
	}
}

// The week total rounds each entry like the Kimai dataset does (to the
// nearest minute), so Kimai Lite and the week tile show the same sum.
func TestKimaiLiveWeekRoundsLikeDataset(t *testing.T) {
	begin := time.Now().Format("2006-01-02T15:04:05-0700")
	sheet := `{"begin": "` + begin + `", "end": "` + begin + `", "duration": 100}`
	mux := http.NewServeMux()
	mux.HandleFunc("/api/timesheets/active", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	mux.HandleFunc("/api/timesheets/recent", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	mux.HandleFunc("/api/timesheets", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total-Pages", "1")
		w.Write([]byte(`[` + sheet + `,` + sheet + `,` + sheet + `]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := sources.KimaiLiveSource{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "tok", VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	if live := out.(*sources.KimaiLive); live.WeekMin != 6 {
		t.Fatalf("week: %d minutes, want 6", live.WeekMin)
	}
}
