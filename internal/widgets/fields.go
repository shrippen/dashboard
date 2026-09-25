package widgets

import (
	"sort"
	"strconv"
	"strings"
)

// Widget config forms are generated from a field list per type:
//
//	rss: [{url text required} {limit number} {summary check}]
//	  form {"cfg.url": ..., "cfg.limit": "8"} → config {"url": ..., "limit": 8}
//
// Dotted keys nest: "info.connection" ↔ {"info": {"connection": ...}}.

// Input is how a config field is edited.
type Input string

const (
	InputText    Input = "text"
	InputArea    Input = "textarea"
	InputNumber  Input = "number"
	InputCheck   Input = "checkbox"
	InputSelect  Input = "select"
	InputList    Input = "list"    // comma separated strings
	InputNumbers Input = "numbers" // comma separated integers
	InputConn    Input = "connection"
	InputLinks   Input = "links"   // one "title | url | icon" per line
	InputHeaders Input = "headers" // one "Name: value" per line
	InputSecret  Input = "secret"  // write-only: stored encrypted, never shown
)

const (
	linkSep     = "|"
	headerSep   = ":"
	secretClear = "-" // matches util.SecretClear: drop a stored secret
)

// FormPrefix marks config fields in a form ("cfg.url").
const FormPrefix = "cfg."

const listSep = ","

// Field is one editable config value.
type Field struct {
	Key      string
	Input    Input
	Options  []string
	Required bool
	Default  any
}

func sel(key string, def string, options ...string) Field {
	return Field{Key: key, Input: InputSelect, Options: options, Default: def}
}

