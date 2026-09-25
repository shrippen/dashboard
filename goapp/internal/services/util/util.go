// Package util holds small helpers shared by services.
package util

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

const slugMax = 60

var (
	nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)
	// diacritics strips combining marks left behind by a compatibility
	// decomposition, approximating Python's unicodedata NFKD + ASCII-drop.
	diacritics = regexp.MustCompile(`[\x{0300}-\x{036f}]`)
)

// Slug turns "Mein Board!" into "mein-board".
func Slug(text, fallback string) string {
	norm := diacritics.ReplaceAllString(nfkdApprox(text), "")
	value := strings.Trim(nonAlnum.ReplaceAllString(norm, "-"), "-")
	value = strings.ToLower(value)
	if len(value) > slugMax {
		value = value[:slugMax]
	}
	if value == "" {
		return fallback
	}
	return value
}

// nfkdApprox drops the most common Latin diacritics by simple
// transliteration, since Go's standard library has no built-in Unicode
// normalization. Good enough for slugs; not a full NFKD implementation.
func nfkdApprox(s string) string {
	replacer := strings.NewReplacer(
		"ä", "a", "ö", "o", "ü", "u", "Ä", "A", "Ö", "O", "Ü", "U", "ß", "ss",
		"é", "e", "è", "e", "ê", "e", "á", "a", "à", "a", "â", "a",
		"ó", "o", "ò", "o", "ô", "o", "ú", "u", "ù", "u", "û", "u",
		"ñ", "n", "ç", "c",
	)
	return replacer.Replace(s)
}

// Unique appends "-2", "-3", ... to base until it is not in taken.
func Unique(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for index := 2; ; index++ {
		candidate := base + "-" + strconv.Itoa(index)
		if !taken[candidate] {
			return candidate
		}
	}
}

// ErrConflict means an optimistic lock failed: somebody saved a newer
// version meanwhile.
var ErrConflict = errors.New("util: version conflict")

// ErrNotFound means the requested resource does not exist.
var ErrNotFound = errors.New("util: not found")
