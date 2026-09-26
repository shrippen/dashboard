package widgets

import (
	"testing"
	"time"

	"dashboard/internal/sources"
)

func TestDayPart(t *testing.T) {
	cases := map[int]string{4: "evening", 5: "morning", 10: "morning", 11: "day", 17: "day", 18: "evening", 23: "evening"}
	for hour, want := range cases {
		if got := dayPart(hour); got != want {
			t.Errorf("dayPart(%d) = %q, want %q", hour, got, want)
		}
	}
}

func TestForecastScalesHighs(t *testing.T) {
	days := []sources.WeatherDay{{Max: 17}, {Max: 19}, {Max: 15}, {Max: 13}, {Max: 30}}
	got := forecast(days)
	want := []int{73, 100, 46, 20}
	if len(got) != len(want) {
		t.Fatalf("got %d days, want %d", len(got), len(want))
	}
	for i, d := range got {
		if d.Height != want[i] {
			t.Errorf("day %d height %d, want %d", i, d.Height, want[i])
		}
	}
}

func TestGreetingLinesSumHintsAndListUpdates(t *testing.T) {
	changes := []GreetingChange{
		{Kind: ChangeOpened}, {Kind: ChangeUpdate, Subject: "Nextcloud", Detail: "31.0.1 → 31.0.2"},
		{Kind: ChangeReopened}, {Kind: ChangeResolved},
	}
	got := greetingLines(changes)
	if len(got) != 3 || got[0].N != 2 || got[0].Tier != "red" || got[1].N != 1 || got[2].Text != "Nextcloud 31.0.1 → 31.0.2" {
		t.Fatalf("lines: %+v", got)
	}
}

func TestGreetingSinceIsYesterdayEvening(t *testing.T) {
	loc := time.FixedZone("CEST", 2*60*60)
	got := GreetingSince(time.Date(2026, 9, 26, 9, 30, 0, 0, loc), 18)
	if want := time.Date(2026, 9, 25, 18, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
