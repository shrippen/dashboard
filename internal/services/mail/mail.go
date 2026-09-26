// Package mail composes account and security mails in the recipient's
// language and hands them to the outbound adapter.
package mail

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"html/template"
	"strings"
	"sync"

	"andon/internal/db"
	"andon/internal/enums"
	"andon/internal/i18n"
	"andon/internal/outbound"
	"andon/internal/repos/users"
	"andon/internal/settings"
)

// SecurityKind names a security notice ("mail.security.<kind>").
type SecurityKind string

const (
	PasswordChanged SecurityKind = "password_changed"
	TOTPEnabled     SecurityKind = "totp_enabled"
	TOTPDisabled    SecurityKind = "totp_disabled"
	TokenCreated    SecurityKind = "token_created"
	PasskeyAdded    SecurityKind = "passkey_added"
)

const (
	knownDevicesPref = "known_devices"
	maxDevices       = 20
	deviceIDLen      = 16
	maxAgentLen      = 120
)

// palette mirrors the shrippen dark tokens. Mail clients ignore CSS
// variables, so the values are inlined here instead of linked from themes/.
type palette struct{ Bg, Card, Fg, Text, Head, Accent, Line, Muted string }

var shrippenDark = palette{
	Bg: "#141312", Card: "#2a2826", Fg: "#ebdbb2", Text: "#d5c4a1",
	Head: "#fbf1c7", Accent: "#fabd2f", Line: "#3c3836", Muted: "#a89984",
}

// DefaultRowColor is the neutral colour for a digest row label.
const DefaultRowColor = "#ebdbb2"

//go:embed templates/base.html
var files embed.FS

var base = template.Must(template.ParseFS(files, "templates/base.html"))

var (
	cfgMu sync.RWMutex
	cfg   settings.Settings
)

// Init stores the operating settings (base URL, SMTP) once at startup.
func Init(c settings.Settings) {
	cfgMu.Lock()
	cfg = c
	cfgMu.Unlock()
}

func current() settings.Settings {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return cfg
}

// Configured reports whether mails can be delivered.
func Configured() bool { return outbound.MailConfigured(current()) }

// Row is one label/text line (digest entries).
type Row struct {
	Label string
	Text  string
	Color string
}

// Button is the single call-to-action link.
type Button struct {
	Label string
	URL   string
}

type view struct {
	Lang       string
	Title      string
	Paragraphs []string
	Items      []Row
	Button     *Button
	Footer     string
	C          palette
}

// Render builds an HTML mail with a plain-text twin.
func Render(to string, locale enums.Locale, subject string, paragraphs []string, button *Button, items []Row) (outbound.Mail, error) {
	footer := i18n.T("mail.footer", locale, map[string]any{"url": current().BaseURL})
	for i := range items {
		if items[i].Color == "" {
			items[i].Color = DefaultRowColor
		}
	}

	var html bytes.Buffer
	err := base.Execute(&html, view{
		Lang: string(locale), Title: subject, Paragraphs: paragraphs, Items: items,
		Button: button, Footer: footer, C: shrippenDark,
	})
	if err != nil {
		return outbound.Mail{}, err
	}

	lines := append([]string{subject, ""}, paragraphs...)
	for _, row := range items {
		lines = append(lines, row.Label+"  "+row.Text)
	}
	if button != nil {
		lines = append(lines, "", button.Label+": "+button.URL)
	}
	lines = append(lines, "", footer)

	return outbound.Mail{To: to, Subject: subject, Text: strings.Join(lines, "\n"), HTML: html.String()}, nil
}

// Send queues a rendered mail.
func Send(m outbound.Mail) { outbound.Deliver(current(), m) }

// BaseURL returns the configured external URL (for links in mails).
func BaseURL() string { return current().BaseURL }

// Invite mails an invitation link.
func Invite(email, link, inviter string, locale enums.Locale) error {
	m, err := Render(email, locale, i18n.T("mail.invite.subject", locale, nil),
		[]string{i18n.T("mail.invite.body", locale, map[string]any{"inviter": inviter})},
		&Button{Label: i18n.T("mail.invite.button", locale, nil), URL: link}, nil)
	if err != nil {
		return err
	}
	Send(m)
	return nil
}

// Reset mails a password reset link.
func Reset(email, link string, locale enums.Locale) error {
	m, err := Render(email, locale, i18n.T("mail.reset.subject", locale, nil),
		[]string{i18n.T("mail.reset.body", locale, nil)},
		&Button{Label: i18n.T("mail.reset.button", locale, nil), URL: link}, nil)
	if err != nil {
		return err
	}
	Send(m)
	return nil
}

// SecurityNotice tells a user about a security-relevant change.
func SecurityNotice(q db.Queryer, userID int64, kind SecurityKind) error {
	user, err := users.Get(q, userID)
	if err != nil || user == nil {
		return err
	}
	locale := user.Locale
	subject := i18n.T("mail.security."+string(kind), locale, nil)
	m, err := Render(user.Email, locale, subject,
		[]string{subject, i18n.T("mail.security.hint", locale, nil)}, nil, nil)
	if err != nil {
		return err
	}
	Send(m)
	return nil
}

// NewLogin mails only for devices not seen before (fingerprint of the
// user agent). The very first device is remembered silently.
func NewLogin(d *sql.DB, userID int64, ip, agent string) error {
	sum := sha256.Sum256([]byte(agent))
	device := hex.EncodeToString(sum[:])[:deviceIDLen]

	var first bool
	var email string
	var locale enums.Locale
	seen := false
	err := db.WithTx(d, func(tx *sql.Tx) error {
		user, err := users.Get(tx, userID)
		if err != nil || user == nil {
			return err
		}
		known := knownDevices(user.Prefs)
		for _, k := range known {
			if k == device {
				seen = true
				return nil
			}
		}

		known = append([]string{device}, known...)
		if len(known) > maxDevices {
			known = known[:maxDevices]
		}
		if user.Prefs == nil {
			user.Prefs = map[string]any{}
		}
		user.Prefs[knownDevicesPref] = known
		email, locale, first = user.Email, user.Locale, len(known) == 1
		return users.Update(tx, user)
	})
	if err != nil || seen || first || email == "" {
		return err
	}

	if len(agent) > maxAgentLen {
		agent = agent[:maxAgentLen]
	}
	subject := i18n.T("mail.security.new_login", locale, nil)
	body := i18n.T("mail.security.new_login_body", locale, map[string]any{"ip": ip, "agent": agent})
	m, err := Render(email, locale, subject, []string{body, i18n.T("mail.security.hint", locale, nil)}, nil, nil)
	if err != nil {
		return err
	}
	Send(m)
	return nil
}

func knownDevices(prefs map[string]any) []string {
	raw, _ := prefs[knownDevicesPref].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	if typed, ok := prefs[knownDevicesPref].([]string); ok {
		return typed
	}
	return out
}
