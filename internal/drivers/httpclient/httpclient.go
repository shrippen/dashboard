// Package httpclient is the shared HTTP client with timeouts and an egress
// guard. Every outbound request of the app passes the guard, so the admin
// can restrict reachable networks and invited users cannot scan the
// internal network through status checks or feeds.
package httpclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

const (
	ConnectTimeout = 5 * time.Second
	ReadTimeout    = 15 * time.Second
	UserAgent      = "andon/0.1 (+https://github.com/shrippen/dashboard)"
	MaxBody        = 5 * 1024 * 1024
)

// HttpError is a transport or status failure with a short, secret-free
// message (never wraps a raw error that might contain a token or URL).
type HttpError struct{ msg string }

func (e HttpError) Error() string { return e.msg }

// EgressDenied means the guard rejected the target host/addresses.
type EgressDenied struct{ Host string }

func (e EgressDenied) Error() string { return "egress denied: " + e.Host }

// Guard decides whether a host (already resolved to addrs) may be reached.
// A nil guard (the default) allows everything.
type Guard func(host string, addrs []net.IP) bool

var guard Guard

// SetGuard installs the process-wide egress guard, or nil to allow all.
func SetGuard(g Guard) { guard = g }

func checkGuard(rawURL string) error {
	if guard == nil {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return HttpError{"bad url"}
	}
	addrs, err := net.LookupIP(u.Hostname())
	if err != nil {
		return HttpError{"dns: " + u.Hostname()}
	}
	if !guard(u.Hostname(), addrs) {
		return EgressDenied{u.Hostname()}
	}
	return nil
}

// Options configure one Request call.
type Options struct {
	Headers map[string]string
	Params  url.Values
	// Body is the raw request body (e.g. a POST's JSON payload). nil for
	// none.
	Body []byte
	// SkipVerify disables TLS certificate verification. Defaults to false
	// (verified) so a zero-value Options is always safe; set true only for
	// a connection the user explicitly marked as self-signed.
	SkipVerify bool
	Timeout    time.Duration
	// NoRedirect returns a redirect as is, e.g. to read a login cookie.
	NoRedirect bool
}

// Request performs one guarded HTTP call and returns the raw response. The
// caller must close resp.Body.
func Request(ctx context.Context, method, rawURL string, opts Options) (*http.Response, error) {
	if err := checkGuard(rawURL); err != nil {
		return nil, err
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, HttpError{"bad url"}
	}
	if opts.Params != nil {
		u.RawQuery = opts.Params.Encode()
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = ReadTimeout
	}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: opts.SkipVerify}, //nolint:gosec // opt-in per connection
			TLSHandshakeTimeout: ConnectTimeout,
		},
	}
	if opts.NoRedirect {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}

	var body io.Reader
	if opts.Body != nil {
		body = bytes.NewReader(opts.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, HttpError{"bad request"}
	}
	req.Header.Set("User-Agent", UserAgent)
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, transportError(err, u.Hostname())
	}
	return resp, nil
}

// transportError names why a request failed without the raw error, which
// may carry the full URL: "dns: api.example.org", "connection refused:
// 10.0.0.5", "tls certificate: …".
func transportError(err error, host string) HttpError {
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var unknownCA x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return HttpError{"timeout: " + host}
	case errors.As(err, &dnsErr):
		return HttpError{"dns: " + host}
	case errors.Is(err, syscall.ECONNREFUSED):
		return HttpError{"connection refused: " + host}
	case errors.As(err, &unknownCA), errors.As(err, &hostnameErr), errors.As(err, &certErr):
		return HttpError{"tls certificate: " + host}
	default:
		return HttpError{"request failed: " + host}
	}
}

// GetJSON performs a guarded GET and decodes a JSON body, returning the
// response headers too (callers need X-Total-Pages etc.).
func GetJSON(ctx context.Context, rawURL string, opts Options) (any, http.Header, error) {
	resp, err := Request(ctx, http.MethodGet, rawURL, opts)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return nil, nil, HttpError{"read failed"}
	}
	if len(body) > MaxBody {
		return nil, nil, HttpError{"response too large"}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, resp.Header, HttpError{fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}

	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, resp.Header, HttpError{"invalid JSON"}
	}
	return parsed, resp.Header, nil
}

// GetText performs a guarded GET and returns the body as text (e.g.
// Prometheus metrics).
func GetText(ctx context.Context, rawURL string, opts Options) (string, error) {
	return doText(ctx, http.MethodGet, rawURL, opts)
}

// PostFormText performs a guarded POST with opts.Params as an
// x-www-form-urlencoded body (e.g. a ClientLogin endpoint that 404s a GET)
// and returns the body as text.
func PostFormText(ctx context.Context, rawURL string, opts Options) (string, error) {
	form := opts.Params.Encode()
	opts.Body = []byte(form)
	opts.Params = nil
	if opts.Headers == nil {
		opts.Headers = map[string]string{}
	}
	opts.Headers["Content-Type"] = "application/x-www-form-urlencoded"
	return doText(ctx, http.MethodPost, rawURL, opts)
}

func doText(ctx context.Context, method, rawURL string, opts Options) (string, error) {
	resp, err := Request(ctx, method, rawURL, opts)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return "", HttpError{"read failed"}
	}
	if len(body) > MaxBody {
		return "", HttpError{"response too large"}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", HttpError{fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return string(body), nil
}

// PeerCert connects to host:port over TLS and returns the leaf
// certificate without verifying it, so expired ones can be reported too.
func PeerCert(ctx context.Context, hostPort string) (*x509.Certificate, error) {
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, HttpError{"bad host"}
	}
	if err := checkGuard("https://" + hostPort); err != nil {
		return nil, err
	}

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: ConnectTimeout},
		Config:    &tls.Config{ServerName: host, InsecureSkipVerify: true}, //nolint:gosec // only reads the certificate
	}
	conn, err := dialer.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return nil, HttpError{"connect failed"}
	}
	defer conn.Close()

	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, HttpError{"no certificate"}
	}
	return certs[0], nil
}

// guardedTransport applies the egress guard to clients this package does
// not build itself (e.g. an SDK's).
type guardedTransport struct{ next http.RoundTripper }

func (g guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := checkGuard(req.URL.String()); err != nil {
		return nil, err
	}
	return g.next.RoundTrip(req)
}

// Client returns an http.Client whose requests pass the egress guard.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: guardedTransport{http.DefaultTransport}}
}

// TLS says whether a request checks the server's certificate.
type TLS int

const (
	TLSVerify TLS = iota // check it (the default)
	TLSSkip              // accept self-signed homelab certificates
)

// TLSOf maps a connection's "verify TLS" setting.
func TLSOf(verify bool) TLS {
	if verify {
		return TLSVerify
	}
	return TLSSkip
}

// ClientTLS is Client with TLS verification switchable per connection
// (self-signed homelab certificates).
func ClientTLS(timeout time.Duration, mode TLS) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: mode == TLSSkip} //nolint:gosec // opt-in per connection
	return &http.Client{Timeout: timeout, Transport: guardedTransport{base}}
}

// CheckHost applies the egress guard to a non-HTTP connection (IMAP).
func CheckHost(host string) error {
	return checkGuard("https://" + host)
}
