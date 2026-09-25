package sources_test

import (
	"strings"
	"testing"
	"time"

	"dashboard/internal/sources"
)

const icalText = "BEGIN:VCALENDAR\r\n" +
	"BEGIN:VEVENT\r\nUID:standup\r\nDTSTART;TZID=Europe/Berlin:20260928T090000\r\n" +
	"RRULE:FREQ=WEEKLY;BYDAY=MO,WE;COUNT=4\r\nEXDATE;TZID=Europe/Berlin:20260930T090000\r\n" +
	"SUMMARY:Stand\r\n up\r\nEND:VEVENT\r\n" +
	// The 5 October instance moved to 10:00.
	"BEGIN:VEVENT\r\nUID:standup\r\nRECURRENCE-ID;TZID=Europe/Berlin:20261005T090000\r\n" +
	"DTSTART;TZID=Europe/Berlin:20261005T100000\r\nSUMMARY:Standup (später)\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:holiday\r\nDTSTART;VALUE=DATE:20261003\r\nSUMMARY:Tag der Einheit\\, frei\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:gone\r\nDTSTART:20261001T080000Z\r\nSTATUS:CANCELLED\r\nSUMMARY:Gone\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:monthly\r\nDTSTART:20260131T120000Z\r\nRRULE:FREQ=MONTHLY;UNTIL=20261231T000000Z\r\nSUMMARY:Ultimo\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestICalOccurrences(t *testing.T) {
	from := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	var got []string
	for _, e := range sources.Occurrences(icalText, from, to) {
		got = append(got, e.Start.UTC().Format("01-02T15:04")+" "+e.Title)
	}
	// Wed 30.09. is excluded, the 05.10. instance moved to 10:00, the
	// cancelled event and months without a 31st are skipped.
	want := []string{
		"09-28T07:00 Standup",
		"10-02T22:00 Tag der Einheit, frei",
		"10-05T08:00 Standup (später)",
		"10-07T07:00 Standup",
	}
	if joined := strings.Join(got, "\n"); joined != strings.Join(want, "\n") {
		t.Fatalf("occurrences:\n%s", joined)
	}
}
