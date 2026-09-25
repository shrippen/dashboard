// Package widgets is the widget type contract and registry.
//
// A widget type declares what it needs, never how to get it:
//
//	Register(WidgetType{Key: "rss", Template: "widgets/rss.html",
//	    Queries: func(cfg any) []Query { ... }})
//
// The widgets service runs the queries (with access checks and caching)
// and hands the results to the template. Config validation happens per
// type via Decode, which mirrors Python's pydantic model_validate: known
// fields only, sane zero-value defaults.
package widgets

import (
	"sort"

	"dashboard/internal/enums"
)

// ConnUse says whether (and how) a query needs the widget's connection.
type ConnUse string

const (
	// ConnNone is "" (Go's zero value for a string type), not "none": a
	// Query built without setting Conn must default to "no connection
	// needed", the same as Python's Query(conn=ConnUse.NONE) default.
	ConnNone   ConnUse = ""
	ConnWidget ConnUse = "widget"
	ConnInfo   ConnUse = "info"
	// ConnPeer is another connection of the widget's space, found by
	// Query.Service (e.g. the Kimai hours next to Invoice Ninja revenue).
	ConnPeer ConnUse = "peer"
)

// Extra is additional data the widgets service supplies besides the queries.
type Extra string

const (
	ExtraNone   Extra = "none"
	ExtraHints  Extra = "hints"
	ExtraPoints Extra = "points"
)

// Category groups widget types for the library UI.
type Category string

const (
	CategoryStart   Category = "start"
	CategoryInsight Category = "insight"
)

// ViewCtx is what a view function may use: no I/O, only values.
type ViewCtx struct {
	Today    string // ISO date
	Settings map[string]any
	Options  map[string]any
	Service  string // "" if the widget has no connection
}

// Query is one data request a widget type needs; the widgets service runs
// it and passes the result back keyed by Name.
type Query struct {
	Name    string
	Source  string
	Params  map[string]any
	Conn    ConnUse
	Service enums.ServiceType // ConnPeer only
}

// DecodeFunc parses a widget's raw JSON config into its typed config value.
type DecodeFunc func(raw map[string]any) any

// QueriesFunc returns the queries a widget instance needs, given its config.
type QueriesFunc func(cfg any) []Query

// ViewFunc shapes query results into template data.
type ViewFunc func(cfg any, results map[string]any, ctx ViewCtx) map[string]any

// WidgetType is one kind of widget (link, rss, clock, kpi, ...).
type WidgetType struct {
	Key      string
	Decode   DecodeFunc
	Template string
	Category Category
	Service  enums.ServiceType // "" if not tied to one service
	RefreshS int               // 0 = no periodic refresh
	// Inline widgets render with the page (search needs link tiles in the HTML).
	Inline  bool
	Queries QueriesFunc
	View    ViewFunc
	Extra   Extra
}

var registry = map[string]WidgetType{}

// Register adds a widget type to the process-wide registry.
func Register(kind WidgetType) WidgetType {
	if kind.Queries == nil {
		kind.Queries = func(any) []Query { return nil }
	}
	registry[kind.Key] = kind
	return kind
}

// Get looks up a widget type by key, or ok=false if unknown.
func Get(key string) (WidgetType, bool) {
	k, ok := registry[key]
	return k, ok
}

// AllTypes returns every registered widget type, grouped by category then key.
func AllTypes() []WidgetType {
	out := make([]WidgetType, 0, len(registry))
	for _, k := range registry {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Decode parses a widget's stored config through its type's decoder.
// Returns (nil, false) for an unknown widget type.
func Decode(key string, config map[string]any) (any, bool) {
	kind, ok := registry[key]
	if !ok {
		return nil, false
	}
	if config == nil {
		config = map[string]any{}
	}
	return kind.Decode(config), true
}
