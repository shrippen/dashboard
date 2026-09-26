package sources

// iCal calendar feed (Nextcloud, Google, Outlook "secret address"):
// upcoming events within a window, recurring events expanded.
//
//	BEGIN:VEVENT
//	DTSTART;TZID=Europe/Berlin:20260928T090000   ─┐
//	RRULE:FREQ=WEEKLY;BYDAY=MO,WE;COUNT=10        ├─► Event{Start, Title}
//	SUMMARY:Standup                               ─┘   per occurrence
//
// Supported rules: FREQ DAILY/WEEKLY/MONTHLY/YEARLY with INTERVAL, COUNT,
// UNTIL and BYDAY (weekly), EXDATE and moved instances (RECURRENCE-ID).
// Other BY* parts are ignored: the event then repeats on its start day.

import (
	"bufio"
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"andon/internal/drivers/httpclient"
	"andon/internal/enums"
)

const (
	icalTTL       = 15 * time.Minute
	icalZone      = "Europe/Berlin" // for floating times
	icalDate      = "20060102"
	icalDateTime  = "20060102T150405"
	icalMaxEvents = 50
	icalMaxSteps  = 20000 // guards endless rules
	daysPerWeek   = 7
)

// Event is one occurrence.
type Event struct {
	Start    time.Time
	AllDay   bool
	Title    string
	Location string
}

// CalendarResult lists occurrences, soonest first.
type CalendarResult struct{ Events []Event }

type CalendarSource struct{}

func (CalendarSource) Key() string                { return "ical" }
func (CalendarSource) TTL() time.Duration         { return icalTTL }
func (CalendarSource) Service() enums.ServiceType { return "" }

func (CalendarSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	text, err := httpclient.GetText(ctx, asStr(sctx.Params["url"]), httpclient.Options{})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	days := int(asFloat(sctx.Params["days"]))
	now := time.Now()
	return &CalendarResult{Events: Occurrences(text, now, now.AddDate(0, 0, days))}, nil
}

// vevent is one parsed VEVENT: property name → value, params.
type vevent map[string]icalProp

type icalProp struct {
	Value  string
	Params map[string]string
}

