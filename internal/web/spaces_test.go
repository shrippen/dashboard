package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSpaceSettingsSaveGoalsAndRules(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)

	resp := getFollowingRedirect(t, srv, client, "/spaces/settings")
	page := readAll(t, resp)
	resp.Body.Close()
	settingsURL := resp.Request.URL.Path
	if !strings.Contains(page, `name="rule.kimai.timer_running_long.hours"`) {
		t.Fatalf("rule parameters missing:\n%s", page)
	}

	resp = postForm(t, client, srv.URL+settingsURL, url.Values{
		"csrf": {csrf}, "revenue_year": {"90000"}, "hours_per_day": {"7"}, "vat_method": {"soll"},
		"rule.kimai.timer_running_long.hours": {"6"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", resp.StatusCode)
	}
	page = string(mustGet(t, srv, client, settingsURL))
	for _, want := range []string{`value="90000"`, `<option value="soll" selected>`, `name="rule.kimai.timer_running_long.hours" value="6"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("expected %q after save:\n%s", want, page)
		}
	}
	if strings.Contains(page, `name="rule.kimai.timer_running_long.enabled" checked`) {
		t.Fatal("an unchecked rule must be stored as disabled")
	}
}
