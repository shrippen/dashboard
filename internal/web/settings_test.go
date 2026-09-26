package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"andon/internal/drivers/httpclient"
)

func TestSettingsOpenRegistrationAndCSP(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	t.Cleanup(func() { httpclient.SetGuard(nil) })

	resp, err := client.Get(srv.URL + "/admin/settings")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-src 'none'") || !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Fatalf("unexpected CSP: %q", csp)
	}

	csrf := csrfToken(t, srv, client)
	resp = postForm(t, client, srv.URL+"/admin/settings/general", url.Values{
		"csrf": {csrf}, "registration": {"on"}, "iframe": {"https://grafana.lan, javascript:x"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save general: %d", resp.StatusCode)
	}

	guest := freshClient(t)
	resp = postForm(t, guest, srv.URL+"/register", url.Values{"email": {"self@x.de"}, "name": {"Self"}, "password": {"long-enough-password"}, "locale": {"de"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected open registration, got %d", resp.StatusCode)
	}

	resp, _ = guest.Get(srv.URL + "/embed/hints")
	resp.Body.Close()
	csp = resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-src https://grafana.lan;") || !strings.Contains(csp, "frame-ancestors *") {
		t.Fatalf("expected iframe origin and open ancestors on embeds: %q", csp)
	}

	resp = postForm(t, client, srv.URL+"/admin/settings/network", url.Values{"csrf": {csrf}, "mode": {"allowlist"}, "networks": {"not-a-cidr"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected bad CIDR refused, got %d", resp.StatusCode)
	}
	resp = postForm(t, client, srv.URL+"/admin/settings/network", url.Values{"csrf": {csrf}, "mode": {"allowlist"}, "networks": {"10.0.0.0/8"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected network saved, got %d", resp.StatusCode)
	}
}