var fieldsByType = map[string][]Field{
	"link": {
		{Key: "url", Input: InputText, Required: true},
		{Key: "description", Input: InputArea},
		{Key: "icon", Input: InputText},
		sel("target", "newtab", "newtab", "sametab"),
		sel("status", "http", "http", "off"),
		{Key: "status_url", Input: InputText},
		{Key: "accept", Input: InputNumbers},
		{Key: "insecure", Input: InputCheck},
		{Key: "hotkey", Input: InputText},
		{Key: "info.connection", Input: InputConn},
		{Key: "tags", Input: InputList},
		{Key: "items", Input: InputLinks},
		sel("color", "none", "none", "yellow", "green", "red", "blue", "purple", "aqua", "orange"),
		{Key: "headers", Input: InputHeaders},
	},
	"rss":           {{Key: "url", Input: InputText, Required: true}, {Key: "limit", Input: InputNumber, Default: 8}, {Key: "summary", Input: InputCheck}},
	"clock":         {{Key: "timezones", Input: InputList, Default: []any{defaultTimezone}}, {Key: "seconds", Input: InputCheck}, {Key: "date", Input: InputCheck, Default: true}},
	"weather":       {{Key: "label", Input: InputText}, {Key: "lat", Input: InputNumber, Required: true}, {Key: "lon", Input: InputNumber, Required: true}},
	"iframe":        {{Key: "url", Input: InputText, Required: true}, {Key: "height", Input: InputNumber, Default: 320}},
	"note":          {{Key: "text", Input: InputArea}},
	"image":         {{Key: "url", Input: InputText, Required: true}, {Key: "height", Input: InputNumber, Default: 240}, {Key: "link", Input: InputText}},
	"rates":         {{Key: "base", Input: InputText, Default: "EUR"}, {Key: "symbols", Input: InputList, Default: []any{"USD", "CHF", "GBP"}}},
	"monitors":      {},
	"hass":          {{Key: "entities", Input: InputList, Required: true}},
	"sysinfo":       {},
	"glances_chart": {sel("metric", "cpu", "cpu", "mem", "load", "swap"), {Key: "points", Input: InputNumber, Default: defaultGlancesPoints}},
	"public_ip":     {},
	"kpi": {sel("metric", "hours_today", "hours_today", "hours_week", "hours_month", "utilization", "unbilled",
		"revenue_ytd", "revenue_month", "open_amount", "overdue_amount", "vat_liability", "tax_reserve",
		"asset_value", "assets_ready", "revenue_forecast", "cash_30", "liquidity_30", "effective_rate", "net_worth", "cash")},
	"table": {sel("table", "open_invoices", "open_invoices", "unbilled", "budgets", "client_shares", "asset_dates", "trips", "effective_rates", "app_usage", "payment_morale"),
		{Key: "limit", Input: InputNumber, Default: 8}},
	"chart":       {sel("chart", "revenue", "revenue", "hours", "seasonal"), {Key: "months", Input: InputNumber, Default: 12}},
	"progress":    {{Key: "goal", Input: InputCheck}},
	"deadlines":   {{Key: "days", Input: InputNumber, Default: 45}},
	"trend":       {sel("metric", "revenue_ytd", "revenue_ytd", "open_amount", "month_min"), {Key: "days", Input: InputNumber, Default: 90}},
	"updates":     {{Key: "limit", Input: InputNumber, Default: 20}},
	"kimai_timer": {},
	"heatmap":     {},
	"cashflow":    {{Key: "days", Input: InputNumber, Default: defaultCashDays}},
	"backups":     {{Key: "max_hours", Input: InputNumber, Default: defaultBackupHours}},
	"hints":       {{Key: "sources", Input: InputList}, {Key: "min_severity", Input: InputNumber, Default: 10}, {Key: "limit", Input: InputNumber, Default: 8}},
	"calendar": {{Key: "ical_url", Input: InputSecret}, {Key: "days", Input: InputNumber, Default: defaultCalDays},
		{Key: "limit", Input: InputNumber, Default: defaultListLimit}},
	"custom_api": {{Key: "url", Input: InputText, Required: true}, {Key: "fields", Input: InputArea}, {Key: "headers", Input: InputHeaders}},
	"list":       {{Key: "entries", Input: InputArea}},
	"holidays": {{Key: "country", Input: InputText, Default: defaultCountry}, {Key: "state", Input: InputText},
		{Key: "limit", Input: InputNumber, Default: 5}},
	"xkcd":   {},
	"apod":   {{Key: "api_key", Input: InputSecret}},
	"joke":   {sel("category", "Any", "Any", "Programming", "Misc", "Pun", "Spooky", "Christmas"), sel("lang", "de", "de", "en")},
	"crypto": {{Key: "coins", Input: InputList, Default: []any{"bitcoin", "ethereum"}}, {Key: "currency", Input: InputText, Default: defaultCurrency}},
	"stocks": {{Key: "tickers", Input: InputList, Default: []any{"aapl.us", "sap.de"}}},
	"flights": {{Key: "airport", Input: InputText, Required: true}, sel("direction", "Departure", "Departure", "Arrival"),
		{Key: "limit", Input: InputNumber, Default: defaultListLimit}, {Key: "api_key", Input: InputSecret}},
	"transit": {{Key: "stop", Input: InputText, Required: true}, {Key: "limit", Input: InputNumber, Default: defaultListLimit}},
}

// dataModeField lets connection-bound widgets choose live or background data.
var dataModeField = sel(DataModeKey, string(DataAuto), string(DataAuto), string(DataLive), string(DataStored))

// liveCapable are types whose data comes from a connection.
var liveCapable = map[string]bool{"link": true, "kpi": true, "table": true, "chart": true, "progress": true,
	"sysinfo": true, "monitors": true, "hass": true, "glances_chart": true, "kimai_timer": true}

// FieldsOf returns the config fields of a widget type.
func FieldsOf(key string) []Field {
	if liveCapable[key] {
		return append(append([]Field(nil), fieldsByType[key]...), dataModeField)
	}
	return fieldsByType[key]
}

// FormValue is one field with its current value, ready for a form.
type FormValue struct {
	Field
	Name  string // form name, "cfg.url"
	Label string // catalog suffix of the label, "url" or "info"
	Text  string // value as text
	On    bool   // checkbox state
}

