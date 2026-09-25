// Package metrics turns normalized service datasets into numbers: hours,
// revenue, utilization, upcoming dates. Pure functions, no I/O.
package metrics

import "time"

// Today truncates t to a UTC midnight "date" value. All metrics/rules
// functions that take a "today" or date-range argument expect values built
// this way (or returned by ParseDay), so time.Time equality/comparison
// behaves like comparing plain dates.
func Today(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ParseDay parses an ISO date (or the first 10 chars of a longer
// timestamp) into a UTC midnight time.Time. Returns the zero time and
// false for an empty or unparsable value.
func ParseDay(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	if len(value) > 10 {
		value = value[:10]
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// ParseTime parses an ISO-8601 timestamp (accepting a trailing "Z").
func ParseTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// MonthStart returns the 1st of day's month.
func MonthStart(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// AddMonths returns the 1st of the month `months` away:
// AddMonths(2026-03-15, -2) -> 2026-01-01.
func AddMonths(day time.Time, months int) time.Time {
	index := day.Year()*12 + int(day.Month()) - 1 + months
	year, month := index/12, index%12+1
	return time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
}

// WeekStart returns the Monday of day's week.
func WeekStart(day time.Time) time.Time {
	offset := int(day.Weekday()) - int(time.Monday)
	if offset < 0 {
		offset += 7
	}
	return day.AddDate(0, 0, -offset)
}

// QuarterStart returns the 1st of day's quarter.
func QuarterStart(day time.Time) time.Time {
	m := (int(day.Month())-1)/3*3 + 1
	return time.Date(day.Year(), time.Month(m), 1, 0, 0, 0, 0, time.UTC)
}

// Workdays returns Mon-Fri between start and end (inclusive), minus the
// days in free.
func Workdays(start, end time.Time, free map[time.Time]bool) []time.Time {
	var found []time.Time
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		if free[d] {
			continue
		}
		found = append(found, d)
	}
	return found
}

// Expand returns every day from start to end (inclusive) as a set. If end
// is empty, only start is included.
func Expand(start, end string) map[time.Time]bool {
	first, ok := ParseDay(start)
	if !ok {
		return map[time.Time]bool{}
	}
	last, ok := ParseDay(end)
	if !ok {
		last = first
	}
	out := map[time.Time]bool{}
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		out[d] = true
	}
	return out
}
