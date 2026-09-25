package widgets

import (
	"reflect"
	"testing"
)

func TestEveryTypeHasFields(t *testing.T) {
	for _, kind := range AllTypes() {
		if _, ok := fieldsByType[kind.Key]; !ok {
			t.Errorf("widget type %q has no form fields", kind.Key)
		}
	}
}

func TestFormRoundTrip(t *testing.T) {
	config := map[string]any{
		"url": "https://kimai.lan", "icon": "hl-kimai", "target": "sametab", "status": "off",
		"accept": []any{200.0, 401.0}, "insecure": true, "hotkey": "k", "info": map[string]any{"connection": "kimai"},
	}
	form := map[string]string{}
	for _, v := range FormValues("link", config) {
		if v.Input == InputCheck {
			if v.On {
				form[v.Name] = "on"
			}
			continue
		}
		form[v.Name] = v.Text
	}
	got := ParseForm("link", func(name string) string { return form[name] })
	for key, want := range config {
		if !reflect.DeepEqual(got[key], want) {
			t.Errorf("%s: got %#v, want %#v", key, got[key], want)
		}
	}
}
