package calendar_test

import (
	"strings"
	"testing"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/services/access"
	"dashboard/internal/services/calendar"
	"dashboard/internal/services/spaces"
	"dashboard/internal/testkit"
)

// The feed is valid iCalendar with one all-day event per tax deadline.
func TestFeedListsTaxDeadlines(t *testing.T) {
	d := testkit.DB(t)
	who, space := testkit.User(t, d, "a@b.c", enums.RoleUser)
	tax := map[string]any{"vat": map[string]any{"return_interval": "quarterly"}}
	if err := spaces.Update(d, who, space, map[string]any{"tax": tax}, ""); err != nil {
		t.Fatal(err)
	}
	who, _ = access.Load(d, who.UserID)

	feed, err := calendar.Feed(d, who, time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(feed, "BEGIN:VCALENDAR\r\n") || !strings.HasSuffix(feed, "END:VCALENDAR\r\n") {
		t.Fatalf("frame:\n%s", feed)
	}
	events := strings.Count(feed, "BEGIN:VEVENT")
	if events == 0 || events != strings.Count(feed, "DTSTART;VALUE=DATE:") {
		t.Fatalf("events:\n%s", feed)
	}
}
