package util

import (
	"testing"

	"dashboard/internal/crypto"
)

func TestHeadersSealed(t *testing.T) {
	crypto.Init("test-secret-with-enough-length-0123456789")
	sealed, err := SealSecrets(map[string]any{"url": "u", "headers": map[string]any{"X-Api": "tok"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, plain := sealed["headers"]; plain || sealed["headers_enc"] == nil {
		t.Fatalf("not sealed: %v", sealed)
	}
	if got := OpenSecrets(sealed)["headers"].(map[string]any)["X-Api"]; got != "tok" {
		t.Fatalf("open: %v", got)
	}

	// Empty keeps, "-" clears.
	kept, _ := SealSecrets(map[string]any{"headers": map[string]any{}}, sealed)
	if kept["headers_enc"] != sealed["headers_enc"] {
		t.Fatal("empty form dropped headers")
	}
	cleared, _ := SealSecrets(map[string]any{"headers": SecretClear}, sealed)
	if cleared["headers_enc"] != nil {
		t.Fatal("clear kept headers")
	}
	if StripSecrets(sealed)["headers_enc"] != nil {
		t.Fatal("export keeps secret")
	}
}

func TestStringSecret(t *testing.T) {
	crypto.Init("test-secret-with-enough-length-0123456789")
	sealed, err := SealSecrets(map[string]any{"api_key": "k1", "limit": 3.0}, nil)
	if err != nil || sealed["api_key"] != nil || sealed["limit"] != 3.0 {
		t.Fatalf("sealed: %v %v", sealed, err)
	}
	if OpenSecrets(sealed)["api_key"] != "k1" {
		t.Fatal("open")
	}
	if _, ok := sealed["api_key"]; ok {
		t.Fatal("Open changed the stored config")
	}
}
