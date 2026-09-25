package porting

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"dashboard/internal/enums"
	"dashboard/internal/services/access"
	"dashboard/internal/services/util"
)

// Dashy conf.yml → our import document. Items become link widgets, known
// Dashy widgets their counterparts; everything else lands in the report.

const (
	dashyGlances       = "gl-"
	defaultRSSLimit    = 8
	defaultIframeSize  = 320
	defaultImageHeight = 240
	startSlug          = "start"
	startTitle         = "Start"
)

var dashyIconPrefixes = []string{"si-", "hl-", "favicon", "http://", "https://"}

var dashyWidgets = map[string]string{
	"rss-feed": "rss", "clock": "clock", "weather": "weather", "weather-forecast": "weather",
	"iframe": "iframe", "public-ip": "public_ip", "image": "image", "exchange-rates": "rates",
	"hackernews-trending": "rss",
}

// hackerNewsFeed replaces Dashy's Hacker News widget with its RSS feed.
const hackerNewsFeed = "https://hnrss.org/frontpage"

// dashyConnectionWidgets need a connection before a widget can show them.
var dashyConnectionWidgets = map[string]string{
	"uptime-kuma":   "add an Uptime Kuma connection, then a monitors widget",
	"proxmox-lists": "add a Proxmox connection; its hints and link-tile info replace the list",
}

var dashyVisibility = []string{"hideForUsers", "showForUsers", "hideForGuests", "hideForKeycloakUsers"}

var ignoredAppConfig = []string{"theme", "customCss", "layout", "iconSize", "cssThemes", "colors"}

func mapOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func truthy(v any) bool {
	b, _ := v.(bool)
	return b
}

// DashyToDoc translates a Dashy conf.yml into an import document.
func DashyToDoc(text string) (map[string]any, *Report, error) {
	raw, err := Load(text)
	if err != nil {
		return nil, nil, err
	}
	report := &Report{}
	appConfig := mapOf(raw["appConfig"])
	defaultStatus := truthy(appConfig["statusCheck"])

	for _, key := range ignoredAppConfig {
		if _, ok := appConfig[key]; ok {
			report.Notes = append(report.Notes, "appConfig."+key+": ignored (theme stays shrippen)")
		}
	}
	if appConfig["auth"] != nil {
		report.Notes = append(report.Notes, "appConfig.auth: recreate users and permissions in the dashboard")
	}

	var widgetDocs []any
	var sections []any
	taken := map[string]bool{}
	add := func(item map[string]any, fallback string) string {
		id := util.Unique(util.Slug(str(item, "title"), fallback), taken)
		taken[id] = true
		item["id"] = id
		widgetDocs = append(widgetDocs, item)
		return id
	}

	for _, sec := range list(raw, "sections") {
		refs := []any{}
		for _, entry := range list(sec, "items") {
			item := dashyItem(entry, defaultStatus, report)
			if item == nil {
				continue
			}
			refs = append(refs, add(item, "link"))
			for _, field := range dashyVisibility {
				if entry[field] != nil {
					report.Notes = append(report.Notes, str(item, "title")+": "+field+" → set permissions manually")
				}
			}
		}
		for _, entry := range list(sec, "widgets") {
			item := dashyWidget(entry, report)
			if item == nil {
				continue
			}
			refs = append(refs, add(item, str(item, "type")))
		}
		sections = append(sections, dashySection(sec, refs))
	}

	page := mapOf(raw["pageInfo"])
	for _, sub := range list(raw, "pages") {
		report.Notes = append(report.Notes, "page "+str(sub, "name")+": import its YAML file separately")
	}
	title := str(page, "title")
	if title == "" {
		title = startTitle
	}
	doc := map[string]any{
		"settings": dashySettings(page, appConfig),
		"widgets":  widgetDocs,
		"boards":   []any{map[string]any{"name": title, "slug": startSlug, "sections": sections}},
	}
	return doc, report, nil
}

func dashySection(sec map[string]any, refs []any) map[string]any {
	display := mapOf(sec["displayData"])
	out := map[string]any{"title": str(sec, "name"), "widgets": refs}
	if truthy(display["collapsed"]) {
		out["collapsed"] = true
	}
	if n, err := strconv.Atoi(str(display, "cols")); err == nil && n > 0 {
		out["cols"] = n
	}
	switch size := enums.TileSize(str(display, "itemSize")); size {
	case enums.TileSmall, enums.TileMedium, enums.TileLarge:
		out["size"] = string(size)
	}
	if str(display, "sortBy") == string(enums.SortAlphabetical) {
		out["sort"] = string(enums.SortAlphabetical)
	}
	return out
}

