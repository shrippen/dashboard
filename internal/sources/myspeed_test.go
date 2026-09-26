package sources_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"andon/internal/sources"
)

// TestMySpeed: the latest successful MySpeed test becomes the speedtest
// dataset; the password travels like MySpeed's own UI sends it; expected
// speeds come from MySpeed's config unless the options set them.
func TestMySpeed(t *testing.T) {
	const password = "pä:ss"
	authed := func(r *http.Request) bool {
		got, _ := url.QueryUnescape(r.Header.Get("x-password"))
		return got == password
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/speedtests", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode([]any{
			map[string]any{"id": 9, "ping": 0, "download": 0, "upload": 0, "error": "no connection", "created": "2026-09-26 18:00:00.000 +00:00"},
			map[string]any{"id": 8, "ping": 14, "jitter": 2.1, "download": 243.5, "upload": 41.2, "created": "2026-09-26 17:00:00.000 +00:00"},
		})
	})
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ping": "25", "download": "250", "upload": "40"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	sctx := sources.Ctx{URL: srv.URL, Secret: password, Options: map[string]any{"kind": "myspeed"}}
	out, err := sources.SpeedtestData{}.Fetch(context.Background(), sctx)
	if err != nil {
		t.Fatal(err)
	}
	data := out.(*sources.SpeedtestDataset)
	if data.Down != 243.5 || data.Up != 41.2 || data.Ping != 14 || data.At.Hour() != 17 {
		t.Fatalf("latest successful test: %+v", data)
	}
	if data.ExpectDown != 250 || data.ExpectUp != 40 {
		t.Fatalf("expected speeds from MySpeed: %+v", data)
	}

	sctx.Options["expect_down"] = 500.0
	out, _ = sources.SpeedtestData{}.Fetch(context.Background(), sctx)
	if out.(*sources.SpeedtestDataset).ExpectDown != 500 {
		t.Fatal("options must win over MySpeed's config")
	}
}
