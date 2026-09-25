// Package notify sends push notifications through a user's own Apprise
// channels when new hints appear, respecting per-channel severity and
// quiet hours.
//
//	every minute   new hints ≥ channel level of subscribed sources, not sent yet → push
//	               quiet hours: only critical ones (unless muted); the rest waits
//	               critical and still open after N hours → push again (not flapping)
//
//	every 5 min    digest mail at the user's "HH:MM" (daily, or on one weekday)
package notify

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/i18n"
	"dashboard/internal/model"
	"dashboard/internal/outbound"
	data "dashboard/internal/repos/data"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/audit"
	"dashboard/internal/services/calendar"
	"dashboard/internal/services/hints"
	"dashboard/internal/services/mail"
	"dashboard/internal/services/summary"
	"dashboard/internal/services/util"
	"dashboard/internal/settings"
)

const maxDigestLines = 10

var berlin = mustLoadBerlin()

func mustLoadBerlin() *time.Location {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return time.UTC
	}
	return loc
}

var (
	ErrDenied     = access.ErrDenied
	ErrInvalidURL = errors.New("notify: invalid apprise url")
	ErrBadTime    = errors.New("notify: bad time")
	ErrFailed     = errors.New("notify: delivery failed")
)

// ChannelView is one channel as shown to its owner (URL masked).
type ChannelView struct {
	ID          int64
	Name        string
	Hint        string // masked URL, e.g. "ntfy://…/topic"
	MinSeverity enums.Severity
	Enabled     bool
	Sources     []string
}

func mask(rawURL string) string {
	scheme, rest, ok := strings.Cut(rawURL, "://")
	if !ok {
		return scheme
	}
	if i := strings.LastIndex(rest, "/"); i >= 0 {
		return scheme + "://…/" + rest[i+1:]
	}
	return scheme + "://" + rest
}

func looksLikeApprise(rawURL string) bool {
	return strings.Contains(rawURL, "://")
}

// Channels lists a user's own notification channels.
func Channels(d *sql.DB, who *access.Principal) ([]ChannelView, error) {
	var out []ChannelView
	err := db.WithTx(d, func(tx *sql.Tx) error {
		chans, err := data.Channels(tx, who.UserID)
		if err != nil {
			return err
		}
		for _, c := range chans {
			url, err := crypto.Decrypt(c.URLEnc, crypto.PurposeNotify)
			if err != nil {
				return err
			}
			out = append(out, ChannelView{ID: c.ID, Name: c.Name, Hint: mask(url), MinSeverity: c.MinSeverity, Enabled: c.Enabled, Sources: c.Sources})
		}
		return nil
	})
	return out, err
}

// AddChannel adds a new apprise:// (or ntfy://, gotify://, ...) channel;
// sources limits it to hints of these services (none = all).
func AddChannel(d *sql.DB, who *access.Principal, name, rawURL string, level enums.Severity, sources []string) error {
	rawURL = strings.TrimSpace(rawURL)
	if !looksLikeApprise(rawURL) {
		return ErrInvalidURL
	}
	label := strings.TrimSpace(name)
	if label == "" {
		label, _, _ = strings.Cut(rawURL, ":")
	}
	enc, err := crypto.Encrypt(rawURL, crypto.PurposeNotify, nil)
	if err != nil {
		return err
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		channel := &model.NotifyChannel{UserID: who.UserID, Name: label, URLEnc: enc, MinSeverity: level, Enabled: true, Sources: sources}
		if err := data.AddChannel(tx, channel); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "notify.channel_added", label, "", nil)
	})
}

func own(q db.Queryer, who *access.Principal, channelID int64) (*model.NotifyChannel, error) {
	item, err := data.Channel(q, channelID)
	if err != nil {
		return nil, err
	}
	if item == nil || item.UserID != who.UserID {
		return nil, ErrDenied
	}
	return item, nil
}

// DeleteChannel removes one of the caller's own channels.
func DeleteChannel(d *sql.DB, who *access.Principal, channelID int64) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		if _, err := own(tx, who, channelID); err != nil {
			return err
		}
		return data.RemoveChannel(tx, channelID)
	})
}

