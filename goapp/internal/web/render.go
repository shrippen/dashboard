package web

import (
	"embed"
	"html/template"
	"net/http"
	"time"

	"dashboard/internal/i18n"
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
		"t":     func(string, ...any) string { return "" },
		"money": func(float64, ...string) string { return "" },
		"num":   func(float64, ...int) string { return "" },
		"day":   func(any) string { return "" },
		"pct":   func(float64) string { return "" },
		"ago":   func(any) string { return "" },

		// barPct/tier are locale-independent (plain numbers/CSS keywords),
		// so unlike the above they're the real implementation, not a
		// placeholder.
		"barPct": barPct,
		"tier":   tier,
		"eqID":   func(a *int64, b int64) bool { return a != nil && *a == b },
	}
	return template.Must(template.New("root").Funcs(funcs).ParseFS(templateFiles, "templates/*.html"))
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

// Page renders a full page with the common translation/formatting helpers
// bound to ctx.Locale.
func Page(w http.ResponseWriter, ctx Ctx, name string, status int, values map[string]any) error {
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
		"num": func(v float64, digits ...int) string { return i18n.Num(v, locale, firstOr(digits, 0)) },
		"day": func(v any) string { return i18n.Day(v, locale) },
		"pct": func(v float64) string { return i18n.Num(v*pctScale, locale, 0) + " %" },
		"ago": func(v any) string { return i18n.Ago(asTimePtr(v), locale) },
	}

	data := map[string]any{"Ctx": ctx, "Who": ctx.Who, "CSRFField": CSRFField, "CSRFHeader": CSRFHeader}
	for k, v := range values {
		data[k] = v
	}

	page, err := templates.Clone()
	if err != nil {
		return err
	}
	page = page.Funcs(funcs)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	return page.ExecuteTemplate(w, name, data)
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
