package services_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dashboard/internal/drivers/services"
)

func TestKimaiPagesFollowsXTotalPages(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Query().Get("page"))
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("expected bearer token header, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("X-Total-Pages", "2")
		page := r.URL.Query().Get("page")
		w.Write([]byte(fmt.Sprintf(`[{"id": %s}]`, page)))
	}))
	defer srv.Close()

	api := services.KimaiApi{URL: srv.URL, Token: "tok", Verify: true}
	items, err := api.Pages(context.Background(), "timesheets", url.Values{})
	if err != nil {
		t.Fatalf("pages: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items across 2 pages, got %d", len(items))
	}
	if len(seen) != 2 || seen[0] != "1" || seen[1] != "2" {
		t.Fatalf("expected pages 1,2 requested, got %v", seen)
	}
}

func TestKimaiGetNotFoundIsApiMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	api := services.KimaiApi{URL: srv.URL, Token: "tok", Verify: true}
	_, err := api.Get(context.Background(), "holiday/absences", nil)
	if _, ok := err.(services.ApiMissing); !ok {
		t.Fatalf("expected ApiMissing for 404, got %T: %v", err, err)
	}
}

func TestNinjaPagesFollowsMetaPagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-TOKEN") != "tok" {
			t.Errorf("expected token header, got %q", r.Header.Get("X-API-TOKEN"))
		}
		page := r.URL.Query().Get("page")
		w.Write([]byte(fmt.Sprintf(
			`{"data": [{"id": %s}], "meta": {"pagination": {"total_pages": 2}}}`, page)))
	}))
	defer srv.Close()

	api := services.NinjaApi{URL: srv.URL, Token: "tok", Verify: true}
	items, err := api.Pages(context.Background(), "invoices", url.Values{})
	if err != nil {
		t.Fatalf("pages: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestPaperlessGetOmitsAPIVersion(t *testing.T) {
	// paperless-ngx returns 406 for a pinned version it no longer accepts;
	// the driver must not force one.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if accept := r.Header.Get("Accept"); strings.Contains(accept, "version=") {
			w.WriteHeader(http.StatusNotAcceptable)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	api := services.PaperlessApi{URL: srv.URL, Token: "tok", Verify: true}
	_, err := api.Get(context.Background(), "documents/", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
}

func TestNinjaVersionReadsHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-App-Version", "5.1.0")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	api := services.NinjaApi{URL: srv.URL, Token: "tok", Verify: true}
	v, err := api.Version(context.Background())
	if err != nil || v != "5.1.0" {
		t.Fatalf("expected version 5.1.0, got %q err=%v", v, err)
	}
}

func TestSnipeRowsFollowsOffset(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		offset := r.URL.Query().Get("offset")
		if offset == "0" {
			w.Write([]byte(`{"total": 3, "rows": [{"id": 1}, {"id": 2}]}`))
		} else {
			w.Write([]byte(`{"total": 3, "rows": [{"id": 3}]}`))
		}
	}))
	defer srv.Close()

	api := services.SnipeApi{URL: srv.URL, Token: "tok", Verify: true}
	items, err := api.Rows(context.Background(), "hardware", nil)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls (offset 0 then 2), got %d", calls)
	}
}

func TestDawarichGetIncludesApiKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "secret" {
			t.Errorf("expected api_key param, got %q", r.URL.Query().Get("api_key"))
		}
		w.Write([]byte(`{"areas": []}`))
	}))
	defer srv.Close()

	api := services.DawarichApi{URL: srv.URL, Token: "secret", Verify: true}
	_, err := api.Get(context.Background(), "areas", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
}

func TestDawarichVersionReadsHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Dawarich-Version", "0.24.0")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	api := services.DawarichApi{URL: srv.URL, Token: "secret", Verify: true}
	v, err := api.Version(context.Background())
	if err != nil || v != "0.24.0" {
		t.Fatalf("expected version, got %q err=%v", v, err)
	}
}
