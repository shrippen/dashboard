package web

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/i18n"
	"dashboard/internal/services/access"
	"dashboard/internal/services/themes"
)

//go:embed templates/*.html
var templateFiles embed.FS

const pctScale = 100

var templates = mustParse()

func mustParse() *template.Template {
	funcs := template.FuncMap{
		// t/money/etc. are bound per-render in Page() via t.Funcs, since
		// they close over the request's locale. These placeholders let the
		// templates parse before that binding happens.
		"t":         func(string, ...any) string { return "" },
		"money":     func(float64, ...string) string { return "" },
		"num":       func(float64, ...int) string { return "" },
		"day":       func(any) string { return "" },
		"pct":       func(float64) string { return "" },
		"ago":       func(any) string { return "" },
		"clockDate": func(string) string { return "" },
		"tt":        func(string, map[string]any) string { return "" },
		"fragment":  func(*tileBody) (template.HTML, error) { return "", nil },

		// barPct/tier are locale-independent (plain numbers/CSS keywords),
		// so unlike the above they're the real implementation, not a
		// placeholder.
		"barPct":      barPct,
		"tier":        tier,
		"eqID":        func(a *int64, b int64) bool { return a != nil && *a == b },
		"weatherKind": weatherKind,
		"clockNow":    clockNow,
		"dict":        dict,
		"monogram":    monogram,
		"deref":       func(p *enums.TeamRole) enums.TeamRole { return *p },
		"dataURI":     dataURI,
		"mainRuns":    mainRuns,
		"credShape":   func(s enums.ServiceType) string { return string(credShapeOf(s)) },
	}
	return template.Must(template.New("root").Funcs(funcs).ParseFS(templateFiles, "templates/*.html"))
}

// dataURI marks an inlined image (from the image source) as a safe URL;
// anything else becomes empty.
func dataURI(s string) template.URL {
	if !strings.HasPrefix(s, "data:image/") {
		return ""
	}
	return template.URL(s) //nolint:gosec // built server-side from an image/* response
}

// barPct turns a ratio (e.g. 0.45, or 1.2 over budget) into a 0-100 percent
// for a progress bar's width.
func barPct(ratio float64) int {
	switch {
	case ratio < 0:
		return 0
	case ratio > 1:
		return 100
	default:
		return int(ratio*100 + 0.5)
	}
}

// weatherThresholds maps a WMO weather code's upper bound to its icon key
// (e.g. code 61 -> "rain"): the first threshold the code doesn't exceed.
var weatherThresholds = []struct {
	max  int
	kind string
}{
	{0, "clear"}, {3, "cloudy"}, {48, "fog"}, {57, "drizzle"}, {67, "rain"},
	{77, "snow"}, {82, "showers"}, {86, "snow"}, {99, "thunder"},
}

func weatherKind(code int) string {
	for _, t := range weatherThresholds {
		if code <= t.max {
			return t.kind
		}
	}
	return "unknown"
}

// tier is a progress bar's colour band: red at/over budget, yellow near it.
func tier(ratio float64) string {
	switch {
	case ratio >= 1:
		return "red"
	case ratio >= 0.8:
		return "yellow"
	default:
		return "green"
	}
}

// clockNow formats the current time in an IANA timezone ("" or unknown ->
// server-local). Locale-independent (24h HH:MM[:SS]), unlike clockDate.
func clockNow(tz string, seconds bool) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.Local
	}
	if seconds {
		return time.Now().In(loc).Format("15:04:05")
	}
	return time.Now().In(loc).Format("15:04")
}

func clockDate(tz string, locale enums.Locale) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	return i18n.Weekday(now, locale) + ", " + i18n.Day(now, locale)
}