func lookup(config map[string]any, dotted string) (any, bool) {
	parts := strings.Split(dotted, ".")
	var cur any = config
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[p]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func textOf(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, textOf(item))
		}
		return strings.Join(parts, listSep+" ")
	}
	return ""
}

// FormValues pairs each field of a type with its value in config.
func FormValues(key string, config map[string]any) []FormValue {
	fields := FieldsOf(key)
	out := make([]FormValue, 0, len(fields))
	for _, f := range fields {
		v, ok := lookup(config, f.Key)
		if !ok {
			v = f.Default
		}
		label := f.Key
		if i := strings.LastIndex(label, "."); i >= 0 {
			label = label[:i]
		}
		on, _ := v.(bool)
		text := textOf(v)
		switch f.Input {
		case InputLinks:
			text = linksText(v)
		case InputHeaders:
			text = headersText(v)
		case InputSecret:
			text = ""
		}
		out = append(out, FormValue{Field: f, Name: FormPrefix + f.Key, Label: label, Text: text, On: on})
	}
	return out
}

func set(config map[string]any, dotted string, value any) {
	parts := strings.Split(dotted, ".")
	cur := config
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = value
}

func splitList(raw string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseForm turns submitted form values into a config map; get returns
// the value of one form field ("" if missing).
func ParseForm(key string, get func(name string) string) map[string]any {
	config := map[string]any{}
	for _, f := range FieldsOf(key) {
		raw := strings.TrimSpace(get(FormPrefix + f.Key))
		switch f.Input {
		case InputCheck:
			set(config, f.Key, raw != "")
		case InputNumber:
			if n, err := strconv.ParseFloat(strings.ReplaceAll(raw, ",", "."), 64); err == nil {
				set(config, f.Key, n)
			}
		case InputList:
			list := []any{}
			for _, s := range splitList(raw) {
				list = append(list, s)
			}
			set(config, f.Key, list)
		case InputNumbers:
			list := []any{}
			for _, s := range splitList(raw) {
				if n, err := strconv.Atoi(s); err == nil {
					list = append(list, float64(n))
				}
			}
			set(config, f.Key, list)
		case InputLinks:
			set(config, f.Key, parseLinks(raw))
		case InputSecret:
			set(config, f.Key, raw)
		case InputHeaders:
			if raw == secretClear {
				set(config, f.Key, raw)
				continue
			}
			set(config, f.Key, parseHeaders(raw))
		default:
			if raw != "" || f.Required {
				set(config, f.Key, raw)
			}
		}
	}
	return config
}

// linksText: [{title: Admin, url: https://x/admin}] → "Admin | https://x/admin".
func linksText(v any) string {
	var lines []string
	for _, l := range subLinks(v) {
		line := l.Title + " " + linkSep + " " + l.URL
		if l.Icon != "" {
			line += " " + linkSep + " " + l.Icon
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func parseLinks(raw string) []any {
	out := []any{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Split(line, linkSep)
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}

		// A bare URL is allowed: "https://x/admin".
		if len(parts) == 1 {
			parts = []string{"", parts[0]}
		}
		if parts[1] == "" {
			continue
		}
		item := map[string]any{"title": parts[0], "url": parts[1]}
		if len(parts) > 2 && parts[2] != "" {
			item["icon"] = parts[2]
		}
		out = append(out, item)
	}
	return out
}

// headersText: {X-Api: a} → "X-Api: a", sorted for a stable form.
func headersText(v any) string {
	m := stringMap(v)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+headerSep+" "+m[k])
	}
	return strings.Join(lines, "\n")
}

func parseHeaders(raw string) map[string]any {
	out := map[string]any{}
	for _, line := range strings.Split(raw, "\n") {
		name, value, ok := strings.Cut(line, headerSep)
		if name = strings.TrimSpace(name); !ok || name == "" {
			continue
		}
		out[name] = strings.TrimSpace(value)
	}
	return out
}
