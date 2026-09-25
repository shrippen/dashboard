// Package notify sends push notifications through a user's own Apprise
// channels when new hints appear, respecting per-channel severity and
// quiet hours.
//
//	every minute   new hints ≥ channel level, not sent yet, outside quiet hours → push
//
// Digest e-mails (Python's notify.digests) are not ported: that needs an
// SMTP outbound service that doesn't exist in Go yet.
package notify

import (
	"context"
	"database/sql"
	"errors"
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
	"dashboard/internal/services/hints"
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
			out = append(out, ChannelView{ID: c.ID, Name: c.Name, Hint: mask(url), MinSeverity: c.MinSeverity, Enabled: c.Enabled})
		}
		return nil
	})
	return out, err
}

// AddChannel adds a new apprise:// (or ntfy://, gotify://, ...) channel.
func AddChannel(d *sql.DB, who *access.Principal, name, rawURL string, level enums.Severity) error {
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
		channel := &model.NotifyChannel{UserID: who.UserID, Name: label, URLEnc: enc, MinSeverity: level, Enabled: true}
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
}

const quietKey = "quiet"

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
		return nil
	})
	return out, err
}

// SavePrefs writes a user's quiet-hours preference.
func SavePrefs(d *sql.DB, who *access.Principal, p Prefs) error {
	for _, text := range []string{p.QuietFrom, p.QuietTo} {
		if text != "" && parseClock(text) == nil {
			return ErrBadTime
		}
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
		prefs[quietKey] = map[string]any{"from": p.QuietFrom, "to": p.QuietTo}
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
		if quietNow(u.Prefs, now) {
			continue
		}
		n, err := dispatchUser(ctx, d, cfg, u.ID)
		if err != nil {
			return sent, err
		}
		sent += n
	}
	return sent, nil
}

func dispatchUser(ctx context.Context, d *sql.DB, cfg settings.Settings, userID int64) (int, error) {
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
	err = db.WithTx(d, func(tx *sql.Tx) error {
		for _, h := range open {
			sent, err := data.WasSent(tx, userID, h.ID)
			if err != nil {
				return err
			}
			if !sent {
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
			if h.Severity >= c.MinSeverity {
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
			if err := data.LogSent(tx, userID, h.ID); err != nil {
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
