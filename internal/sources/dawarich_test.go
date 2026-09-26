package sources_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"andon/internal/sources"
)

func TestDawarichDataNormalizesVisitsAndAreas(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/points", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"timestamp": 1750000000}]`))
	})
	mux.HandleFunc("/api/v1/areas", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id": 1, "name": "Kunde A", "latitude": 52.5, "longitude": 13.4, "radius": 100}]`))
	})
	mux.HandleFunc("/api/v1/visits", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{
			"id": 1, "started_at": "2026-01-05T09:00:00Z", "ended_at": "2026-01-05T11:00:00Z",
			"duration": 120, "area_id": 1, "place": {"name": "Büro", "latitude": 52.5, "longitude": 13.4}
		}]`))
	})
	mux.HandleFunc("/api/v1/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"total_distance": 42}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := sources.DawarichData{}
	out, err := src.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "tok", VerifyTLS: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	data := out.(*sources.DawarichDataset)

	if len(data.Areas) != 1 || data.Areas[0].Name != "Kunde A" {
		t.Fatalf("unexpected areas: %+v", data.Areas)
	}
	if len(data.Visits) != 1 {
		t.Fatalf("expected 1 visit, got %d", len(data.Visits))
	}
	v := data.Visits[0]
	if v.Minutes != 120 || v.Name != "Büro" || v.Lat == nil || *v.Lat != 52.5 {
		t.Fatalf("unexpected visit: %+v", v)
	}
	if data.LastPoint == "" {
		t.Fatal("expected last point to be set from the timestamp")
	}
	if data.Stats["total_distance"] != float64(42) {
		t.Fatalf("expected stats passthrough, got %+v", data.Stats)
	}
}

func TestDawarichTestReadsVersionHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Dawarich-Version", "0.24.1")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	src := sources.DawarichTest{}
	out, err := src.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "tok", VerifyTLS: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if m := out.(map[string]any); m["version"] != "0.24.1" {
		t.Fatalf("expected version, got %+v", m)
	}
}
