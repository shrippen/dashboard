package sources_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"dashboard/internal/sources"
)

func TestHTTPStatusUpWithinDefaultRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	source, err := sources.Get("http_status")
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	out, err := source.Fetch(context.Background(), sources.Ctx{Params: map[string]any{"url": srv.URL}})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	status := out.(*sources.HTTPStatusResult)
	if !status.Up || status.Code != http.StatusOK {
		t.Fatalf("expected up=true code=200, got %+v", status)
	}
}

func TestHTTPStatusDownOutsideAcceptList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	source, _ := sources.Get("http_status")
	out, err := source.Fetch(context.Background(), sources.Ctx{Params: map[string]any{
		"url": srv.URL, "accept": []int{201, 204},
	}})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	status := out.(*sources.HTTPStatusResult)
	if status.Up {
		t.Fatalf("expected down (200 not in accept list), got %+v", status)
	}
}

func TestHTTPStatusUnreachableIsDataNotError(t *testing.T) {
	source, _ := sources.Get("http_status")
	out, err := source.Fetch(context.Background(), sources.Ctx{Params: map[string]any{"url": "http://127.0.0.1:1"}})
	if err != nil {
		t.Fatalf("expected a failed check to be data, not an error: %v", err)
	}
	status := out.(*sources.HTTPStatusResult)
	if status.Up || status.Error == "" {
		t.Fatalf("expected down with an error message, got %+v", status)
	}
}

func TestFeedFetchParsesRSS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0"><channel><title>Example Feed</title>
<item><title>Hello &amp; World</title><link>https://example.org/1</link>
<pubDate>Mon, 02 Jan 2006 15:04:05 +0000</pubDate><description>&lt;p&gt;Body text&lt;/p&gt;</description></item>
</channel></rss>`))
	}))
	defer srv.Close()

	source, _ := sources.Get("rss")
	out, err := source.Fetch(context.Background(), sources.Ctx{Params: map[string]any{"url": srv.URL, "limit": float64(8)}})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	feed := out.(*sources.FeedResult)
	if feed.Title != "Example Feed" || len(feed.Items) != 1 {
		t.Fatalf("unexpected feed: %+v", feed)
	}
	item := feed.Items[0]
	if item.Title != "Hello & World" || item.Link != "https://example.org/1" || item.Summary != "Body text" {
		t.Fatalf("unexpected item: %+v", item)
	}
	if item.Published == "" {
		t.Fatal("expected a parsed publish date")
	}
}

func TestFeedFetchParsesAtom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom"><title>Atom Feed</title>
<entry><title>Entry One</title><link href="https://example.org/a"/>
<updated>2026-01-02T15:04:05Z</updated><summary>Summary text</summary></entry>
</feed>`))
	}))
	defer srv.Close()

	source, _ := sources.Get("rss")
	out, err := source.Fetch(context.Background(), sources.Ctx{Params: map[string]any{"url": srv.URL, "limit": float64(8)}})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	feed := out.(*sources.FeedResult)
	if feed.Title != "Atom Feed" || len(feed.Items) != 1 || feed.Items[0].Link != "https://example.org/a" {
		t.Fatalf("unexpected feed: %+v", feed)
	}
}

func TestFeedFetchRejectsInvalidXML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not xml at all"))
	}))
	defer srv.Close()

	source, _ := sources.Get("rss")
	if _, err := source.Fetch(context.Background(), sources.Ctx{Params: map[string]any{"url": srv.URL}}); err == nil {
		t.Fatal("expected an error for invalid feed content")
	}
}

func TestFeedFetchRespectsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<rss version="2.0"><channel><title>T</title>
<item><title>1</title></item><item><title>2</title></item><item><title>3</title></item>
</channel></rss>`))
	}))
	defer srv.Close()

	source, _ := sources.Get("rss")
	out, err := source.Fetch(context.Background(), sources.Ctx{Params: map[string]any{"url": srv.URL, "limit": float64(2)}})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(out.(*sources.FeedResult).Items) != 2 {
		t.Fatalf("expected limit=2 to apply, got %d items", len(out.(*sources.FeedResult).Items))
	}
}

func TestPublicIPFetch(t *testing.T) {
	source, err := sources.Get("public_ip")
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if source.Service() != "" {
		t.Fatalf("expected public_ip to need no connection, got service=%q", source.Service())
	}
}

func TestGlancesFetchUsesBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/4/quicklook":
			w.Write([]byte(`{"cpu": 12.5, "mem": 40.0, "swap": 1.0}`))
		case "/api/4/fs":
			w.Write([]byte(`[{"mnt_point": "/", "percent": 55.0}]`))
		case "/api/4/load":
			w.Write([]byte(`{"min5": 0.8}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	source, err := sources.Get("glances")
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	out, err := source.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "tok", VerifyTLS: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	stats := out.(*sources.GlancesResult)
	if stats.CPU != 12.5 || stats.Load != 0.8 || len(stats.Disks) != 1 || stats.Disks[0].Percent != 55.0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("expected bearer token header, got %q", gotAuth)
	}
}

func TestHTTPStatusSendsHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api") != "t" {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	source, _ := sources.Get("http_status")
	out, _ := source.Fetch(context.Background(), sources.Ctx{Params: map[string]any{
		"url": srv.URL, "headers": map[string]string{"X-Api": "t"},
	}})
	if status := out.(*sources.HTTPStatusResult); !status.Up {
		t.Fatalf("header not sent: %+v", status)
	}
}
