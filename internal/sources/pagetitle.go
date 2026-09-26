package sources

// Page title for "add link by URL": og:site_name, else the last part of
// the <title>, else the host.
//
//	<title>Login | Jellyfin</title> → "Jellyfin"

import (
	"context"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"andon/internal/drivers/httpclient"
)

const titleScan = 256 << 10

var (
	titleRe   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	siteRe    = regexp.MustCompile(`(?is)<meta[^>]+property=["']og:site_name["'][^>]+content=["']([^"']+)["']`)
	titleCuts = []string{" | ", " - ", " – ", " · "}
)

// PageTitle returns a short name for a URL; the host when the page gives none.
func PageTitle(ctx context.Context, target string) string {
	fallback := target
	if u, err := url.Parse(target); err == nil && u.Hostname() != "" {
		fallback = u.Hostname()
	}
	resp, err := httpclient.Request(ctx, http.MethodGet, target, httpclient.Options{})
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, titleScan))
	page := string(raw)

	if m := siteRe.FindStringSubmatch(page); m != nil {
		return strings.TrimSpace(html.UnescapeString(m[1]))
	}
	m := titleRe.FindStringSubmatch(page)
	if m == nil {
		return fallback
	}
	title := strings.Join(strings.Fields(html.UnescapeString(m[1])), " ")

	// "Login | Jellyfin": the site name usually comes last.
	for _, cut := range titleCuts {
		if i := strings.LastIndex(title, cut); i >= 0 {
			title = strings.TrimSpace(title[i+len(cut):])
		}
	}
	if title == "" {
		return fallback
	}
	return title
}
