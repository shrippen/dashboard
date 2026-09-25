package sources

// Picture widgets: the latest xkcd and NASA's astronomy picture of the
// day. Images are inlined like the image widget's, so the browser never
// contacts the image host.

import (
	"context"
	"net/url"
	"time"

	"dashboard/internal/drivers/httpclient"
	"dashboard/internal/enums"
)

const (
	pictureTTL = 6 * time.Hour
	apodMax    = 4 << 20 // APOD images are larger than icons and photos
	nasaDemo   = "DEMO_KEY"
	apodImage  = "image"
)

var (
	xkcdBase = "https://xkcd.com"
	nasaBase = "https://api.nasa.gov"
)

// Picture is a titled image; DataURI is "" when only Link can be shown
// (a video, or an image too large to inline).
type Picture struct {
	Title, Text, Link, DataURI string
}

// ── xkcd ──

type XkcdSource struct{}

func (XkcdSource) Key() string                { return "xkcd" }
func (XkcdSource) TTL() time.Duration         { return pictureTTL }
func (XkcdSource) Service() enums.ServiceType { return "" }

func (XkcdSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	body, _, err := httpclient.GetJSON(ctx, xkcdBase+"/info.0.json", httpclient.Options{})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	m := asMap(body)
	pic := &Picture{Title: asStr(m["safe_title"]), Text: asStr(m["alt"]), Link: xkcdBase + "/" + asStr(m["num"]) + "/"}
	img, err := fetchImage(ctx, asStr(m["img"]), imageMax)
	if err != nil {
		return nil, err
	}
	pic.DataURI = img.DataURI
	return pic, nil
}

// ── apod ──

type ApodSource struct{}

func (ApodSource) Key() string                { return "apod" }
func (ApodSource) TTL() time.Duration         { return pictureTTL }
func (ApodSource) Service() enums.ServiceType { return "" }

// Fetch uses the widget's own API key or NASA's rate-limited DEMO_KEY.
func (ApodSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	key := asStr(sctx.Params["api_key"])
	if key == "" {
		key = nasaDemo
	}
	body, _, err := httpclient.GetJSON(ctx, nasaBase+"/planetary/apod", httpclient.Options{Params: url.Values{"api_key": {key}}})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	m := asMap(body)
	pic := &Picture{Title: asStr(m["title"]), Text: asStr(m["explanation"]), Link: asStr(m["url"])}
	if asStr(m["media_type"]) != apodImage {
		return pic, nil
	}

	// A picture that fails to inline still shows as a link.
	if img, err := fetchImage(ctx, pic.Link, apodMax); err == nil {
		pic.DataURI = img.DataURI
	}
	return pic, nil
}

func init() {
	Register(XkcdSource{})
	Register(ApodSource{})
}
