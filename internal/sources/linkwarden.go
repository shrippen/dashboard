package sources

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"andon/internal/drivers/services"
	"andon/internal/enums"
)

const lwMaxPages = 20

// Bookmark is one saved link.
type Bookmark struct {
	Name, URL, Collection string
}

type LinkwardenDataset struct {
	URL         string
	Collections []string
	Links       []Bookmark
}

type LinkwardenData struct{}

func (LinkwardenData) Key() string                { return "linkwarden.data" }
func (LinkwardenData) TTL() time.Duration         { return dataTTL }
func (LinkwardenData) Service() enums.ServiceType { return enums.ServiceLinkwarden }

// Fetch reads the collections named in options.collections (all if
// empty) and their links.
func (LinkwardenData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoLinkwarden(), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	api := services.LinkwardenApi{URL: sctx.URL, Token: secret, Verify: sctx.VerifyTLS}
	data, err := loadLinkwarden(ctx, api, sctx)
	if err != nil {
		return nil, fetchError(err)
	}
	return data, nil
}

func loadLinkwarden(ctx context.Context, api services.LinkwardenApi, sctx Ctx) (*LinkwardenDataset, error) {
	wanted := map[string]bool{}
	for _, c := range asList(sctx.Options["collections"]) {
		wanted[strings.ToLower(strings.TrimSpace(asStr(c)))] = true
	}

	cols, err := api.Get(ctx, "collections", nil)
	if err != nil {
		return nil, err
	}
	data := &LinkwardenDataset{URL: sctx.URL}
	for _, raw := range asList(cols) {
		c := asMap(raw)
		name := asStr(c["name"])
		if len(wanted) > 0 && !wanted[strings.ToLower(name)] {
			continue
		}
		data.Collections = append(data.Collections, name)
		links, err := collectionLinks(ctx, api, asInt64(c["id"]), name)
		if err != nil {
			return nil, err
		}
		data.Links = append(data.Links, links...)
	}
	return data, nil
}

// collectionLinks pages with Linkwarden's cursor (the last link id).
func collectionLinks(ctx context.Context, api services.LinkwardenApi, id int64, name string) ([]Bookmark, error) {
	var out []Bookmark
	cursor := ""
	for range lwMaxPages {
		params := url.Values{"collectionId": {strconv.FormatInt(id, 10)}}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		page, err := api.Get(ctx, "links", params)
		if err != nil {
			return nil, err
		}
		list := asList(page)
		if len(list) == 0 {
			break
		}
		for _, raw := range list {
			l := asMap(raw)
			out = append(out, Bookmark{Name: asStr(l["name"]), URL: asStr(l["url"]), Collection: name})
		}
		next := strconv.FormatInt(asInt64(asMap(list[len(list)-1])["id"]), 10)
		if next == cursor {
			break
		}
		cursor = next
	}
	return out, nil
}

func init() {
	Register(LinkwardenData{})
	Register(testOf{LinkwardenData{}, func(d any) map[string]any { return map[string]any{"links": len(d.(*LinkwardenDataset).Links)} }})
}
