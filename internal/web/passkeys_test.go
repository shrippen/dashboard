package web_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestPasskeyLoginFinishNeedsJSON(t *testing.T) {
	srv, client, _ := newTestServer(t)

	// A cross-site form can only send text/plain; that must never log in.
	resp, err := client.Post(srv.URL+"/login/passkey/finish?ceremony=x", "text/plain", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestPasskeyBeginNeedsSession(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)

	resp, err := client.Post(srv.URL+"/me/passkeys/begin", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("begin without session succeeded")
	}

	login(t, srv, client)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/me/passkeys/begin", strings.NewReader("{}"))
	req.Header.Set("X-CSRF-Token", csrfToken(t, srv, client))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"challenge"`) {
		t.Fatalf("begin: %d %s", resp.StatusCode, body)
	}
}
