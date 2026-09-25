package spaces

import "strings"

// Page texts of a space, shown on its boards (Dashy's pageInfo):
//
//	settings {title, description, nav: [{title, url}], footer}
const (
	PageTitle  = "title"
	PageDesc   = "description"
	PageNav    = "nav"
	PageFooter = "footer"
	navSep     = "|"
)

// NavLink is one link in a board's page header.
type NavLink struct {
	Title, URL string
}

// PageInfo is the page header and footer of a space's boards.
type PageInfo struct {
	Title, Description, Footer string
	Nav                        []NavLink
}

// PageOf reads the page texts from space settings.
func PageOf(settings map[string]any) PageInfo {
	text := func(key string) string {
		s, _ := settings[key].(string)
		return s
	}
	page := PageInfo{Title: text(PageTitle), Description: text(PageDesc), Footer: text(PageFooter)}
	list, _ := settings[PageNav].([]any)
	for _, raw := range list {
		m, _ := raw.(map[string]any)
		title, _ := m["title"].(string)
		url, _ := m["url"].(string)
		if !safeURL(url) {
			continue
		}
		if title == "" {
			title = url
		}
		page.Nav = append(page.Nav, NavLink{Title: title, URL: url})
	}
	return page
}

// safeURL keeps nav links to web and site-relative addresses.
func safeURL(url string) bool {
	return strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") ||
		(strings.HasPrefix(url, "/") && !strings.HasPrefix(url, "//"))
}

// NavText: [{Docs /docs}] → "Docs | /docs", one link per line.
func NavText(page PageInfo) string {
	lines := make([]string, 0, len(page.Nav))
	for _, n := range page.Nav {
		lines = append(lines, n.Title+" "+navSep+" "+n.URL)
	}
	return strings.Join(lines, "\n")
}

// ParsePage turns the settings form's page fields into settings changes.
func ParsePage(get func(string) string) map[string]any {
	nav := []any{}
	for _, line := range strings.Split(get(PageNav), "\n") {
		title, url, ok := strings.Cut(line, navSep)
		if !ok {
			title, url = "", title
		}
		url = strings.TrimSpace(url)
		if !safeURL(url) {
			continue
		}
		nav = append(nav, map[string]any{"title": strings.TrimSpace(title), "url": url})
	}
	return map[string]any{
		PageTitle:  strings.TrimSpace(get(PageTitle)),
		PageDesc:   strings.TrimSpace(get(PageDesc)),
		PageFooter: strings.TrimSpace(get(PageFooter)),
		PageNav:    nav,
	}
}