// Page renders a full page with the common translation/formatting helpers
// bound to ctx.Locale, and the active shrippen theme's stylesheet (unless
// the caller already set "ThemeURL" itself — the board page picks its own
// board/space-scoped theme).
func (d Deps) Page(w http.ResponseWriter, ctx Ctx, name string, status int, values map[string]any) error {
	locale := ctx.Locale
	funcs := template.FuncMap{
		"t": func(key string, kv ...any) string { return i18n.T(key, locale, pairs(kv)) },
		"money": func(v float64, currency ...string) string {
			c := i18n.DefaultCurrency
			if len(currency) > 0 && currency[0] != "" {
				c = currency[0]
			}
			return i18n.Money(v, locale, c)
		},
		"num":       func(v float64, digits ...int) string { return i18n.Num(v, locale, firstOr(digits, 0)) },
		"day":       func(v any) string { return i18n.Day(v, locale) },
		"pct":       func(v float64) string { return i18n.Num(v*pctScale, locale, 0) + " %" },
		"ago":       func(v any) string { return i18n.Ago(asTimePtr(v), locale) },
		"clockDate": func(tz string) string { return clockDate(tz, locale) },
		// tt translates with typed params ({"$money": 12.5} -> "12,50 €").
		"tt": func(key string, params map[string]any) string {
			return i18n.T(key, locale, i18n.Typed(params, locale))
		},
	}

	data := map[string]any{"Ctx": ctx, "Who": ctx.Who, "CSRFField": CSRFField, "CSRFHeader": CSRFHeader}
	for k, v := range values {
		data[k] = v
	}
	if ctx.Who != nil {
		d.addNav(data, ctx.Who)
	}
	if _, ok := data["ThemeURL"]; !ok {
		url, err := d.themeURL(ctx.Who, nil, nil)
		if err != nil {
			return err
		}
		data["ThemeURL"] = url
	}

	page, err := templates.Clone()
	if err != nil {
		return err
	}
	page = page.Funcs(funcs)

	// fragment renders a tile body inside the page, with the page's data
	// plus the fragment, as /widget-fragments/{id} would answer.
	page = page.Funcs(template.FuncMap{"fragment": func(body *tileBody) (template.HTML, error) {
		own := make(map[string]any, len(data)+2)
		for k, v := range data {
			own[k] = v
		}
		own["Frag"], own["PlacementID"] = body.Frag, body.PlacementID
		var buf bytes.Buffer
		if err := page.ExecuteTemplate(&buf, body.Template, own); err != nil {
			return "", err
		}
		return template.HTML(buf.String()), nil //nolint:gosec // output of our own escaping templates
	}})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	return page.ExecuteTemplate(w, name, data)
}

// themeURL resolves the CSS URL of the theme active for who (nil for an
// anonymous page — login, setup — which gets the instance default),
// optionally narrowed to a board's or space's own theme choice.
func (d Deps) themeURL(who *access.Principal, boardTheme, spaceID *int64) (string, error) {
	themeID, err := themes.Active(d.DB, who, boardTheme, spaceID)
	if err != nil {
		return "", err
	}
	_, version, err := themes.Stylesheet(d.DB, themeID)
	if err != nil {
		return "", err
	}
	return "/theme/" + strconv.FormatInt(themeID, 10) + ".css?v=" + strconv.Itoa(version), nil
}

// dict packs key/value pairs into a map, so a sub-template invoked with
// {{template "name" dict "A" .X "B" $}} can take more than the single
// pipeline argument {{template}} otherwise allows.
func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, errors.New("dict: odd number of arguments")
	}
	out := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			return nil, errors.New("dict: keys must be strings")
		}
		out[key] = kv[i+1]
	}
	return out, nil
}

func firstOr[T any](vals []T, def T) T {
	if len(vals) > 0 {
		return vals[0]
	}
	return def
}

// asTimePtr turns a slot's OkAt (a zero time.Time when unset) into the
// pointer i18n.Ago expects.
func asTimePtr(v any) *time.Time {
	t, ok := v.(time.Time)
	if !ok || t.IsZero() {
		return nil
	}
	return &t
}

// pairs turns a flat ["key", value, "key2", value2, ...] slice into a map,
// so templates can write {{t "hint.x" "hours" 3}}.
func pairs(kv []any) map[string]any {
	out := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		if key, ok := kv[i].(string); ok {
			out[key] = kv[i+1]
		}
	}
	return out
}

// monogram is the fallback icon text: initials of two words, else the
// first two letters ("Invoice Ninja" -> "IN", "Kimai" -> "KI").
func monogram(title string) string {
	words := strings.Fields(strings.ReplaceAll(title, "-", " "))
	var letters []rune
	if len(words) > 1 {
		for _, w := range words[:2] {
			letters = append(letters, []rune(w)[0])
		}
	} else {
		letters = []rune(title)
		if len(letters) > 2 {
			letters = letters[:2]
		}
	}
	if len(letters) == 0 {
		return "?"
	}
	return strings.ToUpper(string(letters))
}
