package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"andon/internal/enums"
	"andon/internal/web"
)

// TestFormSecretShapes: two-part credentials are joined the way each
// driver expects, e.g. Proxmox "user@pam!andon=uuid".
func TestFormSecretShapes(t *testing.T) {
	cases := []struct {
		service enums.ServiceType
		form    url.Values
		want    string
	}{
		{enums.ServiceProxmox, url.Values{"secret_a": {"root@pam!andon"}, "secret_b": {"1234-uuid"}}, "root@pam!andon=1234-uuid"},
		{enums.ServiceAdGuard, url.Values{"secret_a": {"admin"}, "secret_b": {"pw"}}, "admin:pw"},
		{enums.ServiceUmami, url.Values{"secret_a": {"admin"}, "secret_b": {"pw"}}, "admin:pw"},
		{enums.ServiceUmami, url.Values{"secret_a": {""}, "secret_b": {"cloud-key"}}, "cloud-key"},
		{enums.ServiceNextcloud, url.Values{"secret_a": {"arian"}, "secret_b": {"app-pw"}}, "arian:app-pw"},
		{enums.ServiceNextcloud, url.Values{"secret_b": {"serverinfo-token"}}, "serverinfo-token"},
		{enums.ServiceGateway, url.Values{"secret_a": {"key"}, "secret_b": {"secret"}}, "key:secret"},
		{enums.ServiceGateway, url.Values{"secret_b": {"unifi-key"}}, "unifi-key"},
		{enums.ServiceKimai, url.Values{"secret": {"tok"}}, "tok"},
		{enums.ServiceScrutiny, url.Values{"secret": {"ignored"}}, ""},
		// Pasted tokens lose stray whitespace; passwords keep theirs.
		{enums.ServiceKintsugi, url.Values{"secret": {" tok\n"}}, "tok"},
		{enums.ServiceProxmox, url.Values{"secret_a": {"root@pam!andon "}, "secret_b": {"\t1234-uuid\r\n"}}, "root@pam!andon=1234-uuid"},
		{enums.ServiceGateway, url.Values{"secret_b": {"unifi-key\n"}}, "unifi-key"},
		{enums.ServiceAdGuard, url.Values{"secret_a": {" admin"}, "secret_b": {" pw "}}, "admin: pw "},
		{enums.ServicePihole, url.Values{"secret": {" pw "}}, " pw "},
	}
	for _, c := range cases {
		r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(c.form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if got, err := web.FormSecret(r, c.service); err != nil || got != c.want {
			t.Errorf("%s: got %q (%v), want %q", c.service, got, err, c.want)
		}
	}
}

// TestFormSecretIncomplete: services that always log in with both parts
// refuse half a credential instead of storing a broken one, e.g. a
// FreshRSS API password without its user name.
func TestFormSecretIncomplete(t *testing.T) {
	for _, service := range []enums.ServiceType{enums.ServiceFreshRSS, enums.ServiceMail, enums.ServiceAdGuard, enums.ServiceProxmox} {
		for _, form := range []url.Values{{"secret_b": {"only-password"}}, {"secret_a": {"only-user"}}} {
			r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if got, err := web.FormSecret(r, service); err == nil {
				t.Errorf("%s %v: accepted %q", service, form, got)
			}
		}
	}
}

// TestSetupScreensGuide: fixed API addresses are filled in, Proxmox asks
// for token ID and secret, the calendar names its fields by what goes in.
func TestSetupScreensGuide(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	github := string(mustGet(t, srv, client, "/connections/new?service=github"))
	if !strings.Contains(github, `value="https://api.github.com"`) {
		t.Fatalf("github URL not prefilled:\n%s", github)
	}
	proxmox := string(mustGet(t, srv, client, "/connections/new?service=proxmox"))
	if !strings.Contains(proxmox, "Token-ID") || !strings.Contains(proxmox, `name="secret_b"`) {
		t.Fatalf("proxmox lacks token ID and secret fields:\n%s", proxmox)
	}
	pihole := string(mustGet(t, srv, client, "/connections/new?service=pihole"))
	if !strings.Contains(pihole, `<label for="secret">Passwort</label>`) {
		t.Fatalf("pi-hole asks for a token instead of its password:\n%s", pihole)
	}
	calendar := string(mustGet(t, srv, client, "/connections/new?service=calendar"))
	if !strings.Contains(calendar, "Private iCal-Adresse") {
		t.Fatalf("calendar fields not named:\n%s", calendar)
	}
}
