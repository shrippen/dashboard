package notify_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/outbound"
	"dashboard/internal/repos/content"
	"dashboard/internal/services/mail"
	"dashboard/internal/services/notify"
	"dashboard/internal/settings"
)

// TestDigestSendsOncePerDayOnChosenWeekday: the digest goes out after the
// chosen time on the chosen weekday, lists tax deadlines, and only once a day.
func TestDigestSendsOncePerDayOnChosenWeekday(t *testing.T) {
	d := openTestDB(t)
	mail.Init(settings.Settings{Testing: true, BaseURL: "http://dash.test"})
	outbound.TakeOutbox()
	userID := addUser(t, d)
	who := principalFor(t, d, userID)

	if err := notify.SavePrefs(d, who, notify.Prefs{Daily: "07:30", Weekly: "mon"}); err != nil {
		t.Fatalf("save prefs: %v", err)
	}
	err := db.WithTx(d, func(tx *sql.Tx) error {
		for id := range who.Spaces {
			sp, err := content.Space(tx, id)
			if err != nil {
				return err
			}
			tax := map[string]any{"tax": map[string]any{"vat": map[string]any{"return_interval": "monthly"}}}
			return content.UpdateSpaceSettings(tx, id, tax, sp.Version)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tax settings: %v", err)
	}

	berlin, _ := time.LoadLocation("Europe/Berlin")
	mondayEarly := time.Date(2026, 9, 28, 7, 0, 0, 0, berlin)
	tuesday := time.Date(2026, 9, 29, 8, 0, 0, 0, berlin)
	mondayLate := time.Date(2026, 9, 28, 8, 0, 0, 0, berlin)

	for _, now := range []time.Time{mondayEarly, tuesday} {
		if n, err := notify.Digests(d, now); err != nil || n != 0 {
			t.Fatalf("expected no digest at %s, got %d err=%v", now, n, err)
		}
	}
	if n, err := notify.Digests(d, mondayLate); err != nil || n != 1 {
		t.Fatalf("expected one digest, got %d err=%v", n, err)
	}
	sent := outbound.TakeOutbox()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "USt-Voranmeldung") {
		t.Fatalf("expected digest with VAT deadline, got %+v", sent)
	}
	if n, _ := notify.Digests(d, mondayLate.Add(time.Hour)); n != 0 {
		t.Fatal("expected only one digest per day")
	}
}
