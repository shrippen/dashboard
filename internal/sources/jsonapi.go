package sources

// Generic JSON connection: any URL, optional token. Its dataset is the
// decoded body, so custom rules can test any field ("stats.users > 100").
//
//	options.header  name of the token header; default "Authorization: Bearer <token>"

import (
	"context"
	"time"

	"dashboard/internal/drivers/httpclient"
	"dashboard/internal/enums"
)

const bearerPrefix = "Bearer "

type JSONAPIData struct{}

func (JSONAPIData) Key() string                { return "jsonapi.data" }
func (JSONAPIData) TTL() time.Duration         { return opsTTL }
func (JSONAPIData) Service() enums.ServiceType { return enums.ServiceJSONAPI }

func (JSONAPIData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return map[string]any{"stats": map[string]any{"users": 42.0, "queue": 3.0}}, nil
	}
	headers := map[string]string{"Accept": "application/json"}
	if sctx.Secret != "" {
		if name := asStr(sctx.Options["header"]); name != "" {
			headers[name] = sctx.Secret
		} else {
			headers["Authorization"] = bearerPrefix + sctx.Secret
		}
	}
	body, _, err := httpclient.GetJSON(ctx, sctx.URL, httpclient.Options{Headers: headers, SkipVerify: !sctx.VerifyTLS})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	return body, nil
}

func init() {
	Register(JSONAPIData{})
	Register(testOf{JSONAPIData{}, func(d any) map[string]any { return map[string]any{"fields": len(asMap(d))} }})
}
