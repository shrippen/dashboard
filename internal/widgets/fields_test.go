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

func TestLinksAndHeadersForm(t *testing.T) {
	config := map[string]any{"url": "https://a", "items": []any{
		map[string]any{"title": "Admin", "url": "https://a/admin", "icon": "hl-a"},
		map[string]any{"title": "Docs", "url": "https://a/docs"},
	}}
	var text string
	for _, v := range FormValues("link", config) {
		if v.Input == InputLinks {
			text = v.Text
		}
	}
	if text != "Admin | https://a/admin | hl-a\nDocs | https://a/docs" {
		t.Fatalf("text: %q", text)
	}

	got := ParseForm("link", func(name string) string {
		switch name {
		case FormPrefix + "items":
			return text + "\nhttps://a/bare"
		case FormPrefix + "headers":
			return "X-Api: t:1\nbroken"
		}
		return ""
	})
	items := got["items"].([]any)
	if len(items) != 3 || items[2].(map[string]any)["url"] != "https://a/bare" {
		t.Fatalf("items: %#v", items)
	}
	if h := got["headers"].(map[string]any); len(h) != 1 || h["X-Api"] != "t:1" {
		t.Fatalf("headers: %#v", h)
	}
}

// TestPlaceField: weather asks for a place; the pick stores its name and
// coordinates as numbers, and the form shows both again.
func TestPlaceField(t *testing.T) {
	form := map[string]string{"cfg.place": "Weimar, Thüringen, Deutschland", "cfg.lat": "50.9803", "cfg.lon": "11.32903"}
	config := ParseForm("weather", func(name string) string { return form[name] })
	if config["place"] != "Weimar, Thüringen, Deutschland" || config["lat"] != 50.9803 || config["lon"] != 11.32903 {
		t.Fatalf("config: %v", config)
	}
	for _, v := range FormValues("weather", config) {
		if v.Input == InputPlace {
			if v.Text != "Weimar, Thüringen, Deutschland" || v.Lat != "50.9803" || v.Lon != "11.32903" {
				t.Fatalf("form value: %+v", v)
			}
			return
		}
	}
	t.Fatal("weather has no place field")
}
