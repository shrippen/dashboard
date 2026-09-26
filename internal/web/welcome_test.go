package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestWelcomeFlow: the first login opens the welcome page once; it shows
// the checklist and the concepts; a page intro shows until closed; the
// menu shows the progress.
func TestWelcomeFlow(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)

	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := noFollow.PostForm(srv.URL+"/login", url.Values{"email": {"admin@x.de"}, "password": {"s3cret-password-long"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if loc := res.Header.Get("Location"); loc != "/start" {
		t.Fatalf("login goes to %q", loc)
	}
	for i, want := range []string{"/welcome", "/"} {
		res, err := noFollow.Get(srv.URL + "/start")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if loc := res.Header.Get("Location"); loc != want {
			t.Fatalf("start #%d goes to %q, want %q", i+1, loc, want)
		}
	}

	welcome := string(mustGet(t, srv, client, "/welcome"))
	for _, want := range []string{"Erste Schritte", `class="checklist"`, `href="/connections/new"`, `id="c-connections"`, "0/6"} {
		if !strings.Contains(welcome, want) {
			t.Fatalf("welcome lacks %q:\n%s", want, welcome)
		}
	}

	hints := string(mustGet(t, srv, client, "/hints"))
	if !strings.Contains(hints, `class="intro"`) || !strings.Contains(hints, `<span class="nav-count" title="Erste Schritte">`) {
		t.Fatalf("hints page lacks intro or menu progress:\n%s", hints)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/welcome/intro/hints", strings.NewReader(url.Values{"csrf": {csrfToken(t, srv, client)}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("closing the intro: %d", res.StatusCode)
	}
	if hints = string(mustGet(t, srv, client, "/hints")); strings.Contains(hints, `class="intro"`) {
		t.Fatal("intro still shown after closing it")
	}
	// Visiting the hints ticked that step.
	if welcome = string(mustGet(t, srv, client, "/welcome")); !strings.Contains(welcome, "1/6") {
		t.Fatalf("hints visit not counted:\n%s", welcome)
	}
}
