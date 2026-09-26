package sources

// Calendar as a connection, so rules can compare appointments with other
// data (e.g. Kimai bookings). The feed address usually contains a secret,
// so it is taken from the token when one is stored:
//
//	token  https://cloud.example/remote.php/dav/…?export  (encrypted)
//	url    any address of the calendar, for the tile

import (
	"context"
	"time"

	"andon/internal/drivers/httpclient"
	"andon/internal/enums"
)

// calendarDays is the window before and after today.
const calendarDays = 30

type CalendarData struct{}

func (CalendarData) Key() string                { return "calendar.data" }
func (CalendarData) TTL() time.Duration         { return icalTTL }
func (CalendarData) Service() enums.ServiceType { return enums.ServiceCalendar }

func (CalendarData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	now := time.Now()
	if isDemo(sctx) {
		return DemoCalendar(now), nil
	}
	feed := sctx.URL
	if sctx.Secret != "" {
		feed = sctx.Secret
	}
	text, err := httpclient.GetText(ctx, feed, httpclient.Options{SkipVerify: !sctx.VerifyTLS})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	return &CalendarResult{Events: Occurrences(text, now.AddDate(0, 0, -calendarDays), now.AddDate(0, 0, calendarDays))}, nil
}

// DemoCalendar has one customer appointment two days ago.
func DemoCalendar(now time.Time) *CalendarResult {
	day := time.Date(now.Year(), now.Month(), now.Day(), 14, 0, 0, 0, time.UTC).AddDate(0, 0, -2)
	return &CalendarResult{Events: []Event{{Start: day, Title: "Workshop Acme GmbH"}, {Start: day.AddDate(0, 0, 5), Title: "Zahnarzt"}}}
}

func init() {
	Register(CalendarData{})
	Register(testOf{CalendarData{}, func(d any) map[string]any { return map[string]any{"events": len(d.(*CalendarResult).Events)} }})
}
