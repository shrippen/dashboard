// Package i18n loads the dotted-key YAML catalogs and formats numbers,
// money and dates for German and English.
//
//	t("hint.kimai.timer_long", enums.LocaleEN, map[string]any{"hours": 11}) -> "Timer running for 11 h"
//
// Locale-aware formatting is deliberately simple: correct for de/en, not
// full CLDR.
package i18n

import (
	"embed"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"dashboard/internal/enums"
)

//go:embed catalogs/*.yml
var catalogFiles embed.FS

const (
	DefaultLocale   = enums.LocaleDE
	DefaultCurrency = "EUR"
)

var (
	catalogs = map[enums.Locale]map[string]string{}
	loaded   = false
)

func ensureLoaded() {
	if loaded {
		return
	}
	for _, loc := range []enums.Locale{enums.LocaleDE, enums.LocaleEN} {
		raw, err := catalogFiles.ReadFile("catalogs/" + string(loc) + ".yml")
		if err != nil {
			panic(fmt.Sprintf("i18n: missing catalog for %s: %v", loc, err))
		}
		var node map[string]any
		if err := yaml.Unmarshal(raw, &node); err != nil {
			panic(fmt.Sprintf("i18n: bad catalog for %s: %v", loc, err))
		}
		flat := map[string]string{}
		flatten("", node, flat)
		catalogs[loc] = flat
	}
	loaded = true
}

func flatten(prefix string, node any, out map[string]string) {
	switch v := node.(type) {
	case map[string]any:
		for key, value := range v {
			full := key
			if prefix != "" {
				full = prefix + "." + key
			}
			flatten(full, value, out)
		}
	default:
		out[prefix] = fmt.Sprint(v)
	}
}

var placeholder = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

// T looks up key in locale's catalog (falling back to DefaultLocale, then
// the raw key), substituting params. Unknown placeholders stay visible as
// "{name}" rather than failing.
func T(key string, locale enums.Locale, params map[string]any) string {
	ensureLoaded()
	text, ok := catalogs[locale][key]
	if !ok {
		text, ok = catalogs[DefaultLocale][key]
	}
	if !ok {
		text = key
	}
	if len(params) == 0 {
		return text
	}
	return placeholder.ReplaceAllStringFunc(text, func(m string) string {
		name := m[1 : len(m)-1]
		if v, ok := params[name]; ok {
			return fmt.Sprint(v)
		}
		return m
	})
}

// Typed formats a hint's typed params ({"$money": 12.5} -> "12,50 €", etc.)
// for use as T() params. The "key" entry (the message key itself) is
// dropped, since it is not a display parameter.
func Typed(params map[string]any, locale enums.Locale) map[string]any {
	out := make(map[string]any, len(params))
	for name, value := range params {
		if name == "key" {
			continue
		}
		out[name] = typedValue(value, locale)
	}
	return out
}

func typedValue(value any, locale enums.Locale) any {
	m, ok := value.(map[string]any)
	if !ok {
		return value
	}
	if v, ok := m["$money"]; ok {
		currency := DefaultCurrency
		if c, ok := m["currency"].(string); ok {
			currency = c
		}
		return Money(toFloat(v), locale, currency)
	}
	if v, ok := m["$day"]; ok {
		return Day(v, locale)
	}
	if v, ok := m["$num"]; ok {
		digits := 0
		if d, ok := m["digits"]; ok {
			digits = int(toFloat(d))
		}
		return Num(toFloat(v), locale, digits)
	}
	return value
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		f, _ := strconv.ParseFloat(fmt.Sprint(v), 64)
		return f
	}
}

// Pick returns the first supported language of an Accept-Language header,
// or DefaultLocale.
func Pick(acceptLanguage string) enums.Locale {
	for _, part := range strings.Split(acceptLanguage, ",") {
		code := strings.ToLower(strings.TrimSpace(strings.Split(part, ";")[0]))
		if len(code) > 2 {
			code = code[:2]
		}
		switch enums.Locale(code) {
		case enums.LocaleDE:
			return enums.LocaleDE
		case enums.LocaleEN:
			return enums.LocaleEN
		}
	}
	return DefaultLocale
}

// Money formats an amount with a currency symbol/suffix per locale.
func Money(value float64, locale enums.Locale, currency string) string {
	symbol := currencySymbol(currency)
	amount := groupedDecimal(value, locale, 2)
	if locale == enums.LocaleDE {
		return amount + " " + symbol
	}
	return symbol + amount
}

func currencySymbol(currency string) string {
	if currency == "EUR" {
		return "€"
	}
	return currency
}

// Num formats a plain number with locale-specific separators.
func Num(value float64, locale enums.Locale, digits int) string {
	return groupedDecimal(value, locale, digits)
}

