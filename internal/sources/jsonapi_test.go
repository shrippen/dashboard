package sources_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dashboard/internal/sources"
)

// TestJSONAPIShape: fields and list come from the options; the dataset
// still marshals as the body for rule paths.
func TestJSONAPIShape(t *testing.T) {
	var options map[string]any
	_ = json.Unmarshal([]byte(`{"fields": [{"label": "Nutzer", "path": "stats.users"}, {"path": "name"}, {"path": "nope"}],
		"list": {"path": "jobs", "columns": ["name", "state"], "limit": 1}}`), &options)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"stats": {"users": 42}, "name": "app", "jobs": [{"name": "a", "state": "ok"}, {"name": "b"}]}`))
	}))
	defer srv.Close()
	out, err := sources.JSONAPIData{}.Fetch(t.Context(), sources.Ctx{URL: srv.URL, Secret: "tok", Options: options})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.JSONAPIDataset)
	if len(d.Fields) != 3 || d.Fields[0].Value != 42 || !d.Fields[0].Numeric || d.Fields[1].Text != "app" || d.Fields[2].Found {
		t.Fatalf("fields: %+v", d.Fields)
	}
	if len(d.Rows) != 1 || d.Rows[0][0] != "a" || d.Rows[0][1] != "ok" {
		t.Fatalf("rows: %+v", d.Rows)
	}
	raw, _ := json.Marshal(d)
	if string(raw) != `{"jobs":[{"name":"a","state":"ok"},{"name":"b"}],"name":"app","stats":{"users":42}}` {
		t.Fatalf("marshal: %s", raw)
	}
}
