package widgets

// "jsonapi": the key figures and list an own integration's YAML options
// define (see sources/jsonapi.go).

import (
	"andon/internal/enums"
	"andon/internal/sources"
)

// jsonAPIRefresh: seconds between reloads, matching the source TTL.
const jsonAPIRefresh = 300

// jsonFieldLevel marks a field for the template: "", "warn", "fail".
func jsonFieldLevel(f sources.JSONField) string {
	switch {
	case !f.Numeric:
		return ""
	case f.Critical > 0 && f.Value >= f.Critical:
		return "fail"
	case f.Warn > 0 && f.Value >= f.Warn:
		return "warn"
	}
	return ""
}

// JSONFieldView is one key figure as shown.
type JSONFieldView struct {
	sources.JSONField
	Level string
}

func jsonAPIView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.JSONAPIDataset)
	if !ok {
		return map[string]any{}
	}
	fields := make([]JSONFieldView, 0, len(data.Fields))
	for _, f := range data.Fields {
		fields = append(fields, JSONFieldView{JSONField: f, Level: jsonFieldLevel(f)})
	}
	return map[string]any{"Fields": fields, "Columns": data.Columns, "Rows": data.Rows}
}

func init() {
	Register(WidgetType{Key: "jsonapi", Decode: decodeEmpty, Template: "widgets/jsonapi", Category: CategoryInsight,
		Service: enums.ServiceJSONAPI, RefreshS: jsonAPIRefresh, Queries: dataQuery, View: jsonAPIView})
}
