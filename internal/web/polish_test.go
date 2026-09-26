package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"andon/internal/enums"
	"andon/internal/services/hints"
	"andon/internal/web"
)

// With TOTP forced for admins, an admin without it only reaches the
// security page (to set it up) until it's on.
func TestForcedAdminTOTPRedirects(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	resp, err := client.PostForm(srv.URL+"/admin/settings/general", url.Values{
		"csrf": {csrfToken(t, srv, client)}, "force_admin_totp": {"on"},
	})
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	resp.Body.Close()

	resp, err = client.Get(srv.URL + "/connections")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/me/security") {
		t.Fatalf("expected a redirect to the security page, got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	if body := string(mustGet(t, srv, client, "/me/security")); !strings.Contains(body, "totp/begin") || !strings.Contains(body, `class="error"`) {
		t.Fatalf("security page must stay reachable:\n%s", body)
	}
}

// The audit log names actions and people in words, not codes.
func TestAuditLogReadable(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	page := string(mustGet(t, srv, client, "/admin/audit"))
	if !strings.Contains(page, "Anmeldung mit Passwort") || strings.Contains(page, ">login.password<") || strings.Contains(page, ">#1<") {
		t.Fatalf("expected readable audit entries:\n%s", page)
	}
}

func TestGroupHintsFoldsLongRules(t *testing.T) {
	var views []hints.View
	for i := range 7 {
		views = append(views, hints.View{ID: int64(i), Rule: "in.missing", Severity: enums.SeverityWarn, Sources: []string{"invoiceninja"}})
	}
	views = append(views, hints.View{ID: 99, Rule: "kimai.gap", Severity: enums.SeverityCritical, Sources: []string{"kimai"}})

	groups := web.GroupHints(views)
	if len(groups) != 2 || len(groups[0].Shown) != 5 || len(groups[0].Rest) != 2 || groups[1].Rule != "kimai.gap" {
		t.Fatalf("groups: %+v", groups)
	}
	f := web.HintFilter{Level: "critical"}
	if got := f.Apply(views); len(got) != 1 || got[0].ID != 99 {
		t.Fatalf("filter: %+v", got)
	}
}
