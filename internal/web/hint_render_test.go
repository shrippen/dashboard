package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"andon/internal/enums"
	"andon/internal/metrics"
	"andon/internal/services/hints"
	"andon/internal/services/widgetlib"
	"andon/internal/widgets"
)

// The hint detail fragment renders history, people and states.
func TestHintDetailRenders(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx := Ctx{CSRF: "c", Locale: enums.LocaleDE}
	err := Deps{}.Page(rec, ctx, "hint_detail", http.StatusOK, map[string]any{
		"ThemeURL": "", "ID": int64(7), "States": workStates,
		"People":  []hints.Person{{ID: 2, Name: "Kim"}},
		"History": []hints.EventView{{Kind: enums.EventAcked, Actor: "Alex", Note: "erledigt", At: time.Now()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{`action="/hints/7/assign"`, ">Kim<", "in Arbeit", "quittiert · Alex", "erledigt"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q:\n%s", want, body)
		}
	}
}

// Freelance tiles render from their view data.
func TestFreelanceTilesRender(t *testing.T) {
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	cases := map[string]map[string]any{
		"widgets/cashflow": {"Path": "M0,1 L2,3", "W": 1000, "H": 160, "Low": -300.0, "LowDay": day, "End": 700.0, "Currency": "EUR",
			"Relative": false, "Events": []metrics.CashEvent{{Day: day, Label: "fixed", Amount: -800}}},
		"widgets/heatmap": {"Cells": []widgets.HeatCell{{Day: "2026-09-24", Level: 4, Hours: "6:40"}}, "W": 636, "H": 84, "Total": 6},
	}
	for name, view := range cases {
		rec := httptest.NewRecorder()
		frag := &widgetlib.Fragment{View: view, Slots: map[string]widgetlib.Slot{}}
		if err := (Deps{}).Page(rec, Ctx{Locale: enums.LocaleDE}, name, http.StatusOK, map[string]any{"ThemeURL": "", "Frag": frag}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := map[string]string{"widgets/cashflow": "Feste Kosten", "widgets/heatmap": `data-level="4"`}[name]
		if body := rec.Body.String(); !strings.Contains(body, want) {
			t.Fatalf("%s:\n%s", name, body)
		}
	}
}
