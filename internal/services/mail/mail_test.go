package mail_test

import (
	"strings"
	"testing"

	"andon/internal/enums"
	"andon/internal/repos/users"
	"andon/internal/services/mail"
	"andon/internal/settings"
	"andon/internal/testkit"
)

// Mail text is escaped in HTML and repeated as plain text, button too.
func TestRenderEscapesAndTwins(t *testing.T) {
	mail.Init(settings.Settings{BaseURL: "https://dash.example"})
	m, err := mail.Render("a@b.c", enums.LocaleDE, "Hallo", []string{"<script>x</script>"},
		&mail.Button{Label: "Öffnen", URL: "https://dash.example/x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.HTML, "<script>") || !strings.Contains(m.HTML, "&lt;script&gt;") {
		t.Fatalf("html not escaped:\n%s", m.HTML)
	}
	if !strings.Contains(m.Text, "<script>x</script>") || !strings.Contains(m.Text, "Öffnen: https://dash.example/x") {
		t.Fatalf("text:\n%s", m.Text)
	}
}

// Each browser is remembered once; the list doesn't grow on repeats.
func TestNewLoginRemembersDevices(t *testing.T) {
	d := testkit.DB(t)
	who, _ := testkit.User(t, d, "a@b.c", enums.RoleUser)

	for _, agent := range []string{"Firefox", "Chrome", "Firefox"} {
		if err := mail.NewLogin(d, who.UserID, "10.0.0.1", agent); err != nil {
			t.Fatal(err)
		}
	}
	u, err := users.Get(d, who.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if known, _ := u.Prefs["known_devices"].([]any); len(known) != 2 {
		t.Fatalf("known devices: %v", u.Prefs["known_devices"])
	}
}
