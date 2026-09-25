package sources_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dashboard/internal/sources"
)

func jsonServer(t *testing.T, routes map[string]any, check func(*http.Request) bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range routes {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if check != nil && !check(r) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(body)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestScrutinyReadsSummary(t *testing.T) {
	srv := jsonServer(t, map[string]any{"/api/summary": map[string]any{"data": map[string]any{"summary": map[string]any{
		"0x1": map[string]any{"device": map[string]any{"device_name": "sdb", "model_name": "ST4000", "device_status": 1},
			"smart": map[string]any{"temp": 41, "power_on_hours": 42000, "collector_date": "2026-09-25T03:00:00Z"}},
		"0x2": map[string]any{"device": map[string]any{"device_name": "sda", "device_status": 0}, "smart": map[string]any{}},
	}}}}, nil)

	out, err := sources.ScrutinyData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	disks := out.(*sources.ScrutinyDataset).Disks
	if len(disks) != 2 || disks[1].Name != "sdb" || disks[1].Status != 1 || disks[1].Temp != 41 || disks[1].Seen.IsZero() {
		t.Fatalf("disks: %+v", disks)
	}
}

func TestImmichReadsStorageJobsVersion(t *testing.T) {
	srv := jsonServer(t, map[string]any{
		"/api/server/storage":       map[string]any{"diskUsagePercentage": 87.4, "diskAvailable": "412 GiB"},
		"/api/server/statistics":    map[string]any{"photos": 100, "videos": 5},
		"/api/jobs":                 map[string]any{"faceDetection": map[string]any{"jobCounts": map[string]any{"failed": 3}}, "sidecar": map[string]any{"jobCounts": map[string]any{"failed": 0}}},
		"/api/server/version":       map[string]any{"major": 1, "minor": 131, "patch": 0},
		"/api/server/version-check": map[string]any{"releaseVersion": "v1.132.3"},
	}, func(r *http.Request) bool { return r.Header.Get("x-api-key") == "k" })

	out, err := sources.ImmichData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "k", VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.ImmichDataset)
	if d.Photos != 100 || d.DiskPercent != 87.4 || d.FailedJobs["faceDetection"] != 3 || len(d.FailedJobs) != 1 ||
		d.Version != "v1.131.0" || d.Latest != "v1.132.3" {
		t.Fatalf("data: %+v", d)
	}
}

func TestUmamiLoginAndBothStatShapes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"token": "tok"})
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/api/websites", authed(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "a", "name": "Blog"}, map[string]any{"id": "b", "name": "Shop"}}})
	}))
	mux.HandleFunc("/api/websites/a/stats", authed(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"visitors": map[string]any{"value": 10, "prev": 20}, "pageviews": map[string]any{"value": 30, "prev": 40}})
	}))
	mux.HandleFunc("/api/websites/b/stats", authed(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"visitors": 5, "pageviews": 7, "comparison": map[string]any{"visitors": 50, "pageviews": 70}})
	}))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := sources.UmamiData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "admin:pw", VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	sites := out.(*sources.UmamiDataset).Sites
	if len(sites) != 2 || sites[0].Visitors != 10 || sites[0].PrevVisit != 20 || sites[1].Visitors != 5 || sites[1].PrevVisit != 50 {
		t.Fatalf("sites: %+v", sites)
	}
}
