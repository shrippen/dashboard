package sources

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"andon/internal/drivers/httpclient"
)

// Icon download adapter: resolves an icon spec to image bytes.
//
//	si-github        → Simple Icons (SVG)
//	hl-kimai         → Dashboard Icons (SVG, PNG fallback)
//	sh-kimai         → selfh.st icons (SVG, PNG fallback)
//	mdi-server       → Material Design Icons (SVG)
//	fas fa-rocket    → Font Awesome Free (SVG; fab = brands, far = regular)
//	favicon + url    → <origin>/favicon.ico
//	https://…/x.png  → as is

const (
	simpleIcons    = "https://cdn.jsdelivr.net/npm/simple-icons@latest/icons/%s.svg"
	dashboardIcons = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/%s/%s.%s"
	selfhstIcons   = "https://cdn.jsdelivr.net/gh/selfhst/icons/%s/%s.%s"
	mdiIcons       = "https://cdn.jsdelivr.net/npm/@mdi/svg@latest/svg/%s.svg"
	faIcons        = "https://cdn.jsdelivr.net/npm/@fortawesome/fontawesome-free@latest/svgs/%s/%s.svg"
	faSolid        = "solid"
	maxIcon        = 512 * 1024
	iconTimeout    = 10 * time.Second
	svgType        = "image/svg+xml"
	icoType        = "image/x-icon"
)

var iconTypes = map[string]bool{
	svgType: true, "image/png": true, icoType: true, "image/vnd.microsoft.icon": true,
	"image/jpeg": true, "image/webp": true, "image/gif": true,
}

var (
	svgScript   = regexp.MustCompile(`(?is)<script.*?</script>`)
	svgHandler  = regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*("[^"]*"|'[^']*')`)
	svgExternal = regexp.MustCompile(`(?i)(xlink:)?href\s*=\s*("[^"#][^"]*"|'[^'#][^']*')`)
	svgForeign  = regexp.MustCompile(`(?is)<foreignObject.*?</foreignObject>`)
)

// Icon is a downloaded image.
type Icon struct {
	Body      []byte
	MediaType string
}

// IconCandidates lists the URLs to try for spec, in order.
func IconCandidates(spec, pageURL string) []string {
	switch {
	case strings.HasPrefix(spec, "si-"):
		return []string{fmt.Sprintf(simpleIcons, spec[3:])}
	case strings.HasPrefix(spec, "hl-"):
		name := spec[3:]
		return []string{fmt.Sprintf(dashboardIcons, "svg", name, "svg"), fmt.Sprintf(dashboardIcons, "png", name, "png")}
	case strings.HasPrefix(spec, "sh-"):
		name := spec[3:]
		return []string{fmt.Sprintf(selfhstIcons, "svg", name, "svg"), fmt.Sprintf(selfhstIcons, "png", name, "png")}
	case strings.HasPrefix(spec, "mdi-"):
		return []string{fmt.Sprintf(mdiIcons, spec[4:])}
	case IsFontAwesome(spec):
		style, name := fontAwesome(spec)
		if name == "" {
			return nil
		}
		return []string{fmt.Sprintf(faIcons, style, name)}
	case spec == "favicon":
		u, err := url.Parse(pageURL)
		if err != nil || u.Host == "" {
			return nil
		}
		return []string{u.Scheme + "://" + u.Host + "/favicon.ico"}
	case strings.HasPrefix(spec, "http://") || strings.HasPrefix(spec, "https://"):
		return []string{spec}
	}
	return nil
}

// faStyles maps Font Awesome style classes (v5 and v6) to its SVG folders.
var faStyles = map[string]string{
	"fas": faSolid, "fa-solid": faSolid, "fab": "brands", "fa-brands": "brands", "far": "regular", "fa-regular": "regular",
}

// IsFontAwesome: "fas fa-rocket", "fa-brands fa-github".
func IsFontAwesome(spec string) bool {
	first, _, _ := strings.Cut(spec, " ")
	_, ok := faStyles[first]
	return ok
}

func fontAwesome(spec string) (style, name string) {
	style = faSolid
	for _, part := range strings.Fields(spec) {
		if s, ok := faStyles[part]; ok {
			style = s
			continue
		}
		if n, ok := strings.CutPrefix(part, "fa-"); ok && n != "" {
			name = n
		}
	}
	return style, name
}

// CleanSVG removes scripts, event handlers, foreign objects and external
// references, so an SVG is safe to serve from our origin.
func CleanSVG(body []byte) []byte {
	text := svgScript.ReplaceAll(body, nil)
	text = svgForeign.ReplaceAll(text, nil)
	text = svgHandler.ReplaceAll(text, nil)
	return svgExternal.ReplaceAll(text, nil)
}

// FetchIcon downloads the first usable image for spec.
func FetchIcon(ctx context.Context, spec, pageURL string) (Icon, error) {
	for _, candidate := range IconCandidates(spec, pageURL) {
		icon, ok := tryIcon(ctx, candidate)
		if ok {
			return icon, nil
		}
	}
	return Icon{}, newSourceError("icon not found")
}

func tryIcon(ctx context.Context, target string) (Icon, bool) {
	resp, err := httpclient.Request(ctx, http.MethodGet, target, httpclient.Options{Timeout: iconTimeout})
	if err != nil {
		return Icon{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Icon{}, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIcon+1))
	if err != nil || len(body) > maxIcon {
		return Icon{}, false
	}

	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	media = strings.ToLower(media)
	switch {
	case strings.HasSuffix(target, ".svg") || media == svgType:
		return Icon{Body: CleanSVG(body), MediaType: svgType}, true
	case iconTypes[media]:
		return Icon{Body: body, MediaType: media}, true
	case strings.HasSuffix(target, ".ico"):
		return Icon{Body: body, MediaType: icoType}, true
	}
	return Icon{}, false
}
