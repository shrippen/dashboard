package sources_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"andon/internal/drivers/httpclient"
	"andon/internal/sources"
)

// TestHassSignIn: code → short token → long-lived token over WebSocket.
func TestHassSignIn(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/token", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("code") != "c1" || r.FormValue("client_id") != "https://andon.test/" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"access_token":"short","expires_in":1800}`))
	})
	mux.HandleFunc("/api/websocket", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		send := func(v any) { raw, _ := json.Marshal(v); _ = c.Write(ctx, websocket.MessageText, raw) }
		recv := func() map[string]any {
			_, raw, _ := c.Read(ctx)
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			return m
		}
		send(map[string]any{"type": "auth_required"})
		if recv()["access_token"] != "short" {
			send(map[string]any{"type": "auth_invalid"})
			return
		}
		send(map[string]any{"type": "auth_ok"})
		req := recv()
		if req["type"] != "auth/long_lived_access_token" || req["lifespan"] != float64(3650) {
			send(map[string]any{"id": 1, "type": "result", "success": false})
			return
		}
		send(map[string]any{"id": 1, "type": "result", "success": true, "result": "long-lived"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	short, err := sources.HassToken(ctx, srv.URL, "c1", "https://andon.test/", httpclient.TLSVerify)
	if err != nil || short != "short" {
		t.Fatalf("token: %q %v", short, err)
	}
	long, err := sources.HassLongLived(ctx, srv.URL, short, httpclient.TLSVerify, time.Now())
	if err != nil || long != "long-lived" {
		t.Fatalf("long-lived: %q %v", long, err)
	}
}

// TestNextcloudLoginFlow: start, "not yet" (404), then user and app password.
func TestNextcloudLoginFlow(t *testing.T) {
	var polls atomic.Int32
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("POST /index.php/login/v2", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"login": srv.URL + "/login/v2/flow/x",
			"poll": map[string]string{"token": "pt", "endpoint": srv.URL + "/login/v2/poll"}})
	})
	mux.HandleFunc("POST /login/v2/poll", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("token") != "pt" || polls.Add(1) == 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"server":"x","loginName":"arian","appPassword":"app-pw"}`))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	login, err := sources.NextcloudStart(ctx, srv.URL, httpclient.TLSVerify)
	if err != nil || login.Link == "" {
		t.Fatalf("start: %+v %v", login, err)
	}
	if _, done, err := sources.NextcloudPoll(ctx, login, httpclient.TLSVerify); done || err != nil {
		t.Fatalf("first poll should wait: done=%v err=%v", done, err)
	}
	secret, done, err := sources.NextcloudPoll(ctx, login, httpclient.TLSVerify)
	if !done || err != nil || secret != "arian:app-pw" {
		t.Fatalf("second poll: %q %v %v", secret, done, err)
	}
}

// TestJellyfinQuickConnect: code, pending, approved → access token.
func TestJellyfinQuickConnect(t *testing.T) {
	var approved atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("POST /QuickConnect/Initiate", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Code":"123456","Secret":"s1"}`))
	})
	mux.HandleFunc("GET /QuickConnect/Connect", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]bool{"Authenticated": approved.Load() && r.URL.Query().Get("secret") == "s1"})
	})
	mux.HandleFunc("POST /Users/AuthenticateWithQuickConnect", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"AccessToken":"jf-token"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	quick, err := sources.JellyfinStart(ctx, srv.URL, "dev1", httpclient.TLSVerify)
	if err != nil || quick.Code != "123456" {
		t.Fatalf("start: %+v %v", quick, err)
	}
	if _, done, err := sources.JellyfinPoll(ctx, srv.URL, "dev1", quick, httpclient.TLSVerify); done || err != nil {
		t.Fatalf("pending poll: done=%v err=%v", done, err)
	}
	approved.Store(true)
	token, done, err := sources.JellyfinPoll(ctx, srv.URL, "dev1", quick, httpclient.TLSVerify)
	if !done || err != nil || token != "jf-token" {
		t.Fatalf("approved poll: %q %v %v", token, done, err)
	}
}
