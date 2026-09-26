package sources_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"andon/internal/drivers/httpclient"
	"andon/internal/sources"
)

// tokenEndpoint answers like an OAuth2 token endpoint and records the
// last form it got.
func tokenEndpoint(t *testing.T, answer string, last *url.Values, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("form: %v", err)
		}
		*last = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestExchangeCode(t *testing.T) {
	var form url.Values
	var calls atomic.Int32
	srv := tokenEndpoint(t, `{"access_token":"a1","refresh_token":"r1","expires_in":3600}`, &form, &calls)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

	g, err := sources.ExchangeCode(context.Background(), srv.URL, "code1", "https://andon/cb", "ver",
		sources.OAuthClient{ID: "cid", Secret: "cs"}, httpclient.TLSVerify, now)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if g.Access != "a1" || g.Refresh != "r1" || !g.Expires.Equal(now.Add(time.Hour)) || g.Kind != sources.GrantRefresh {
		t.Fatalf("grant: %+v", g)
	}
	if form.Get("grant_type") != "authorization_code" || form.Get("code_verifier") != "ver" || form.Get("client_secret") != "cs" {
		t.Fatalf("form: %v", form)
	}
}

func TestGrantFresh(t *testing.T) {
	var form url.Values
	var calls atomic.Int32
	srv := tokenEndpoint(t, `{"access_token":"a2","expires_in":3600}`, &form, &calls)
	client := sources.OAuthClient{ID: "cid", Secret: "cs"}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// Still valid: no call.
	valid := sources.Grant{Kind: sources.GrantRefresh, TokenURL: srv.URL, Access: "a1", Refresh: "r1", Expires: now.Add(time.Hour)}
	if g, renewed, err := valid.Fresh(ctx, client, httpclient.TLSVerify, now); err != nil || renewed || g.Access != "a1" || calls.Load() != 0 {
		t.Fatalf("valid grant renewed: %+v %v %v", g, renewed, err)
	}

	// Expiring: refresh, keep the old refresh token when none comes back.
	expiring := valid
	expiring.Expires = now.Add(30 * time.Second)
	g, renewed, err := expiring.Fresh(ctx, client, httpclient.TLSVerify, now)
	if err != nil || !renewed || g.Access != "a2" || g.Refresh != "r1" {
		t.Fatalf("refresh: %+v %v %v", g, renewed, err)
	}
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "r1" {
		t.Fatalf("refresh form: %v", form)
	}

	// Client credentials: no token yet, fetch one with scope.
	cc := sources.Grant{Kind: sources.GrantClient, TokenURL: srv.URL, Scope: "devices:core:read"}
	g, renewed, err = cc.Fresh(ctx, client, httpclient.TLSVerify, now)
	if err != nil || !renewed || g.Access != "a2" || form.Get("grant_type") != "client_credentials" || form.Get("scope") != "devices:core:read" {
		t.Fatalf("client credentials: %+v %v %v form=%v", g, renewed, err, form)
	}

	// Refresh grant without refresh token cannot renew.
	lost := sources.Grant{Kind: sources.GrantRefresh, TokenURL: srv.URL, Access: "old", Expires: now.Add(-time.Hour)}
	if _, _, err := lost.Fresh(ctx, client, httpclient.TLSVerify, now); err == nil {
		t.Fatal("expected an error without refresh token")
	}
}
