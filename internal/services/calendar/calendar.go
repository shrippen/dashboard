// Package calendar lists upcoming deadlines (tax dates from space settings,
// hints with a due date) and renders them as a per-user iCal feed.
//
//	GET /calendar.ics?token=…  (read-scoped API token)
package calendar

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"andon/internal/enums"
	"andon/internal/i18n"
	"andon/internal/metrics"
	"andon/internal/repos/content"
	"andon/internal/services/access"
	"andon/internal/services/hints"
)

const (
	feedHorizonDays = 400
	prodID          = "-//shrippen//andon//DE"
	dayLayout       = "20060102"
	stampLayout     = "20060102T150405Z"
	taxRulePrefix   = "tax."
)

// Deadline is one dated entry, already translated.
type Deadline struct {
	UID  string
	Due  time.Time
	Text string
}

// TaxDeadlines returns the tax dates of every space who can reach, within days.
func TaxDeadlines(d *sql.DB, who *access.Principal, today time.Time, days int) ([]Deadline, error) {
	ids := make([]int64, 0, len(who.Spaces))
	for id := range who.Spaces {
		ids = append(ids, id)
	}
	spaces, err := content.Spaces(d, ids)
	if err != nil {
		return nil, err
	}

	var out []Deadline
	for _, sp := range spaces {
		tax, ok := metrics.ParseTaxSettings(sp.Settings)
		if !ok {
			continue
		}
		for _, item := range metrics.UpcomingDeadlines(tax, today, days) {
			text := i18n.T("deadline."+item.Kind, who.Locale, map[string]any{"period": item.Period, "year": item.Year})
			uid := fmt.Sprintf("%d-%s-%s", sp.ID, item.Kind, item.Due.Format(time.DateOnly))
			out = append(out, Deadline{UID: uid, Due: item.Due, Text: text})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Due.Before(out[j].Due) })
	return out, nil
}

// Feed renders who's deadlines and due hints as an iCalendar document.
func Feed(d *sql.DB, who *access.Principal, now time.Time) (string, error) {
	items, err := TaxDeadlines(d, who, now, feedHorizonDays)
	if err != nil {
		return "", err
	}

	open, err := hints.Active(d, who, enums.SeverityInfo, nil, 0)
	if err != nil {
		return "", err
	}
	for _, h := range open {
		if h.Due == "" || strings.HasPrefix(h.Rule, taxRulePrefix) {
			continue
		}
		due, err := time.Parse(time.DateOnly, h.Due[:min(len(h.Due), len(time.DateOnly))])
		if err != nil {
			continue
		}
		items = append(items, Deadline{UID: fmt.Sprintf("hint-%d", h.ID), Due: due, Text: h.Title})
	}

	stamp := now.UTC().Format(stampLayout)
	lines := []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:" + prodID, "CALSCALE:GREGORIAN",
		"X-WR-CALNAME:" + escape(i18n.T("calendar.name", who.Locale, nil)),
	}
	for _, item := range items {
		lines = append(lines, event(item, stamp)...)
	}
	lines = append(lines, "END:VCALENDAR")
	return strings.Join(lines, "\r\n") + "\r\n", nil
}

// event is one all-day VEVENT (DTEND is exclusive: the next day).
func event(item Deadline, stamp string) []string {
	return []string{
		"BEGIN:VEVENT",
		"UID:" + item.UID + "@andon",
		"DTSTAMP:" + stamp,
		"DTSTART;VALUE=DATE:" + item.Due.Format(dayLayout),
		"DTEND;VALUE=DATE:" + item.Due.AddDate(0, 0, 1).Format(dayLayout),
		"SUMMARY:" + escape(item.Text),
		"END:VEVENT",
	}
}

// escape applies RFC 5545 TEXT escaping: "a, b; c" -> "a\, b\; c".
func escape(text string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`).Replace(text)
}
