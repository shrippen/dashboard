// Package outbound sends notifications through the user's own Apprise API
// instance (https://github.com/caronc/apprise-api), reached over HTTP.
//
// There is no Apprise library for Go: this calls an Apprise API
// instance's stateless POST /notify endpoint.
package outbound

import (
	"context"
	"encoding/json"
	"errors"

	"dashboard/internal/drivers/httpclient"
)

// ErrNotifyFailed means the Apprise API reachably rejected the request
// (bad URL, delivery failure) — distinct from a network/transport error.
var ErrNotifyFailed = errors.New("outbound: notify failed")

type notifyRequest struct {
	URLs  string `json:"urls"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Send posts one notification to apiURL/notify with the given apprise://
// target URL(s) (comma-separated for more than one).
func Send(ctx context.Context, apiURL, urls, title, body string) error {
	payload, err := json.Marshal(notifyRequest{URLs: urls, Title: title, Body: body})
	if err != nil {
		return err
	}
	resp, err := httpclient.Request(ctx, "POST", apiURL+"/notify", httpclient.Options{
		Headers: map[string]string{"Content-Type": "application/json"}, Body: payload,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ErrNotifyFailed
	}
	return nil
}