// TestChannel sends a test push through one of the caller's own channels.
func TestChannel(ctx context.Context, d *sql.DB, cfg settings.Settings, who *access.Principal, channelID int64) error {
	var rawURL string
	err := db.WithTx(d, func(tx *sql.Tx) error {
		channel, err := own(tx, who, channelID)
		if err != nil {
			return err
		}
		rawURL, err = crypto.Decrypt(channel.URLEnc, crypto.PurposeNotify)
		return err
	})
	if err != nil {
		return err
	}
	title := i18n.T("notify.test_title", who.Locale, nil)
	body := i18n.T("notify.test_body", who.Locale, nil)
	if err := outbound.Send(ctx, cfg.AppriseAPIURL, rawURL, title, body); err != nil {
		return ErrFailed
	}
	return nil
}

// ── Quiet hours ──

// Prefs is a user's notification preferences.
type Prefs struct {
	QuietFrom, QuietTo string // "HH:MM", "" = no quiet hours
	QuietMuted         bool   // quiet hours hold back critical hints too
	RepeatHours        int    // re-push open critical hints after this long, 0 = never
	Daily              string // digest time "HH:MM", "" = no digest
	Weekly             string // weekday key ("mon".."sun"), "" = every day
	NoSummary          bool   // opt-out of the LLM summary in the weekly digest
}

const (
	quietKey      = "quiet"
	digestKey     = "digest"
	digestSentKey = "digest_sent"
	deadlineDays  = 14
	noSummaryKey  = "no_summary"
	mutedKey      = "muted"
	repeatKey     = "repeat_hours"
	summaryWait   = 3 * time.Minute
)

// Weekdays are the digest weekday keys, Monday first (catalog "weekday.<key>").
var Weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// levelColor colours a digest row by severity (shrippen blue/yellow/red).
var levelColor = map[enums.Severity]string{
	enums.SeverityInfo: "#83a598", enums.SeverityWarn: "#fabd2f", enums.SeverityCritical: "#fb4934",
}

// GetPrefs reads a user's quiet-hours preference.
func GetPrefs(d *sql.DB, who *access.Principal) (Prefs, error) {
	var out Prefs
	err := db.WithTx(d, func(tx *sql.Tx) error {
		u, err := users.Get(tx, who.UserID)
		if err != nil || u == nil {
			return orNotFound(err)
		}
		quiet, _ := u.Prefs[quietKey].(map[string]any)
		out.QuietFrom, _ = quiet["from"].(string)
		out.QuietTo, _ = quiet["to"].(string)
		out.QuietMuted, _ = quiet[mutedKey].(bool)
		out.RepeatHours = repeatHours(u.Prefs)
		digest, _ := u.Prefs[digestKey].(map[string]any)
		out.Daily, _ = digest["daily"].(string)
		out.Weekly, _ = digest["weekly"].(string)
		out.NoSummary, _ = digest[noSummaryKey].(bool)
		return nil
	})
	return out, err
}

// SavePrefs writes a user's quiet-hours preference.
func SavePrefs(d *sql.DB, who *access.Principal, p Prefs) error {
	for _, text := range []string{p.QuietFrom, p.QuietTo, p.Daily} {
		if text != "" && parseClock(text) == nil {
			return ErrBadTime
		}
	}
	if p.Weekly != "" && weekdayIndex(p.Weekly) < 0 {
		return ErrBadTime
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		u, err := users.Get(tx, who.UserID)
		if err != nil || u == nil {
			return orNotFound(err)
		}
		prefs := map[string]any{}
		for k, v := range u.Prefs {
			prefs[k] = v
		}
		prefs[quietKey] = map[string]any{"from": p.QuietFrom, "to": p.QuietTo, mutedKey: p.QuietMuted, repeatKey: float64(max(p.RepeatHours, 0))}
		prefs[digestKey] = map[string]any{"daily": p.Daily, "weekly": p.Weekly, noSummaryKey: p.NoSummary}
		u.Prefs = prefs
		return users.Update(tx, u)
	})
}

func orNotFound(err error) error {
	if err != nil {
		return err
	}
	return util.ErrNotFound
}

// clock is minutes since midnight, for a quiet-hours comparison that
// doesn't need a full time.Time.
type clock int

func parseClock(text string) *clock {
	parts := strings.SplitN(text, ":", 2)
	if len(parts) != 2 {
		return nil
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return nil
	}
	c := clock(h*60 + m)
	return &c
}

// quietNow reports whether now falls in a user's quiet window (Europe/
// Berlin, may cross midnight: 22:00–07:00).
func quietNow(prefs map[string]any, now time.Time) bool {
	quiet, _ := prefs[quietKey].(map[string]any)
	from, _ := quiet["from"].(string)
	to, _ := quiet["to"].(string)
	start, end := parseClock(from), parseClock(to)
	if start == nil || end == nil {
		return false
	}
	local := now.In(berlin)
	current := clock(local.Hour()*60 + local.Minute())
	if *start <= *end {
		return *start <= current && current < *end
	}
	return current >= *start || current < *end
}

