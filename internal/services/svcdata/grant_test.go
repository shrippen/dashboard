package svcdata_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"andon/internal/crypto"
	"andon/internal/enums"
	"andon/internal/repos/content"
	"andon/internal/services/svcdata"
	"andon/internal/sources"
	"andon/internal/testkit"
)

// TestGrantRenewedBeforeFetch: a signed-in connection with an expired
// access token is renewed through its refresh token, the source gets the
// new token, and the renewed grant is stored.
func TestGrantRenewedBeforeFetch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("refresh_token") != "r1" || r.FormValue("client_id") != "cid" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"access_token":"fresh","refresh_token":"r2","expires_in":3600}`))
	})
	mux.HandleFunc("GET /data", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"ok": 1}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := testkit.DB(t)
	who, space := testkit.User(t, d, "a@b.c", enums.RoleAdmin)
	id := testkit.Conn(t, d, who, space, enums.ServiceJSONAPI, srv.URL+"/data")

	conn, _ := content.Connection(d, id)
	marker, _ := crypto.Encrypt(sources.GrantMarker, crypto.PurposeCredential, nil)
	conn.SecretEnc = marker
	client, _ := crypto.Encrypt("cid:cs", crypto.PurposeCredential, nil)
	expired := sources.Grant{Kind: sources.GrantRefresh, TokenURL: srv.URL + "/token", Access: "old", Refresh: "r1",
		Expires: time.Now().Add(-time.Hour)}
	if err := content.UpdateConnection(d, conn); err != nil {
		t.Fatal(err)
	}
	if err := content.SetOAuthClient(d, id, client); err != nil {
		t.Fatal(err)
	}
	if err := svcdata.StoreGrant(d, id, 0, expired); err != nil {
		t.Fatal(err)
	}

	result, err := svcdata.Get(context.Background(), d, "jsonapi.data", nil, conn, &who.UserID, svcdata.Force)
	if err != nil || !result.Ok() {
		t.Fatalf("fetch with renewed grant: %+v err=%v", result, err)
	}

	enc, _ := content.Grant(d, id, 0)
	raw, _ := crypto.Decrypt(enc, crypto.PurposeCredential)
	var stored sources.Grant
	_ = json.Unmarshal([]byte(raw), &stored)
	if stored.Access != "fresh" || stored.Refresh != "r2" {
		t.Fatalf("renewed grant not stored: %+v", stored)
	}
}
