package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/services/hints"
)

// The hint detail fragment renders history, people and states.
func TestHintDetailRenders(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx := Ctx{CSRF: "c", Locale: enums.LocaleDE}
	err := Deps{}.Page(rec, ctx, "hint_detail", http.StatusOK, map[string]any{
		"ThemeURL": "", "ID": int64(7), "States": workStates,
		"People":  []hints.Person{{ID: 2, Name: "Kim"}},
		"History": []hints.EventView{{Kind: enums.EventAcked, Actor: "Alex", Note: "erledigt", At: time.Now()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{`action="/hints/7/assign"`, ">Kim<", "in Arbeit", "quittiert · Alex", "erledigt"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q:\n%s", want, body)
		}
	}
}