// ── Dispatch (job) ──

// Dispatch pushes new hints to every active user's enabled channels,
// skipping users currently in their quiet hours. Returns the number of
// pushes sent (one per channel per user, batched across hints).
func Dispatch(ctx context.Context, d *sql.DB, cfg settings.Settings) (int, error) {
	var people []*model.User
	err := db.WithTx(d, func(tx *sql.Tx) error {
		all, err := users.All(tx)
		if err != nil {
			return err
		}
		for _, u := range all {
			if u.IsActive {
				people = append(people, u)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	sent := 0
	now := time.Now().UTC()
	for _, u := range people {
		quiet := quietNow(u.Prefs, now)
		muted, _ := asMap(u.Prefs[quietKey])[mutedKey].(bool)
		if quiet && muted {
			continue
		}
		n, err := dispatchUser(ctx, d, cfg, u.ID, quiet, repeatHours(u.Prefs))
		if err != nil {
			return sent, err
		}
		sent += n
	}
	return sent, nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func repeatHours(prefs map[string]any) int {
	n, _ := asMap(prefs[quietKey])[repeatKey].(float64)
	return int(n)
}

// due decides whether a hint is pushed now.
//
//	never sent                      → yes (in quiet hours: critical only)
//	sent, critical, not flapping,
//	older than repeat hours         → yes, again
func due(h hints.View, last time.Time, quiet bool, repeat int, now time.Time) bool {
	if quiet && h.Severity < enums.SeverityCritical {
		return false
	}
	if last.IsZero() {
		return true
	}
	return repeat > 0 && h.Severity >= enums.SeverityCritical && !h.Flapping &&
		now.Sub(last) >= time.Duration(repeat)*time.Hour
}

// subscribed: the channel takes every source, or one of the hint's.
func subscribed(c *model.NotifyChannel, h hints.View) bool {
	if len(c.Sources) == 0 {
		return true
	}
	for _, want := range c.Sources {
		for _, have := range h.Sources {
			if want == have {
				return true
			}
		}
	}
	return false
}

func dispatchUser(ctx context.Context, d *sql.DB, cfg settings.Settings, userID int64, quiet bool, repeat int) (int, error) {
	who, err := access.Load(d, userID)
	if err != nil {
		return 0, err
	}

	var chans []*model.NotifyChannel
	err = db.WithTx(d, func(tx *sql.Tx) error {
		found, err := data.Channels(tx, userID)
		if err != nil {
			return err
		}
		for _, c := range found {
			if c.Enabled {
				chans = append(chans, c)
			}
		}
		return nil
	})
	if err != nil || len(chans) == 0 {
		return 0, err
	}

	open, err := hints.Active(d, who, enums.SeverityInfo, nil, 0)
	if err != nil {
		return 0, err
	}

	var fresh []hints.View
	now := time.Now().UTC()
	err = db.WithTx(d, func(tx *sql.Tx) error {
		for _, h := range open {
			last, err := data.LastSent(tx, userID, h.ID)
			if err != nil {
				return err
			}
			if due(h, last, quiet, repeat, now) {
				fresh = append(fresh, h)
			}
		}
		return nil
	})
	if err != nil || len(fresh) == 0 {
		return 0, err
	}

	sentCount := 0
	for _, c := range chans {
		var batch []hints.View
		for _, h := range fresh {
			if h.Severity >= c.MinSeverity && subscribed(c, h) {
				batch = append(batch, h)
			}
		}
		if len(batch) == 0 {
			continue
		}
		if err := sendBatch(ctx, d, cfg, who, c, batch); err == nil {
			sentCount++
		}
	}

	return sentCount, db.WithTx(d, func(tx *sql.Tx) error {
		for _, h := range fresh {
			if err := data.TouchSent(tx, userID, h.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

func sendBatch(ctx context.Context, d *sql.DB, cfg settings.Settings, who *access.Principal, c *model.NotifyChannel, batch []hints.View) error {
	var rawURL string
	err := db.WithTx(d, func(tx *sql.Tx) error {
		u, err := data.Channel(tx, c.ID)
		if err != nil || u == nil {
			return orNotFound(err)
		}
		rawURL, err = crypto.Decrypt(u.URLEnc, crypto.PurposeNotify)
		return err
	})
	if err != nil {
		return err
	}

	title := i18n.T("notify.title", who.Locale, map[string]any{"count": len(batch)})
	var lines []string
	for i, h := range batch {
		if i >= maxDigestLines {
			break
		}
		lines = append(lines, "• "+h.Title)
	}
	return outbound.Send(ctx, cfg.AppriseAPIURL, rawURL, title, strings.Join(lines, "\n"))
}

// ── Digest mail (job) ──

func weekdayIndex(key string) int {
	for i, k := range Weekdays {
		if k == key {
			return i
		}
	}
	return -1
}

// digestDue reports whether a user's digest should go out now: past the
// chosen time, not yet sent today, and (if weekly) on the chosen weekday.
func digestDue(prefs map[string]any, local time.Time) bool {
	digest, _ := prefs[digestKey].(map[string]any)
	daily, _ := digest["daily"].(string)
	at := parseClock(daily)
	if at == nil {
		return false
	}
	if clock(local.Hour()*60+local.Minute()) < *at {
		return false
	}
	if sent, _ := prefs[digestSentKey].(string); sent == local.Format(time.DateOnly) {
		return false
	}
	weekly, _ := digest["weekly"].(string)
	mondayFirst := (int(local.Weekday()) + 6) % 7
	return weekly == "" || weekdayIndex(weekly) == mondayFirst
}

// Digests sends every due digest mail. Returns the number sent.
func Digests(d *sql.DB, now time.Time) (int, error) {
	if !mail.Configured() {
		return 0, nil
	}
	local := now.In(berlin)
	all, err := users.All(d)
	if err != nil {
		return 0, err
	}

	sent := 0
	for _, u := range all {
		if !u.IsActive || !digestDue(u.Prefs, local) {
			continue
		}
		if err := sendDigest(d, u.ID, local); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

// weeklySummary is the LLM prose for weekly digests, "" when off, opted
// out or failed (the digest goes out regardless).
func weeklySummary(prefs map[string]any, open []hints.View, deadlines int, locale enums.Locale) string {
	digest, _ := prefs[digestKey].(map[string]any)
	weekly, _ := digest["weekly"].(string)
	optOut, _ := digest[noSummaryKey].(bool)
	if weekly == "" || optOut || !summary.Enabled() {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), summaryWait)
	defer cancel()
	text, err := summary.Weekly(ctx, open, deadlines, locale)
	if err != nil {
		slog.Warn("weekly summary", "err", err)
		return ""
	}
	return text
}

func sendDigest(d *sql.DB, userID int64, local time.Time) error {
	var who *access.Principal
	var prefs map[string]any
	err := db.WithTx(d, func(tx *sql.Tx) error {
		u, err := users.Get(tx, userID)
		if err != nil || u == nil {
			return orNotFound(err)
		}
		prefs = map[string]any{}
		for k, v := range u.Prefs {
			prefs[k] = v
		}
		prefs[digestSentKey] = local.Format(time.DateOnly)
		u.Prefs = prefs
		if err := users.Update(tx, u); err != nil {
			return err
		}
		who, err = access.Load(tx, userID)
		return err
	})
	if err != nil || who == nil {
		return err
	}

	locale := who.Locale
	open, err := hints.Active(d, who, enums.SeverityInfo, nil, maxDigestLines*2)
	if err != nil {
		return err
	}
	rows := make([]mail.Row, 0, len(open))
	for _, h := range open {
		label := i18n.T("severity."+h.Severity.Key(), locale, nil)
		rows = append(rows, mail.Row{Label: label, Text: h.Title, Color: levelColor[h.Severity]})
	}

	due, err := calendar.TaxDeadlines(d, who, local, deadlineDays)
	if err != nil {
		return err
	}
	for _, item := range due {
		rows = append(rows, mail.Row{Label: i18n.Day(item.Due, locale), Text: item.Text})
	}

	subject := i18n.T("notify.digest_subject", locale, map[string]any{"count": len(rows)})
	body := i18n.T("hints.none", locale, nil)
	if len(rows) > 0 {
		body = i18n.T("notify.digest_body", locale, nil)
	}
	paragraphs := []string{body}
	if text := weeklySummary(prefs, open, len(due), locale); text != "" {
		paragraphs = []string{text, body}
	}
	m, err := mail.Render(who.Email, locale, subject, paragraphs, nil, rows)
	if err != nil {
		return err
	}
	mail.Send(m)
	return nil
}
