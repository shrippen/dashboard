package util

import (
	"testing"

	"dashboard/internal/crypto"
)

func TestHeadersSealed(t *testing.T) {
	crypto.Init("test-secret-with-enough-length-0123456789")
	sealed, err := SealHeaders(map[string]any{"url": "u", "headers": map[string]any{"X-Api": "tok"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, plain := sealed["headers"]; plain || sealed["headers_enc"] == nil {
		t.Fatalf("not sealed: %v", sealed)
	}
	if got := OpenHeaders(sealed)["headers"].(map[string]any)["X-Api"]; got != "tok" {
		t.Fatalf("open: %v", got)
	}

	// Empty keeps, "-" clears.
	kept, _ := SealHeaders(map[string]any{"headers": map[string]any{}}, sealed)
	if kept["headers_enc"] != sealed["headers_enc"] {
		t.Fatal("empty form dropped headers")
	}
	cleared, _ := SealHeaders(map[string]any{"headers": HeadersClear}, sealed)
	if cleared["headers_enc"] != nil {
		t.Fatal("clear kept headers")
	}
	if StripSecrets(sealed)["headers_enc"] != nil {
		t.Fatal("export keeps secret")
	}
}
