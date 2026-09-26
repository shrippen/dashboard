package outbound_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"andon/internal/outbound"
)

func TestSendPostsExpectedPayload(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/notify" {
			t.Errorf("expected /notify, got %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := outbound.Send(context.Background(), srv.URL, "ntfy://a/b", "hi", "body"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got["urls"] != "ntfy://a/b" || got["title"] != "hi" || got["body"] != "body" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestSendReturnsErrOnFailureStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	err := outbound.Send(context.Background(), srv.URL, "bad://url", "hi", "body")
	if err != outbound.ErrNotifyFailed {
		t.Fatalf("expected ErrNotifyFailed, got %v", err)
	}
}