// groupedDecimal formats value with thousands grouping and the locale's
// decimal separator (",", "." for de; "." for en).
func groupedDecimal(value float64, locale enums.Locale, digits int) string {
	neg := value < 0
	if neg {
		value = -value
	}
	whole := strconv.FormatFloat(value, 'f', digits, 64)
	intPart, fracPart, hasFrac := whole, "", false
	if i := strings.IndexByte(whole, '.'); i >= 0 {
		intPart, fracPart, hasFrac = whole[:i], whole[i+1:], true
	}

	grouped := groupThousands(intPart, thousandsSep(locale))
	out := grouped
	if hasFrac {
		out += decimalSep(locale) + fracPart
	}
	if neg {
		out = "-" + out
	}
	return out
}

func thousandsSep(locale enums.Locale) string {
	if locale == enums.LocaleDE {
		return "."
	}
	return ","
}

func decimalSep(locale enums.Locale) string {
	if locale == enums.LocaleDE {
		return ","
	}
	return "."
}

func groupThousands(digits, sep string) string {
	n := len(digits)
	if n <= 3 {
		return digits
	}
	var b strings.Builder
	lead := n % 3
	if lead > 0 {
		b.WriteString(digits[:lead])
	}
	for i := lead; i < n; i += 3 {
		if b.Len() > 0 {
			b.WriteString(sep)
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// Day formats a date (time.Time or an ISO-8601 string/prefix) as a medium
// localized date, or "" for nil/empty input.
func Day(value any, locale enums.Locale) string {
	t, ok := asDate(value)
	if !ok {
		return ""
	}
	if locale == enums.LocaleDE {
		return t.Format("02.01.2006")
	}
	return t.Format("Jan 2, 2006")
}

// Weekday formats a date's abbreviated weekday name.
func Weekday(value any, locale enums.Locale) string {
	t, ok := asDate(value)
	if !ok {
		return ""
	}
	if locale == enums.LocaleDE {
		return germanWeekdays[t.Weekday()]
	}
	return t.Format("Mon")
}

var germanWeekdays = map[time.Weekday]string{
	time.Monday: "Mo", time.Tuesday: "Di", time.Wednesday: "Mi", time.Thursday: "Do",
	time.Friday: "Fr", time.Saturday: "Sa", time.Sunday: "So",
}

// Ago formats the (signed) duration between value and now as a relative
// phrase, e.g. "in 3 days" / "vor 3 Tagen".
func Ago(value *time.Time, locale enums.Locale) string {
	if value == nil {
		return ""
	}
	d := time.Until(*value)
	when := past
	if d > 0 {
		when = future
	} else {
		d = -d
	}
	unit, n := roundUnit(d)
	return relativePhrase(n, unit, when, locale)
}

func roundUnit(d time.Duration) (string, int) {
	switch {
	case d < time.Minute:
		return "second", round(d.Seconds())
	case d < time.Hour:
		return "minute", round(d.Minutes())
	case d < 24*time.Hour:
		return "hour", round(d.Hours())
	case d < 30*24*time.Hour:
		return "day", round(d.Hours() / 24)
	case d < 365*24*time.Hour:
		return "month", round(d.Hours() / 24 / 30)
	default:
		return "year", round(d.Hours() / 24 / 365)
	}
}

func round(f float64) int { return int(f + 0.5) }

// germanUnits holds {singular, dative plural} — the case "vor"/"in" take.
var germanUnits = map[string][2]string{
	"second": {"Sekunde", "Sekunden"}, "minute": {"Minute", "Minuten"},
	"hour": {"Stunde", "Stunden"}, "day": {"Tag", "Tagen"},
	"month": {"Monat", "Monaten"}, "year": {"Jahr", "Jahren"},
}

// tense says whether a relative phrase points back ("vor") or ahead ("in").
type tense int

const (
	past tense = iota
	future
)

func relativePhrase(n int, unit string, when tense, locale enums.Locale) string {
	if locale == enums.LocaleDE {
		word := germanUnits[unit][0]
		if n != 1 {
			word = germanUnits[unit][1]
		}
		if when == future {
			return fmt.Sprintf("in %d %s", n, word)
		}
		return fmt.Sprintf("vor %d %s", n, word)
	}

	word := unit
	if n != 1 {
		word += "s"
	}
	if when == future {
		return fmt.Sprintf("in %d %s", n, word)
	}
	return fmt.Sprintf("%d %s ago", n, word)
}

func asDate(value any) (time.Time, bool) {
	switch v := value.(type) {
	case nil:
		return time.Time{}, false
	case time.Time:
		return v, true
	case *time.Time:
		if v == nil {
			return time.Time{}, false
		}
		return *v, true
	case string:
		if v == "" {
			return time.Time{}, false
		}
		s := v
		if len(s) > 10 {
			s = s[:10]
		}
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return time.Time{}, false
		}
		return t, true
	default:
		return time.Time{}, false
	}
}