func dashySettings(page, appConfig map[string]any) map[string]any {
	settings := map[string]any{}
	if v := str(page, "title"); v != "" {
		settings["title"] = v
	}
	if v := str(page, "description"); v != "" {
		settings["description"] = v
	}
	var nav []any
	for _, n := range list(page, "navLinks") {
		nav = append(nav, map[string]any{"title": str(n, "title"), "url": str(n, "path")})
	}
	if len(nav) > 0 {
		settings["nav"] = nav
	}
	if v := str(page, "footerText"); v != "" {
		settings["footer"] = v
	}
	if engine := str(mapOf(appConfig["webSearch"]), "customSearchEngine"); engine != "" {
		settings["search_engine"] = engine
	}
	return settings
}

func dashyIcon(icon string) string {
	for _, prefix := range dashyIconPrefixes {
		if strings.HasPrefix(icon, prefix) {
			return icon
		}
	}
	return ""
}

// codes turns "200, 401" into [200 401].
func codes(raw string) []any {
	var out []any
	for _, part := range strings.Split(raw, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func dashyItem(entry map[string]any, defaultStatus bool, report *Report) map[string]any {
	url := str(entry, "url")
	title := str(entry, "title")
	if title == "" {
		title = url
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		report.Skipped = append(report.Skipped, fmt.Sprintf("item %s: url %q", title, url))
		return nil
	}

	status := defaultStatus
	if v, ok := entry["statusCheck"].(bool); ok {
		status = v
	}
	statusMode, target := "off", string(enums.LinkNewTab)
	if status {
		statusMode = "http"
	}
	if str(entry, "target") == string(enums.LinkSameTab) {
		target = string(enums.LinkSameTab)
	}
	rawIcon := str(entry, "icon")
	icon := dashyIcon(rawIcon)
	if rawIcon != "" && icon == "" {
		report.Notes = append(report.Notes, title+": icon "+rawIcon+" → monogram")
	}

	config := map[string]any{
		"url": url, "description": str(entry, "description"), "icon": icon, "target": target,
		"status": statusMode, "status_url": str(entry, "statusCheckUrl"),
		"accept": codes(str(entry, "statusCheckAcceptCodes")), "insecure": truthy(entry["statusCheckAllowInsecure"]),
		"hotkey": str(entry, "hotkey"),
	}
	return map[string]any{"type": "link", "title": title, "config": config}
}

func dashyWidget(entry map[string]any, report *Report) map[string]any {
	kind := str(entry, "type")
	options := mapOf(entry["options"])
	if strings.HasPrefix(kind, dashyGlances) {
		report.Notes = append(report.Notes, "widget "+kind+": add a Glances connection, then a sysinfo widget")
		return nil
	}
	if note, ok := dashyConnectionWidgets[kind]; ok {
		report.Notes = append(report.Notes, "widget "+kind+": "+note)
		return nil
	}
	target, ok := dashyWidgets[kind]
	if !ok {
		report.Skipped = append(report.Skipped, "widget "+kind)
		return nil
	}

	title := str(entry, "label")
	if title == "" {
		title = str(options, "label")
	}
	item := map[string]any{"type": target, "title": title, "config": map[string]any{}}
	switch target {
	case "rss":
		limit, ok := intOf(options["limit"])
		if !ok {
			limit = defaultRSSLimit
		}
		feed := str(options, "rssUrl")
		if kind == "hackernews-trending" {
			feed = hackerNewsFeed
		}
		item["config"] = map[string]any{"url": feed, "limit": limit}
	case "clock":
		if zone := str(options, "timeZone"); zone != "" {
			item["config"] = map[string]any{"timezones": []any{zone}}
		}
	case "weather":
		lat, latOK := number(options["lat"])
		lon, lonOK := number(options["lon"])
		if !latOK || !lonOK {
			report.Skipped = append(report.Skipped, "widget "+kind+": needs lat/lon (city names are not resolved)")
			return nil
		}
		item["config"] = map[string]any{"lat": lat, "lon": lon}
	case "iframe":
		height, ok := intOf(options["frameHeight"])
		if !ok {
			height = defaultIframeSize
		}
		item["config"] = map[string]any{"url": str(options, "url"), "height": height}
	case "image":
		height, ok := intOf(options["imageHeight"])
		if !ok {
			height = defaultImageHeight
		}
		item["config"] = map[string]any{"url": str(options, "imagePath"), "height": height}
	case "rates":
		var symbols []any
		list, _ := options["outputCurrencies"].([]any)
		for _, s := range list {
			symbols = append(symbols, fmt.Sprint(s))
		}
		item["config"] = map[string]any{"base": str(options, "inputCurrency"), "symbols": symbols}
	}
	return item
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

// ImportDashy translates and merges a Dashy conf.yml into a space.
func ImportDashy(d *sql.DB, who *access.Principal, spaceID int64, text string) (*Report, error) {
	doc, pre, err := DashyToDoc(text)
	if err != nil {
		return nil, err
	}
	yamlText, err := dump(doc)
	if err != nil {
		return nil, err
	}
	report, err := ImportSpace(d, who, spaceID, yamlText, Merge)
	if err != nil {
		return nil, err
	}
	report.Skipped = append(pre.Skipped, report.Skipped...)
	report.Notes = append(pre.Notes, report.Notes...)
	return report, nil
}
