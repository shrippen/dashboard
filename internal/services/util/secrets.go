package util

// Widget config secrets. Status-check headers often carry tokens, so they
// never rest in plain JSON:
//
//	form {headers: {X-Api: t}} ──Seal──► config {headers_enc: base64(AES-GCM)}
//	config ──Open──► {headers: {X-Api: t}} (in memory, for the check only)
//	config ──Strip──► export without headers_enc

import (
	"encoding/base64"
	"encoding/json"

	"dashboard/internal/crypto"
)

const (
	headersKey    = "headers"
	headersEncKey = "headers_enc"

	// HeadersClear as the headers value removes stored headers.
	HeadersClear = "-"
)

func copyConfig(config map[string]any) map[string]any {
	out := make(map[string]any, len(config))
	for k, v := range config {
		out[k] = v
	}
	return out
}

// SealHeaders encrypts config["headers"]. An empty value keeps the headers
// of prev, HeadersClear drops them.
func SealHeaders(config, prev map[string]any) (map[string]any, error) {
	out := copyConfig(config)
	raw, present := out[headersKey]
	delete(out, headersKey)
	delete(out, headersEncKey)

	if s, _ := raw.(string); s == HeadersClear {
		return out, nil
	}
	headers, _ := raw.(map[string]any)
	if len(headers) == 0 || !present {
		if old, ok := prev[headersEncKey]; ok {
			out[headersEncKey] = old
		}
		return out, nil
	}

	plain, err := json.Marshal(headers)
	if err != nil {
		return nil, err
	}
	blob, err := crypto.Encrypt(string(plain), crypto.PurposeCredential, nil)
	if err != nil {
		return nil, err
	}
	out[headersEncKey] = base64.StdEncoding.EncodeToString(blob)
	return out, nil
}

// OpenHeaders returns config with the stored headers decrypted; on any
// error the check simply runs without them.
func OpenHeaders(config map[string]any) map[string]any {
	enc, _ := config[headersEncKey].(string)
	if enc == "" {
		return config
	}
	blob, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return config
	}
	plain, err := crypto.Decrypt(blob, crypto.PurposeCredential)
	if err != nil {
		return config
	}
	var headers map[string]any
	if json.Unmarshal([]byte(plain), &headers) != nil {
		return config
	}
	out := copyConfig(config)
	out[headersKey] = headers
	return out
}

// StripSecrets drops encrypted values: they are bound to this instance's
// key and must not leave it.
func StripSecrets(config map[string]any) map[string]any {
	if _, ok := config[headersEncKey]; !ok {
		return config
	}
	out := copyConfig(config)
	delete(out, headersEncKey)
	return out
}
