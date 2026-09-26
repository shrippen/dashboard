package widgets

// greeting   hello by time of day, date and clock, the weather ahead and
//            what changed since yesterday evening (hints, updates)
//
//	┌──────────────────────┬──────────────┬─────────────────────────┐
//	│ Guten Tag, Arian.    │ 17° bewölkt  │ Seit gestern 18:00      │
//	│ Samstag · 14:32      │ ▂▅▇▃ 4 Tage  │ ■ 2 neue Hinweise …     │
//	└──────────────────────┴──────────────┴─────────────────────────┘

import (
	"time"

	"dashboard/internal/sources"
)

// GreetingSlot names the viewer's greeting data among a widget's results.
const GreetingSlot = "greeting"

const (
	greetingSinceHour = 18
	greetingDays      = 4
	greetingUpdates   = 4
	// forecastFloor keeps the coldest day's bar visible.
	forecastFloor = 20
	pctFull       = 100
)

// Change kinds a greeting summarizes (from the timeline).
const (
	ChangeOpened   = "opened"
	ChangeReopened = "reopened"
	ChangeResolved = "resolved"
	ChangeUpdate   = "update"
)

// GreetingConfig is the "greeting" widget's config.
type GreetingConfig struct {
	Label     string
	Lat, Lon  float64
	Timezone  string
	SinceHour int
}

func decodeGreeting(raw map[string]any) any {
	tz := asString(raw["timezone"])
	if tz == "" {
		tz = defaultTimezone
	}
	return GreetingConfig{Label: asString(raw["label"]), Lat: asFloat(raw["lat"]), Lon: asFloat(raw["lon"]),
		Timezone: tz, SinceHour: clampInt(asInt(raw["since_hour"], greetingSinceHour), 0, 23)}
}

// GreetingChange is one timeline entry a greeting may mention.
type GreetingChange struct {
	Kind, Subject, Detail string
	At                    time.Time
}

// GreetingData is what the widgets service adds for a greeting: the
// viewer and what changed since Since.
type GreetingData struct {
	Name      string
	OpenHints int
	HintLevel int
	Since     time.Time
	Changes   []GreetingChange
}

// GreetingSince is yesterday at hour in now's location: the start of
// "what changed since yesterday evening".
func GreetingSince(now time.Time, hour int) time.Time {
	y := now.AddDate(0, 0, -1)
	return time.Date(y.Year(), y.Month(), y.Day(), hour, 0, 0, 0, now.Location())
}

// GreetingLine is one "since yesterday" line: a catalog key with a count,
// or a literal update text.
type GreetingLine struct {
	Tier string // "red", "green", "blue": the square in front
	Key  string // catalog key, "" for Text
	N    int
	Text string
}

// ForecastDay is one bar of the greeting's forecast.
type ForecastDay struct {
	Day      string
	Code     int
	Max, Min float64
	Height   int // bar height in percent of the warmest day
}

// dayPart: 5–11 morning, 11–18 day, otherwise evening.
func dayPart(hour int) string {
	switch {
	case hour >= 5 && hour < 11:
		return "morning"
	case hour >= 11 && hour < 18:
		return "day"
	default:
		return "evening"
	}
}

func greetingView(cfgAny any, results map[string]any, _ ViewCtx) map[string]any {
	cfg := cfgAny.(GreetingConfig)
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		loc = time.Local
	}
	out := map[string]any{"Part": dayPart(time.Now().In(loc).Hour()), "Label": cfg.Label, "Timezone": cfg.Timezone, "SinceHour": cfg.SinceHour,
		"HasWeather": cfg.Lat != 0 || cfg.Lon != 0}

	if w, ok := results["weather"].(*sources.WeatherResult); ok && w != nil {
		out["Temp"], out["Code"], out["Wind"], out["Days"] = w.Temp, w.Code, w.Wind, forecast(w.Days)
	}
	if g, ok := results[GreetingSlot].(*GreetingData); ok && g != nil {
		out["Name"], out["OpenHints"], out["HintLevel"], out["Lines"] = g.Name, g.OpenHints, g.HintLevel, greetingLines(g.Changes)
	}
	return out
}

// forecast scales the next days' highs into bars:
//
//	17° 19° 15° 13°  →  heights 73 100 46 20 (coldest kept at the floor)
func forecast(days []sources.WeatherDay) []ForecastDay {
	if len(days) > greetingDays {
		days = days[:greetingDays]
	}
	if len(days) == 0 {
		return nil
	}

	low, high := days[0].Max, days[0].Max
	for _, d := range days {
		low, high = min(low, d.Max), max(high, d.Max)
	}
	out := make([]ForecastDay, len(days))
	for i, d := range days {
		height := pctFull
		if high > low {
			height = forecastFloor + int((d.Max-low)/(high-low)*float64(pctFull-forecastFloor))
		}
		out[i] = ForecastDay{Day: d.Day, Code: d.Code, Max: d.Max, Min: d.Min, Height: height}
	}
	return out
}

// greetingLines sums hint changes and lists the latest updates:
//
//	opened, reopened, resolved, update A, update B  →
//	"2 new hints" · "1 hint resolved" · "A" · "B"
func greetingLines(changes []GreetingChange) []GreetingLine {
	opened, resolved := 0, 0
	var updates []GreetingLine
	for _, c := range changes {
		switch c.Kind {
		case ChangeOpened, ChangeReopened:
			opened++
		case ChangeResolved:
			resolved++
		case ChangeUpdate:
			if len(updates) < greetingUpdates {
				updates = append(updates, GreetingLine{Tier: "blue", Text: c.Subject + " " + c.Detail})
			}
		}
	}

	var out []GreetingLine
	if opened > 0 {
		out = append(out, GreetingLine{Tier: "red", Key: "greeting.new_hints", N: opened})
	}
	if resolved > 0 {
		out = append(out, GreetingLine{Tier: "green", Key: "greeting.resolved_hints", N: resolved})
	}
	return append(out, updates...)
}

func init() {
	Register(WidgetType{Key: "greeting", Decode: decodeGreeting, Template: "widgets/greeting",
		Category: CategoryStart, RefreshS: 10 * 60, View: greetingView, Extra: ExtraGreeting,
		Queries: func(cfgAny any) []Query {
			cfg := cfgAny.(GreetingConfig)
			if cfg.Lat == 0 && cfg.Lon == 0 {
				return nil
			}
			return []Query{{Name: "weather", Source: "open_meteo", Params: map[string]any{"lat": cfg.Lat, "lon": cfg.Lon}}}
		}})
}
