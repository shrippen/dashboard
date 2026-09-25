// Package crypto handles secrets at rest, password hashing and random tokens.
//
//	master key (Docker secret)
//	    │ HKDF(purpose)
//	    ▼
//	AES-256-GCM key ──► nonce(12) ‖ ciphertext‖tag   stored in *_enc columns
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
)

const (
	nonceLen   = 12
	keyLen     = 32
	tokenBytes = 32

	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonSaltLen = 16
	argonKeyLen  = 32
)

// Purpose scopes a derived key to one use, so a key never crosses purposes.
type Purpose string

const (
	PurposeCredential Purpose = "credential"
	PurposeTOTP       Purpose = "totp"
	PurposeNotify     Purpose = "notify"
	PurposeSetting    Purpose = "setting"
	PurposeHook       Purpose = "hook"
)

// ErrMissingKey means crypto was used before Init or without a master key.
var ErrMissingKey = errors.New("crypto: master key not initialised")

var master []byte

// Init sets the process-wide master key, derived from secret via MasterFrom.
func Init(secret string) {
	master = MasterFrom(secret)
}

// MasterFrom derives a 32-byte master key from an arbitrary secret string.
func MasterFrom(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func key(purpose Purpose, m []byte) ([]byte, error) {
	if m == nil {
		m = master
	}
	if m == nil {
		return nil, ErrMissingKey
	}
	out := make([]byte, keyLen)
	if _, err := hkdf.New(sha256.New, m, nil, []byte(purpose)).Read(out); err != nil {
		return nil, err
	}
	return out, nil
}

// Encrypt seals text under purpose, optionally with an explicit master key
// (used only for offline tools such as key rotation).
func Encrypt(text string, purpose Purpose, m []byte) ([]byte, error) {
	k, err := key(purpose, m)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, []byte(text), []byte(purpose))
	return append(nonce, ct...), nil
}

// Decrypt opens a blob sealed by Encrypt under the process master key.
func Decrypt(blob []byte, purpose Purpose) (string, error) {
	if len(blob) < nonceLen {
		return "", errors.New("crypto: ciphertext too short")
	}
	k, err := key(purpose, nil)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce, body := blob[:nonceLen], blob[nonceLen:]
	pt, err := gcm.Open(nil, nonce, body, []byte(purpose))
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// HashPassword returns a self-describing argon2id hash (PHC-like string).
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$%d$%d$%d$%s$%s",
		argonTime, argonMemory, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum)), nil
}

// CheckPassword verifies a password against a hash from HashPassword. A
// missing stored hash still spends comparable time, so missing accounts are
// not detectable by timing.
func CheckPassword(stored, password string) bool {
	if stored == "" {
		_, _ = HashPassword(password)
		return false
	}

	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[0] != "argon2id" {
		_, _ = HashPassword(password)
		return false
	}
	t, err1 := strconv.Atoi(parts[1])
	mem, err2 := strconv.Atoi(parts[2])
	threads, err3 := strconv.Atoi(parts[3])
	salt, err4 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err5 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil {
		return false
	}

	got := argon2.IDKey([]byte(password), salt, uint32(t), uint32(mem), uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NewToken returns a random URL-safe token for sessions, invites and resets.
func NewToken() string {
	b := make([]byte, tokenBytes)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// TokenHash stores tokens hashed: a database leak does not leak sessions.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum)
}

// Same does a constant-time string comparison (CSRF tokens, etc.).
func Same(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

// Sign returns a URL-safe HMAC of message under purpose, e.g. the secret
// part of an inbound webhook URL.
func Sign(message string, purpose Purpose) (string, error) {
	k, err := key(purpose, nil)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, k)
	mac.Write([]byte(message))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
