package widgets

import (
	"testing"
	"time"

	"andon/internal/sources"
)

func TestCustomAPIPaths(t *testing.T) {
	cfg := decodeCustomAPI(map[string]any{"fields": "Temp = main.temp\nFirst = list.0.name\nmissing.x\n"})
	body := map[string]any{"main": map[string]any{"temp": 21.5}, "list": []any{map[string]any{"name": "a"}}}
	rows := customAPIView(cfg, map[string]any{"body": &sources.JSONResult{Body: body}}, ViewCtx{})["Rows"].([]APIValue)
	if len(rows) != 3 || rows[0].Value != "21.5" || rows[1].Value != "a" || !rows[2].Missing || rows[2].Label != "missing.x" {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestListDropsUnsafeLinks(t *testing.T) {
	cfg := decodeList(map[string]any{"entries": "Wiki | https://wiki\nNotiz\nBad | javascript:alert(1)\n"}).(ListConfig)
	if len(cfg.Entries) != 3 || cfg.Entries[0].URL != "https://wiki" || cfg.Entries[1].URL != "" || cfg.Entries[2].URL != "" {
		t.Fatalf("entries: %+v", cfg.Entries)
	}
}

func TestCalendarAndBoardViews(t *testing.T) {
	start := time.Date(2026, 9, 28, 7, 0, 0, 0, time.UTC)
	events := &sources.CalendarResult{Events: []sources.Event{
		{Start: start, Title: "Standup"}, {Start: start, AllDay: true, Title: "Frei"}, {Start: start, Title: "Cut"},
	}}
	rows := calendarView(CalendarConfig{Limit: 2}, map[string]any{"events": events}, ViewCtx{})["Rows"].([]CalRow)
	if len(rows) != 2 || rows[0].Time != "09:00" || rows[1].Time != "" {
		t.Fatalf("calendar: %+v", rows)
	}

	board := &sources.BoardResult{Stop: "Hbf", Movements: []sources.Movement{{When: start, Line: "S 1", Delay: 3}}}
	view := transitView(TransitConfig{Limit: 5}, map[string]any{"board": board}, ViewCtx{})
	if got := view["Rows"].([]MoveRow); len(got) != 1 || got[0].Time != "09:00" || got[0].Delay != 3 {
		t.Fatalf("board: %+v", got)
	}
}

func TestHolidaysView(t *testing.T) {
	days := &sources.HolidaysResult{Days: []sources.Holiday{{Day: "2026-10-03", Name: "Einheit"}, {Day: "2026-12-25", Name: "Weihnachten"}}}
	rows := holidaysView(HolidaysConfig{Limit: 1}, map[string]any{"days": days}, ViewCtx{Today: "2026-09-25"})["Rows"].([]HolidayRow)
	if len(rows) != 1 || rows[0].In != 8 {
		t.Fatalf("holidays: %+v", rows)
	}
}

func TestGlancesChartView(t *testing.T) {
	data := &sources.GlancesHistory{Metric: "cpu", Samples: []sources.Sample{{Value: 0}, {Value: 100}}}
	view := glancesChartView(nil, map[string]any{"history": data}, ViewCtx{})
	if view["Path"] != "M0.0,155.0 L1000.0,5.0" || view["Now"] != 100.0 {
		t.Fatalf("view: %+v", view)
	}
}

func TestHeatmapLevels(t *testing.T) {
	data := &sources.KimaiDataset{Timesheets: []sources.KimaiSheet{{Begin: "2026-09-24T09:00:00+0200", Minutes: 400}}}
	view := heatmapView(nil, map[string]any{"data": data}, ViewCtx{Today: "2026-09-25"})
	cells := view["Cells"].([]HeatCell)
	var found bool
	for _, c := range cells {
		if c.Day == "2026-09-24" {
			found = c.Level == 4 && c.Hours == "6:40"
		}
	}
	if !found || cells[len(cells)-1].Day != "2026-09-25" || view["Total"] != 6 {
		t.Fatalf("heatmap: last %+v total %v", cells[len(cells)-1], view["Total"])
	}
}
