package sources

// Generic JSON connection: any URL, optional token. Its dataset is the
// decoded body, so custom rules can test any field ("stats.users > 100").
// Options turn it into an integration of its own (YAML on the connection):
//
//	header: X-Api-Key                  token header; default "Authorization: Bearer <token>"
//	fields:                            key figures for the "jsonapi" widget
//	  - {label: Benutzer, path: stats.users}
//	  - {label: Warteschlange, path: stats.queue, warn: 10, critical: 50}
//	list: {path: jobs, columns: [name, state], limit: 10}

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"andon/internal/drivers/httpclient"
	"andon/internal/enums"
)

const (
	bearerPrefix     = "Bearer "
	jsonPathSep      = "."
	defaultListLimit = 10
	maxListLimit     = 100
)

// JSONField is one configured key figure with its thresholds (0 = none).
type JSONField struct {
	Label          string
	Path           string
	Value          float64
	Text           string // non-numeric values
	Found, Numeric bool
	Warn, Critical float64
}

// JSONAPIDataset is the decoded body plus the configured view of it.
// It marshals as the body, so rule paths address the service's fields.
type JSONAPIDataset struct {
	Body    any
	Fields  []JSONField
	Columns []string
	Rows    [][]string
}

func (d *JSONAPIDataset) MarshalJSON() ([]byte, error) { return json.Marshal(d.Body) }

type JSONAPIData struct{}

func (JSONAPIData) Key() string                { return "jsonapi.data" }
func (JSONAPIData) TTL() time.Duration         { return opsTTL }
func (JSONAPIData) Service() enums.ServiceType { return enums.ServiceJSONAPI }

func (JSONAPIData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return shapeJSON(map[string]any{"stats": map[string]any{"users": 42.0, "queue": 3.0}}, sctx.Options), nil
	}
	headers := map[string]string{"Accept": "application/json"}
	if sctx.Secret != "" {
		if name := asStr(sctx.Options["header"]); name != "" {
			headers[name] = sctx.Secret
		} else {
			headers["Authorization"] = bearerPrefix + sctx.Secret
		}
	}
	body, _, err := httpclient.GetJSON(ctx, sctx.URL, httpclient.Options{Headers: headers, SkipVerify: !sctx.VerifyTLS})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	return shapeJSON(body, sctx.Options), nil
}

// shapeJSON applies the connection's field and list options to a body.
func shapeJSON(body any, options map[string]any) *JSONAPIDataset {
	out := &JSONAPIDataset{Body: body}
	for _, raw := range asList(options["fields"]) {
		spec := asMap(raw)
		f := JSONField{Label: asStr(spec["label"]), Path: asStr(spec["path"]), Warn: asFloat(spec["warn"]), Critical: asFloat(spec["critical"])}
		if f.Path == "" {
			continue
		}
		if f.Label == "" {
			f.Label = f.Path
		}
		v, found := jsonLookup(body, f.Path)
		f.Found = found
		if n, ok := v.(float64); ok {
			f.Value, f.Numeric = n, true
		} else if found {
			f.Text = jsonText(v)
		}
		out.Fields = append(out.Fields, f)
	}

	list := asMap(options["list"])
	items, _ := jsonLookup(body, asStr(list["path"]))
	if asStr(list["path"]) == "" {
		return out
	}
	for _, c := range asList(list["columns"]) {
		out.Columns = append(out.Columns, asStr(c))
	}
	limit := int(asInt64(list["limit"]))
	if limit <= 0 {
		limit = defaultListLimit
	}
	for i, item := range asList(items) {
		if i >= min(limit, maxListLimit) {
			break
		}
		row := make([]string, len(out.Columns))
		for j, col := range out.Columns {
			v, _ := jsonLookup(item, col)
			row[j] = jsonText(v)
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

// jsonLookup walks "a.b.0.c" through maps and lists.
func jsonLookup(body any, path string) (any, bool) {
	cur := body
	for _, part := range strings.Split(path, jsonPathSep) {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// jsonText renders a JSON value for a table cell ("" for nothing).
func jsonText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool, map[string]any, []any:
		raw, _ := json.Marshal(x)
		return string(raw)
	}
	return fmt.Sprint(v)
}

func init() {
	Register(JSONAPIData{})
	Register(testOf{JSONAPIData{}, func(d any) map[string]any {
		return map[string]any{"fields": len(asMap(d.(*JSONAPIDataset).Body))}
	}})
}
