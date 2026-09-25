package web_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestSmallerActions(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)

	form := string(mustGet(t, srv, client, "/boards/new"))
	space := regexp.MustCompile(`<option value="(\d+)">`).FindStringSubmatch(form)[1]
	resp := postForm(t, client, srv.URL+"/boards/new", url.Values{"csrf": {csrf}, "name": {"Zweites"}, "space_id": {space}})
	if resp.StatusCode != http.StatusSeeOther || !strings.HasSuffix(resp.Header.Get("Location"), "?edit") {
		t.Fatalf("new board: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp = postForm(t, client, srv.URL+"/connections", url.Values{"csrf": {csrf}, "space_id": {space}, "service": {"kimai"},
		"name": {"Mine"}, "url": {"https://kimai.lan"}, "mode": {"personal"}, "tls": {"verify"}})
	conn := regexp.MustCompile(`/connections/(\d+)/edit`).FindStringSubmatch(resp.Header.Get("Location"))[1]
	if !strings.Contains(string(mustGet(t, srv, client, "/me/credentials")), "Mine") {
		t.Fatal("personal connection missing on credentials page")
	}
	postForm(t, client, srv.URL+"/me/credentials/"+conn, url.Values{"csrf": {csrf}, "secret": {"tok"}})
	if !strings.Contains(string(mustGet(t, srv, client, "/me/credentials")), `data-state="ok"`) {
		t.Fatal("personal token not stored")
	}

	postForm(t, client, srv.URL+"/connections/"+conn+"/options", url.Values{"csrf": {csrf}, "options": {"all_users: true\n"}})
	if !strings.Contains(string(mustGet(t, srv, client, "/connections/"+conn+"/edit")), "all_users: true") {
		t.Fatal("options not stored")
	}

	postForm(t, client, srv.URL+"/teams", url.Values{"csrf": {csrf}, "name": {"Ops"}})
	teams := string(mustGet(t, srv, client, "/teams"))
	team := regexp.MustCompile(`/teams/(\d+)/members"`).FindStringSubmatch(teams)[1]
	me := regexp.MustCompile(`<option value="(\d+)">Admin`).FindStringSubmatch(teams)[1]
	postForm(t, client, srv.URL+"/teams/"+team+"/members", url.Values{"csrf": {csrf}, "user_id": {me}, "role": {"owner"}})
	teams = string(mustGet(t, srv, client, "/teams"))
	teamSpace := regexp.MustCompile(`/spaces/(\d+)/team-settings`).FindStringSubmatch(teams)[1]
	postForm(t, client, srv.URL+"/spaces/"+teamSpace+"/team-settings", url.Values{"csrf": {csrf}, "hint_ack": {"team"}})
	if !strings.Contains(string(mustGet(t, srv, client, "/teams")), `<option value="team" selected>`) {
		t.Fatal("team hint handling not stored")
	}

	resp = postForm(t, client, srv.URL+"/me/security/sessions/others/end", url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("end others: %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/me/locale", strings.NewReader("csrf="+csrf+"&locale=en"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", srv.URL+"/hints")
	resp, _ = client.Do(req)
	if resp.Header.Get("Location") != "/hints" || !strings.Contains(string(mustGet(t, srv, client, "/hints")), `lang="en"`) {
		t.Fatalf("locale switch: %s", resp.Header.Get("Location"))
	}
}
