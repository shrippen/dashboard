package widgets

import (
	"testing"
	"time"

	"dashboard/internal/sources"
)

func TestDayBarWidensForEarlyWork(t *testing.T) {
	day := time.Date(2026, 9, 26, 0, 0, 0, 0, time.Local)
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	spans := []sources.KimaiSpan{{Begin: at(7, 30), End: at(9, 0)}, {Begin: at(13, 0)}}

	from, to, segs, now := dayBar(spans, at(15, 0))
	if from != 7 || to != 21 || len(segs) != 2 || segs[0].L < 3.5 || segs[0].L > 3.6 || !segs[1].Run || now != 57.14285714285714 {
		t.Fatalf("from %d to %d segs %+v now %v", from, to, segs, now)
	}
}

func TestClockSeconds(t *testing.T) {
	if got := clockSeconds(88*time.Minute + 16*time.Second); got != "1:28:16" {
		t.Fatalf("got %q", got)
	}
}
