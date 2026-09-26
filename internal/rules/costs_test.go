package rules_test

import (
	"fmt"
	"testing"
	"time"

	"andon/internal/rules"
	"andon/internal/sources"
)

func TestUnusedServiceAndReplacement(t *testing.T) {
	today := day("2026-09-25")
	komodo := &sources.KomodoDataset{Stacks: []sources.KStack{{Name: "paperless-ai", State: "running"}, {Name: "immich", State: "running"}, {Name: "secret", State: "running"}}}
	links := []rules.Link{
		{Title: "Paperless-AI", URL: "https://ai.lan", LastClick: today.AddDate(0, 0, -120)},
		{Title: "Immich", URL: "https://photos.lan", LastClick: today.AddDate(0, 0, -1)},
	}
	env := crossEnv("2026-09-25", map[string]any{"komodo": komodo, rules.LinksDataset: links})
	if got := run(t, "system.unused_service", nil, env); len(got) != 1 || got[0].Params["name"] != "paperless-ai" {
		t.Fatalf("unused: %+v", got)
	}

	snipe := &sources.SnipeDataset{Assets: []sources.SnipeAsset{{Name: "Server Dell", PurchaseDate: "2017-03-01", PurchaseCost: 900}}}
	hass := &sources.HassDataset{Entities: []sources.Entity{{ID: "sensor.dell_power", Name: "Dell Leistung", State: "68", Unit: "W", DeviceClass: "power"}}}
	env = crossEnv("2026-09-25", map[string]any{"snipeit": snipe, "homeassistant": hass})
	env.Settings = map[string]any{"homelab": map[string]any{"power_price": 0.35}}
	got := run(t, "snipe.replace_worth", nil, env)
	if len(got) != 1 || got[0].Params["months"].(int) > 36 {
		t.Fatalf("replace: %+v", got)
	}
}

func TestShiftJobsAndWeather(t *testing.T) {
	tibber := &sources.TibberDataset{Currency: "EUR"}
	for h := range 24 {
		price := 0.25
		if h >= 17 && h <= 20 {
			price = 0.45
		}
		tibber.Prices = append(tibber.Prices, sources.PricePoint{At: time.Date(2026, 9, 25, h, 0, 0, 0, time.Local), Total: price})
	}
	borg := &sources.BorgDataset{Clients: []sources.BorgClient{{Name: "nas", LastBackup: time.Date(2026, 9, 24, 18, 0, 0, 0, time.Local)}}}
	env := crossEnv("2026-09-25", map[string]any{"tibber": tibber, "borgbackup": borg})
	if got := run(t, "energy.shift_jobs", nil, env); len(got) != 1 || got[0].Params["hour"] != 18 {
		t.Fatalf("shift: %+v", got)
	}

	// 20 days where use follows the cold, then a week using 40 % more.
	for i := range 27 {
		temp := 5 + float64(i%10)
		kwh := 6 + 0.5*(15-temp)
		if i >= 20 {
			kwh *= 1.4
		}
		tibber.Days = append(tibber.Days, sources.EnergyDay{Day: fmt.Sprintf("2026-09-%02d", i+1), KWh: kwh, TempC: temp, HasTemp: true})
	}
	env = crossEnv("2026-09-28", map[string]any{"tibber": tibber})
	if got := run(t, "energy.weather_adjusted", nil, env); len(got) != 1 {
		t.Fatalf("weather: %+v", got)
	}
}
