package httpclient_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"dashboard/internal/drivers/httpclient"
)

func TestGetJSONSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total-Pages", "3")
		w.Write([]byte(`{"ok": true}`))
	}))
	defer srv.Close()

	body, headers, err := httpclient.GetJSON(context.Background(), srv.URL, httpclient.Options{})
	if err != nil {
		t.Fatalf("get json: %v", err)
	}
	m, ok := body.(map[string]any)
	if !ok || m["ok"] != true {
		t.Fatalf("unexpected body: %+v", body)
	}
	if headers.Get("X-Total-Pages") != "3" {
		t.Fatalf("expected header passthrough, got %q", headers.Get("X-Total-Pages"))
	}
}

func TestGetJSONErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, _, err := httpclient.GetJSON(context.Background(), srv.URL, httpclient.Options{})
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestGetJSONInvalidBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, _, err := httpclient.GetJSON(context.Background(), srv.URL, httpclient.Options{})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestEgressGuardDenies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	httpclient.SetGuard(func(host string, addrs []net.IP) bool { return false })
	defer httpclient.SetGuard(nil)

	_, _, err := httpclient.GetJSON(context.Background(), srv.URL, httpclient.Options{})
	var denied httpclient.EgressDenied
	if err == nil {
		t.Fatal("expected egress denied")
	}
	if !isEgressDenied(err, &denied) {
		t.Fatalf("expected EgressDenied, got %T: %v", err, err)
	}
}

func isEgressDenied(err error, target *httpclient.EgressDenied) bool {
	e, ok := err.(httpclient.EgressDenied)
	if ok {
		*target = e
	}
	return ok
}

func TestEgressGuardAllows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok": true}`))
	}))
	defer srv.Close()

	httpclient.SetGuard(func(host string, addrs []net.IP) bool { return true })
	defer httpclient.SetGuard(nil)

	_, _, err := httpclient.GetJSON(context.Background(), srv.URL, httpclient.Options{})
	if err != nil {
		t.Fatalf("expected allowed request to succeed, got %v", err)
	}
}
