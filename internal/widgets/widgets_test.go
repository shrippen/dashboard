package widgets_test

import (
	"testing"

	"andon/internal/widgets"
)

func TestDecodeLinkDefaults(t *testing.T) {
	cfg, ok := widgets.Decode("link", map[string]any{"url": "https://example.org"})
	if !ok {
		t.Fatal("expected link type registered")
	}
	link := cfg.(widgets.LinkConfig)
	if link.URL != "https://example.org" || link.Status != widgets.StatusHTTP {
		t.Fatalf("unexpected defaults: %+v", link)
	}
}

func TestDecodeUnknownType(t *testing.T) {
	if _, ok := widgets.Decode("nope", nil); ok {
		t.Fatal("expected unknown widget type to report ok=false")
	}
}

func TestLinkQueriesStatusCheck(t *testing.T) {
	kind, ok := widgets.Get("link")
	if !ok {
		t.Fatal("expected link type registered")
	}
	cfg, _ := widgets.Decode("link", map[string]any{"url": "https://example.org", "status": "http"})
	qs := kind.Queries(cfg)
	if len(qs) != 1 || qs[0].Source != "http_status" {
		t.Fatalf("expected 1 http_status query, got %+v", qs)
	}
}

func TestClockDefaultsToOneTimezone(t *testing.T) {
	cfg, _ := widgets.Decode("clock", map[string]any{})
	clock := cfg.(widgets.ClockConfig)
	if len(clock.Timezones) != 1 || clock.Timezones[0] != "Europe/Berlin" {
		t.Fatalf("expected default Berlin timezone, got %+v", clock)
	}
	if !clock.Date {
		t.Fatal("expected date shown by default")
	}
}

func TestRssLimitClamped(t *testing.T) {
	cfg, _ := widgets.Decode("rss", map[string]any{"url": "https://example.org/feed", "limit": float64(999)})
	rss := cfg.(widgets.RssConfig)
	if rss.Limit != 50 {
		t.Fatalf("expected limit clamped to 50, got %d", rss.Limit)
	}
}

func TestAllTypesSortedByCategoryThenKey(t *testing.T) {
	all := widgets.AllTypes()
	if len(all) < 8 {
		t.Fatalf("expected at least 8 start widget types, got %d", len(all))
	}
	for i := 1; i < len(all); i++ {
		prev, cur := all[i-1], all[i]
		if prev.Category > cur.Category || (prev.Category == cur.Category && prev.Key > cur.Key) {
			t.Fatalf("types not sorted: %s before %s", prev.Key, cur.Key)
		}
	}
}
