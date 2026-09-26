package outbound

// Target is the service instance a write goes to: its base URL, the
// caller's token and whether its TLS certificate is checked.
type Target struct {
	URL, Token string
	VerifyTLS  bool
}
