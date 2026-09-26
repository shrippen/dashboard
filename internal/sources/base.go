// Package sources turns one query against one service into JSON-able
// domain data:
//
//	Get("rss").Fetch(ctx) -> {"items": [...]}
//
// Sources never see users or permissions; services decide who may ask.
package sources

import (
	"context"
	"fmt"
	"time"

	"andon/internal/drivers/httpclient"
	"andon/internal/enums"
)

// SourceError is an expected failure (service down, bad token); its
// message is shown to the user.
type SourceError struct{ msg string }

func (e SourceError) Error() string { return e.msg }

func newSourceError(format string, args ...any) SourceError {
	return SourceError{fmt.Sprintf(format, args...)}
}

// Ctx is what a source needs to run one fetch: the connection's own
// settings plus per-call parameters (e.g. an RSS feed's URL and limit).
type Ctx struct {
	URL       string
	Secret    string
	VerifyTLS bool
	Options   map[string]any
	Params    map[string]any
	Events    []Pushed // push sources only, oldest first
}

// TLS is the connection's certificate check for driver calls.
func (c Ctx) TLS() httpclient.TLS { return httpclient.TLSOf(c.VerifyTLS) }

// Pushed is one event a service sent to the dashboard's webhook.
type Pushed struct {
	Event, Subject string
	At             time.Time
}

// PushSource is a source whose data arrives by webhook: the caller loads
// the events of the last PushWindow into Ctx.Events.
type PushSource interface {
	Source
	PushWindow() time.Duration
}

// Source is one named, cacheable query against a service.
type Source interface {
	Key() string
	TTL() time.Duration
	Service() enums.ServiceType // "" if not tied to one service (e.g. rss)
	Fetch(ctx context.Context, sctx Ctx) (any, error)
}

var registry = map[string]Source{}

// Register adds a source to the process-wide registry.
func Register(s Source) Source {
	registry[s.Key()] = s
	return s
}

// Get looks up a source by key.
func Get(key string) (Source, error) {
	s, ok := registry[key]
	if !ok {
		return nil, fmt.Errorf("sources: unknown source %q", key)
	}
	return s, nil
}

// dataAliases are services whose dataset source has another key.
var dataAliases = map[enums.ServiceType]string{enums.ServiceGlances: "glances"}

// DataKey is the key of a service's dataset source ("kimai.data",
// "glances").
func DataKey(service enums.ServiceType) string {
	if key, ok := dataAliases[service]; ok {
		return key
	}
	return string(service) + ".data"
}
