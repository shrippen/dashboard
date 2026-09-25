package web

import (
	"embed"
	"html/template"
	"net/http"

	"dashboard/internal/i18n"
)

//go:embed templates/*.html
var templateFiles embed.FS

var templates = mustParse()

func mustParse() *template.Template {
	funcs := template.FuncMap{
		// t/money/etc. are bound per-render in Page() via t.Funcs, since
		// they close over the request's locale. These placeholders let the
		// templates parse before that binding happens.
		"t": func(string, ...any) string { return "" },
	}
	return template.Must(template.New("root").Funcs(funcs).ParseFS(templateFiles, "templates/*.html"))
}

// Page renders a full page with the common translation/formatting helpers
// bound to ctx.Locale.
func Page(w http.ResponseWriter, ctx Ctx, name string, status int, values map[string]any) error {
	locale := ctx.Locale
	funcs := template.FuncMap{
		"t":     func(key string, kv ...any) string { return i18n.T(key, locale, pairs(kv)) },
		"money": func(v float64) string { return i18n.Money(v, locale, i18n.DefaultCurrency) },
		"num":   func(v float64) string { return i18n.Num(v, locale, 0) },
		"day":   func(v any) string { return i18n.Day(v, locale) },
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
