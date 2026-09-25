package widgets

import (
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
	},
	"rss":       {{Key: "url", Input: InputText, Required: true}, {Key: "limit", Input: InputNumber, Default: 8}, {Key: "summary", Input: InputCheck}},
	"clock":     {{Key: "timezones", Input: InputList, Default: []any{defaultTimezone}}, {Key: "seconds", Input: InputCheck}, {Key: "date", Input: InputCheck, Default: true}},
	"weather":   {{Key: "label", Input: InputText}, {Key: "lat", Input: InputNumber, Required: true}, {Key: "lon", Input: InputNumber, Required: true}},
	"iframe":    {{Key: "url", Input: InputText, Required: true}, {Key: "height", Input: InputNumber, Default: 320}},
	"note":      {{Key: "text", Input: InputArea}},
	"image":     {{Key: "url", Input: InputText, Required: true}, {Key: "height", Input: InputNumber, Default: 240}, {Key: "link", Input: InputText}},
	"rates":     {{Key: "base", Input: InputText, Default: "EUR"}, {Key: "symbols", Input: InputList, Default: []any{"USD", "CHF", "GBP"}}},
	"monitors":  {},
	"hass":      {{Key: "entities", Input: InputList, Required: true}},
	"sysinfo":   {},
	"public_ip": {},
	"kpi": {sel("metric", "hours_today", "hours_today", "hours_week", "hours_month", "utilization", "unbilled",
		"revenue_ytd", "revenue_month", "open_amount", "overdue_amount", "vat_liability", "tax_reserve",
		"asset_value", "assets_ready", "revenue_forecast", "cash_30", "liquidity_30", "effective_rate")},
	"table": {sel("table", "open_invoices", "open_invoices", "unbilled", "budgets", "client_shares", "asset_dates", "trips", "effective_rates"),
		{Key: "limit", Input: InputNumber, Default: 8}},
	"chart":     {sel("chart", "revenue", "revenue", "hours", "seasonal"), {Key: "months", Input: InputNumber, Default: 12}},
	"progress":  {{Key: "goal", Input: InputCheck}},
	"deadlines": {{Key: "days", Input: InputNumber, Default: 45}},
	"trend":     {sel("metric", "revenue_ytd", "revenue_ytd", "open_amount", "month_min"), {Key: "days", Input: InputNumber, Default: 90}},
	"hints":     {{Key: "sources", Input: InputList}, {Key: "min_severity", Input: InputNumber, Default: 10}, {Key: "limit", Input: InputNumber, Default: 8}},
}

// FieldsOf returns the config fields of a widget type.
func FieldsOf(key string) []Field { return fieldsByType[key] }

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
		out = append(out, FormValue{Field: f, Name: FormPrefix + f.Key, Label: label, Text: textOf(v), On: on})
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
		default:
			if raw != "" || f.Required {
				set(config, f.Key, raw)
			}
		}
	}
	return config
}