// Occurrences returns the events of an iCal text starting in [from, to).
func Occurrences(text string, from, to time.Time) []Event {
	events := parseEvents(text)

	// Moved or cancelled instances replace their rule occurrence.
	replaced := map[string]map[int64]bool{}
	for _, ev := range events {
		rid, ok := ev["RECURRENCE-ID"]
		if !ok {
			continue
		}
		start, _, ok := icalTime(rid)
		if !ok {
			continue
		}
		uid := ev["UID"].Value
		if replaced[uid] == nil {
			replaced[uid] = map[int64]bool{}
		}
		replaced[uid][start.Unix()] = true
	}

	var out []Event
	for _, ev := range events {
		if strings.EqualFold(ev["STATUS"].Value, "CANCELLED") {
			continue
		}
		start, allDay, ok := icalTime(ev["DTSTART"])
		if !ok {
			continue
		}
		skip := exdates(ev)
		for k := range replaced[ev["UID"].Value] {
			if _, isOverride := ev["RECURRENCE-ID"]; !isOverride {
				skip[k] = true
			}
		}
		base := Event{Title: unescape(ev["SUMMARY"].Value), Location: unescape(ev["LOCATION"].Value), AllDay: allDay}
		for _, at := range expand(start, ev["RRULE"].Value, to) {
			if at.Before(from) || !at.Before(to) || skip[at.Unix()] {
				continue
			}
			e := base
			e.Start = at
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	if len(out) > icalMaxEvents {
		out = out[:icalMaxEvents]
	}
	return out
}

func parseEvents(text string) []vevent {
	var events []vevent
	var cur vevent
	for _, line := range unfold(text) {
		switch {
		case line == "BEGIN:VEVENT":
			cur = vevent{}
			continue
		case line == "END:VEVENT":
			if cur != nil {
				events = append(events, cur)
			}
			cur = nil
			continue
		}
		if cur == nil {
			continue
		}
		head, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		parts := strings.Split(head, ";")
		prop := icalProp{Value: value, Params: map[string]string{}}
		for _, p := range parts[1:] {
			if k, v, ok := strings.Cut(p, "="); ok {
				prop.Params[strings.ToUpper(k)] = strings.Trim(v, `"`)
			}
		}
		name := strings.ToUpper(parts[0])

		// EXDATE may repeat; keep every value.
		if old, ok := cur[name]; ok && name == "EXDATE" {
			prop.Value = old.Value + "," + prop.Value
		}
		cur[name] = prop
	}
	return events
}

// unfold joins continuation lines (RFC 5545 3.1).
func unfold(text string) []string {
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += line[1:]
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func unescape(s string) string {
	return strings.NewReplacer(`\n`, " ", `\N`, " ", `\,`, ",", `\;`, ";", `\\`, `\`).Replace(s)
}

func zone(name string) *time.Location {
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	loc, _ := time.LoadLocation(icalZone)
	if loc == nil {
		return time.UTC
	}
	return loc
}

// icalTime parses DATE, UTC and zoned/floating DATE-TIME values.
func icalTime(p icalProp) (time.Time, bool, bool) {
	v := strings.TrimSpace(p.Value)
	if len(v) == len(icalDate) {
		t, err := time.ParseInLocation(icalDate, v, zone(icalZone))
		return t, true, err == nil
	}
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse(icalDateTime, strings.TrimSuffix(v, "Z"))
		return t, false, err == nil
	}
	t, err := time.ParseInLocation(icalDateTime, v, zone(firstNonBlank(p.Params["TZID"], icalZone)))
	return t, false, err == nil
}

func firstNonBlank(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func exdates(ev vevent) map[int64]bool {
	out := map[int64]bool{}
	p, ok := ev["EXDATE"]
	if !ok {
		return out
	}
	for _, v := range strings.Split(p.Value, ",") {
		if t, _, ok := icalTime(icalProp{Value: v, Params: p.Params}); ok {
			out[t.Unix()] = true
		}
	}
	return out
}

var weekdays = map[string]time.Weekday{
	"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday,
	"TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday,
}

// expand lists the occurrences of a rule up to (excluding) end.
func expand(start time.Time, rule string, end time.Time) []time.Time {
	if rule == "" {
		return []time.Time{start}
	}
	parts := map[string]string{}
	for _, p := range strings.Split(rule, ";") {
		if k, v, ok := strings.Cut(p, "="); ok {
			parts[strings.ToUpper(k)] = v
		}
	}
	interval, _ := strconv.Atoi(parts["INTERVAL"])
	interval = max(interval, 1)
	count, _ := strconv.Atoi(parts["COUNT"])
	if until, _, ok := icalTime(icalProp{Value: parts["UNTIL"], Params: map[string]string{}}); ok && until.Before(end) {
		end = until.Add(time.Second)
	}

	var days []time.Weekday
	for _, d := range strings.Split(parts["BYDAY"], ",") {
		if wd, ok := weekdays[strings.ToUpper(strings.TrimSpace(d))]; ok {
			days = append(days, wd)
		}
	}

	var out []time.Time
	emit := func(t time.Time) bool {
		if !t.Before(end) || (count > 0 && len(out) >= count) {
			return false
		}
		out = append(out, t)
		return true
	}

	for step := 0; step < icalMaxSteps; step++ {
		switch parts["FREQ"] {
		case "DAILY":
			if !emit(start.AddDate(0, 0, step*interval)) {
				return out
			}
		case "WEEKLY":
			if !weekly(start, step*interval, days, emit) {
				return out
			}
		case "MONTHLY":
			t := start.AddDate(0, step*interval, 0)
			if t.Day() != start.Day() {
				continue // no 31st in this month
			}
			if !emit(t) {
				return out
			}
		case "YEARLY":
			t := start.AddDate(step*interval, 0, 0)
			if t.Day() != start.Day() {
				continue // 29 February
			}
			if !emit(t) {
				return out
			}
		default:
			return []time.Time{start}
		}
	}
	return out
}

// weekly emits the BYDAY days of the week week weeks after start (only
// start's own weekday without BYDAY); false once emit refuses.
func weekly(start time.Time, week int, days []time.Weekday, emit func(time.Time) bool) bool {
	if len(days) == 0 {
		return emit(start.AddDate(0, 0, week*daysPerWeek))
	}
	monday := start.AddDate(0, 0, -((int(start.Weekday())+6)%daysPerWeek)+week*daysPerWeek)
	sort.Slice(days, func(i, j int) bool { return (days[i]+6)%daysPerWeek < (days[j]+6)%daysPerWeek })
	for _, wd := range days {
		t := monday.AddDate(0, 0, (int(wd)+6)%daysPerWeek)
		if t.Before(start) {
			continue
		}
		if !emit(t) {
			return false
		}
	}
	return true
}

func init() {
	Register(CalendarSource{})
}
