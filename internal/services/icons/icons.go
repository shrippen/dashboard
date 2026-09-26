// Package icons caches link-tile icons: download once, serve locally,
// never block a page render.
//
//	URL(spec) ──cached?──► /icons/<key>
//	          └─missing──► "" (template shows a monogram), fetch in background
package icons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"andon/internal/repos/files"
	"andon/internal/sources"
)

const (
	area         = "cache"
	missSuffix   = ".miss"
	typeSuffix   = ".type"
	uploadPrefix = "upload:"
	urlPrefix    = "/icons/"
	keyLen       = 24
	maxUpload    = 512 * 1024
	fetchTimeout = 30 * time.Second
	defaultType  = "image/png"
	faviconSpec  = "favicon"
	svgMediaType = "image/svg+xml"
)

var uploadTypes = map[string]bool{
	svgMediaType: true, "image/png": true, "image/webp": true, "image/jpeg": true, "image/x-icon": true,
}

// ErrInvalid means an uploaded icon is too large or not an image.
var ErrInvalid = errors.New("icon.invalid")

var (
	storeMu  sync.RWMutex
	store    = files.New("")
	pending  = map[string]bool{}
	pendMu   sync.Mutex
	inFlight sync.WaitGroup
)

// Init sets the icon directory (DATA_DIR/icons).
func Init(dir string) {
	storeMu.Lock()
	store = files.New(dir)
	storeMu.Unlock()
}

func disk() files.Store {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return store
}

func hashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])[:keyLen]
}

func specKey(spec, pageURL string) string {
	if spec == faviconSpec {
		return hashKey(faviconSpec + ":" + pageURL)
	}
	return hashKey(spec)
}

// glyphPrefixes are single-color icon sets: black shapes that need
// inverting on dark themes.
var glyphPrefixes = []string{"si-", "mdi-"}

// Glyph reports whether spec's icon is a single-color glyph.
func Glyph(spec string) bool {
	spec = strings.TrimSpace(spec)
	for _, p := range glyphPrefixes {
		if strings.HasPrefix(spec, p) {
			return true
		}
	}
	return sources.IsFontAwesome(spec)
}

const (
	emojiMin    = 0x2000 // below: letters and common symbols
	emojiMaxLen = 8      // runes; flags and ZWJ sequences need several
	hexBase     = 16
)

// Emoji returns the emoji spec stands for ("🚀", "U+1F680", "1f680"),
// or "" if it is none. Emojis render as text, nothing is downloaded.
func Emoji(spec string) string {
	spec = strings.TrimSpace(spec)
	hex := strings.TrimPrefix(strings.TrimPrefix(spec, "U+"), "u+")
	if n, err := strconv.ParseInt(hex, hexBase, 32); err == nil && len(hex) >= 4 && len(hex) <= 6 {
		r := rune(n)
		if r >= emojiMin && utf8.ValidRune(r) {
			return string(r)
		}
		return ""
	}

	runes := []rune(spec)
	if len(runes) == 0 || len(runes) > emojiMaxLen {
		return ""
	}
	for _, r := range runes {
		if r < emojiMin && r != zeroWidthJoiner {
			return ""
		}
	}
	return spec
}

const zeroWidthJoiner = 0x200D

// URL returns the local URL of spec's icon, or "" while it is missing
// (a background download starts on first request).
func URL(spec, pageURL string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	if key, ok := strings.CutPrefix(spec, uploadPrefix); ok {
		return urlPrefix + key
	}
	if len(sources.IconCandidates(spec, pageURL)) == 0 {
		return ""
	}

	key := specKey(spec, pageURL)
	s := disk()
	if s.Exists(area, key) {
		return urlPrefix + key
	}
	if s.Exists(area, key+missSuffix) {
		return ""
	}
	schedule(spec, pageURL, key)
	return ""
}

func schedule(spec, pageURL, key string) {
	pendMu.Lock()
	if pending[key] {
		pendMu.Unlock()
		return
	}
	pending[key] = true
	pendMu.Unlock()

	inFlight.Add(1)
	go func() {
		defer inFlight.Done()
		defer func() {
			pendMu.Lock()
			delete(pending, key)
			pendMu.Unlock()
		}()
		download(spec, pageURL, key)
	}()
}

func download(spec, pageURL, key string) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	s := disk()
	icon, err := sources.FetchIcon(ctx, spec, pageURL)
	if err != nil {
		// Icons are cosmetic: remember the miss, retry after ForgetMisses.
		_ = s.Put(area, key+missSuffix, []byte(spec))
		return
	}
	if err := save(s, key, icon.Body, icon.MediaType); err != nil {
		slog.Warn("icon store", "spec", spec, "err", err)
	}
}

func save(s files.Store, key string, body []byte, mediaType string) error {
	if err := s.Put(area, key+typeSuffix, []byte(mediaType)); err != nil {
		return err
	}
	return s.Put(area, key, body)
}

// Wait blocks until running downloads finish (tests, shutdown).
func Wait() { inFlight.Wait() }

// Read returns a cached icon and its media type.
func Read(key string) ([]byte, string, bool) {
	if len(key) != keyLen || strings.Trim(key, "0123456789abcdef") != "" {
		return nil, "", false
	}
	s := disk()
	body, err := s.Get(area, key)
	if err != nil {
		return nil, "", false
	}
	media := defaultType
	if raw, err := s.Get(area, key+typeSuffix); err == nil {
		media = string(raw)
	}
	return body, media, true
}

// Upload stores an uploaded icon; returns the spec to put into a link widget.
func Upload(body []byte, mediaType string) (string, error) {
	if len(body) == 0 || len(body) > maxUpload || !uploadTypes[mediaType] {
		return "", ErrInvalid
	}
	if mediaType == svgMediaType {
		body = sources.CleanSVG(body)
	}
	key := hashKey(string(body))
	if err := save(disk(), key, body, mediaType); err != nil {
		return "", err
	}
	return uploadPrefix + key, nil
}

// ForgetMisses lets failed icons be retried (daily job).
func ForgetMisses() error {
	s := disk()
	names, err := s.Names(area)
	if err != nil {
		return err
	}
	for _, name := range names {
		if strings.HasSuffix(name, missSuffix) {
			if err := s.Remove(area, name); err != nil {
				return err
			}
		}
	}
	return nil
}
