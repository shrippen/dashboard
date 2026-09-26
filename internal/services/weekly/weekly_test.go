package weekly_test

import (
	"context"
	"testing"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/model"
	data "dashboard/internal/repos/data"
	"dashboard/internal/services/weekly"
	"dashboard/internal/testkit"
)

// The story counts this week's opened and resolved hints of the caller,
// not another user's personal ones.
func TestStoryCountsOwnHintEvents(t *testing.T) {
	d := testkit.DB(t)
	who, space := testkit.User(t, d, "a@b.c", enums.RoleUser)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	stranger, _ := testkit.User(t, d, "x@y.z", enums.RoleUser)
	other := stranger.UserID

	add := func(print string, owner *int64, kinds ...enums.HintEvent) {
		h := &model.Hint{SpaceID: space, UserID: owner, Fingerprint: print, Rule: "kimai.gap", Severity: enums.SeverityWarn,
			Params: map[string]any{}, FirstSeen: now, LastSeen: now}
		if err := data.AddHint(d, h); err != nil {
			t.Fatal(err)
		}
		for _, k := range kinds {
			if err := data.AddHintEvent(d, &model.HintEvent{HintID: h.ID, Kind: k, At: now.Add(-time.Hour)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("a", nil, enums.EventOpened, enums.EventResolved)
	add("b", &who.UserID, enums.EventOpened)
	add("c", &other, enums.EventOpened)

	lines, err := weekly.Story(context.Background(), d, who, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Key != "hints" || lines[0].Params["opened"] != 2 || lines[0].Params["resolved"] != 1 {
		t.Fatalf("lines: %+v", lines)
	}
}
