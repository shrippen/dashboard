package web_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"dashboard/internal/outbound"
)

var linkRe = regexp.MustCompile(`http://dash\.test(/(?:invite|reset)/[A-Za-z0-9_-]+)`)

func freshClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func postForm(t *testing.T, client *http.Client, target string, form url.Values) *http.Response {
	t.Helper()
	resp, err := client.PostForm(target, form)
	if err != nil {
		t.Fatalf("post %s: %v", target, err)
	}
	resp.Body.Close()
	return resp
}

// TestInviteMailAcceptLogin: an admin invite is mailed; the invitee accepts
// via the mailed link, joins the chosen team and is logged in.
func TestInviteMailAcceptLogin(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	csrf := csrfToken(t, srv, client)
	postForm(t, client, srv.URL+"/teams", url.Values{"csrf": {csrf}, "name": {"Ops"}})
	outbound.TakeOutbox()

	resp, err := client.PostForm(srv.URL+"/admin/invite", url.Values{
		"csrf": {csrf}, "email": {"new@x.de"}, "role": {"user"}, "locale": {"de"},
		"teams": {"Ops"}, "team_role": {"editor"},
	})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with invite link, got %d", resp.StatusCode)
	}

	sent := outbound.TakeOutbox()
	if len(sent) != 1 || sent[0].To != "new@x.de" || !strings.Contains(sent[0].HTML, "/invite/") {
		t.Fatalf("expected one invite mail, got %+v", sent)
	}
	path := linkRe.FindStringSubmatch(sent[0].Text)[1]

	guest := freshClient(t)
	body := mustGet(t, srv, guest, path)
	if !strings.Contains(string(body), "new@x.de") {
		t.Fatalf("expected invite form for new@x.de:\n%s", body)
	}
	resp = postForm(t, guest, srv.URL+path, url.Values{"name": {"Neu"}, "password": {"another-long-password"}, "locale": {"de"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("expected login redirect after accept, got %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	body = mustGet(t, srv, client, "/admin/users")
	if !strings.Contains(string(body), "Ops (Editor)") {
		t.Fatalf("expected invitee in team Ops as editor:\n%s", body)
	}

	resp = postForm(t, freshClient(t), srv.URL+path, url.Values{"name": {"X"}, "password": {"another-long-password"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected a used invite to be refused, got %d", resp.StatusCode)
	}
}

// TestPasswordResetByMail: the reset request mails a link; the new password
// works, the old one does not, and a security notice follows.
func TestPasswordResetByMail(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	outbound.TakeOutbox()

	guest := freshClient(t)
	postForm(t, guest, srv.URL+"/reset", url.Values{"email": {"nobody@x.de"}})
	if len(outbound.TakeOutbox()) != 0 {
		t.Fatal("expected no mail for an unknown address")
	}

	postForm(t, guest, srv.URL+"/reset", url.Values{"email": {"admin@x.de"}})
	sent := outbound.TakeOutbox()
	if len(sent) != 1 {
		t.Fatalf("expected one reset mail, got %d", len(sent))
	}
	path := linkRe.FindStringSubmatch(sent[0].Text)[1]

	resp := postForm(t, guest, srv.URL+path, url.Values{"password": {"short"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected short password refused, got %d", resp.StatusCode)
	}
	resp = postForm(t, guest, srv.URL+path, url.Values{"password": {"brand-new-password-1"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect after reset, got %d", resp.StatusCode)
	}
	if notice := outbound.TakeOutbox(); len(notice) != 1 || !strings.Contains(notice[0].Subject, "Passwort") {
		t.Fatalf("expected a password-changed notice, got %+v", notice)
	}

	resp = postForm(t, freshClient(t), srv.URL+"/login", url.Values{"email": {"admin@x.de"}, "password": {"s3cret-password-long"}})
	if resp.StatusCode == http.StatusSeeOther {
		t.Fatal("old password still works")
	}
	resp = postForm(t, freshClient(t), srv.URL+"/login", url.Values{"email": {"admin@x.de"}, "password": {"brand-new-password-1"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("new password refused: %d", resp.StatusCode)
	}

	resp = postForm(t, guest, srv.URL+path, url.Values{"password": {"brand-new-password-2"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected a used reset link to be refused, got %d", resp.StatusCode)
	}
}

// TestRegisterClosedByDefault: self-registration is off until an admin opens it.
func TestRegisterClosedByDefault(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)

	resp, err := freshClient(t).Get(srv.URL + "/register")
	if err != nil {
		t.Fatalf("get register: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected closed registration, got %d", resp.StatusCode)
	}
	resp = postForm(t, freshClient(t), srv.URL+"/register", url.Values{"email": {"x@x.de"}, "name": {"X"}, "password": {"long-enough-password"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected registration refused, got %d", resp.StatusCode)
	}
}

// TestAdminResetLinkAndGuards: the admin gets a reset link without SMTP,
// and cannot deactivate or delete their own account.
func TestAdminResetLinkAndGuards(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	body := mustGet(t, srv, client, "/admin/users")
	id := regexp.MustCompile(`/admin/users/(\d+)/role`).FindSubmatch(body)[1]
	csrf := csrfToken(t, srv, client)

	resp, err := client.PostForm(srv.URL+"/admin/users/"+string(id)+"/reset", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatalf("reset link: %v", err)
	}
	defer resp.Body.Close()
	page := readAll(t, resp)
	if !strings.Contains(page, "http://dash.test/reset/") {
		t.Fatalf("expected reset link on page:\n%s", page)
	}

	resp = postForm(t, client, srv.URL+"/admin/users/"+string(id)+"/active", url.Values{"csrf": {csrf}, "state": {"off"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected self-deactivation refused, got %d", resp.StatusCode)
	}
	resp = postForm(t, client, srv.URL+"/admin/users/"+string(id)+"/delete", url.Values{"csrf": {csrf}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected self-deletion refused, got %d", resp.StatusCode)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}
