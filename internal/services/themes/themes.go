// Package themes handles design tokens per color mode, rendered to one
// stylesheet.
//
//	theme = {dark: {--bg-void: #141312, ...}, light: {...overrides}, customCSS}
//	css   = :root{dark} :root[data-theme=light]{light} @media(auto -> light)
//
// The token names of the shrippen design system are the theme contract.
// Only shrippen ships; users duplicate it and change values.
package themes

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/misc"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/audit"
	"dashboard/internal/services/util"
)

//go:embed builtin/shrippen/tokens.css builtin/shrippen/theme.json
var builtinFiles embed.FS

const (
	builtinSlug    = "shrippen"
	contractVer    = 1
	defaultSetting = "theme_default"
	maxCSS         = 50_000
	maxZip         = 2 * 1024 * 1024
	aaText         = 4.5
)

// extraDark/extraLight are tokens the dashboard adds on top of the design
// system (components hardcode these).
var (
	extraDark  = map[string]string{"--nav-bg": "rgba(20,19,18,.92)", "--shadow": "rgba(0,0,0,.35)"}
	extraLight = map[string]string{"--nav-bg": "rgba(240,233,214,.92)", "--shadow": "rgba(60,56,54,.18)"}
)

var textPairs = [][2]string{
	{"--fg1", "--bg-void"}, {"--fg1", "--bg-panel"}, {"--fg2", "--bg-panel"},
	{"--fg0", "--bg-void"}, {"--blue", "--bg-void"}, {"--fg3", "--bg-panel"},
}

var (
	blockRe   = regexp.MustCompile(`(?s):root(\[data-theme="light"\])?\s*\{(.*?)\}`)
	declRe    = regexp.MustCompile(`(?i)(--[a-z0-9-]+)\s*:\s*([^;]+);`)
	commentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	safeValue = regexp.MustCompile(`^[#a-zA-Z0-9 ,.'"()%+\-/]*$`)
	hexColor  = regexp.MustCompile(`(?i)^#([0-9a-f]{3}|[0-9a-f]{6})$`)
)

var forbidden = []string{"url(", "expression", "@import", "javascript:", "\\"}

// ErrTheme is a validation failure with a translatable message key.
type ErrTheme struct{ Key string }

func (e ErrTheme) Error() string { return e.Key }

var (
	ErrNotFound = util.ErrNotFound
	ErrDenied   = access.ErrDenied
)

// Mode is a color scheme.
type Mode string

const (
	ModeDark  Mode = "dark"
	ModeLight Mode = "light"
)

// Ref is a lightweight theme listing entry.
type Ref struct {
	ID      int64
	Slug    string
	Name    string
	Builtin bool
	SpaceID *int64
	CanEdit bool
}

// ContrastIssue is one text/background pair failing WCAG AA.
type ContrastIssue struct {
	Mode  Mode
	FG    string
	BG    string
	Ratio float64
}

// ── Parsing and rendering ──

// ParseCSS extracts :root {...} and :root[data-theme="light"] {...}
// declarations into (dark, light) token maps.
func ParseCSS(text string) (dark, light map[string]string) {
	text = commentRe.ReplaceAllString(text, "")
	dark, light = map[string]string{}, map[string]string{}
	for _, m := range blockRe.FindAllStringSubmatch(text, -1) {
		target := dark
		if m[1] != "" {
			target = light
		}
		for _, d := range declRe.FindAllStringSubmatch(m[2], -1) {
			target[d[1]] = strings.Join(strings.Fields(d[2]), " ")
		}
	}
	return dark, light
}

var (
	contractDark, contractLight map[string]string
)

// Contract returns the token names and default values: the builtin theme
// plus the dashboard's own extras. Computed once and cached.
func Contract() (map[string]string, map[string]string) {
	if contractDark == nil {
		raw, err := builtinFiles.ReadFile("builtin/shrippen/tokens.css")
		if err != nil {
			panic("themes: missing embedded shrippen tokens.css: " + err.Error())
		}
		dark, light := ParseCSS(string(raw))
		contractDark, contractLight = merge(dark, extraDark), merge(light, extraLight)
	}
	return contractDark, contractLight
}

func merge(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func checkValue(name, value string) (string, error) {
	value = strings.Join(strings.Fields(value), " ")
	low := strings.ToLower(value)
	for _, bad := range forbidden {
		if strings.Contains(low, bad) {
			return "", ErrTheme{"theme.bad_value:" + name}
		}
	}
	if !safeValue.MatchString(value) {
		return "", ErrTheme{"theme.bad_value:" + name}
	}
	return value, nil
}

// CleanTokens keeps only tokens the contract knows, validated against
// injection (no url(), @import, backslashes, ...).
func CleanTokens(tokens map[string]any) (map[string]string, error) {
	known, _ := Contract()
	out := map[string]string{}
	for name, raw := range tokens {
		if _, ok := known[name]; !ok {
			continue
		}
		value, err := checkValue(name, fmt.Sprint(raw))
		if err != nil {
			return nil, err
		}
		out[name] = value
	}
	return out, nil
}

// Render turns one theme into its full stylesheet (dark root, light
// override, prefers-color-scheme fallback, then any custom CSS).
func Render(theme *model.Theme) string {
	baseDark, baseLight := Contract()
	dark := merge(baseDark, stringMap(theme.Dark))
	light := merge(baseLight, stringMap(theme.Light))

	var b strings.Builder
	writeBlock(&b, ":root", dark, "dark")
	b.WriteByte('\n')
	writeBlock(&b, `:root[data-theme="light"]`, light, "light")
	b.WriteString("\n@media (prefers-color-scheme: light){:root:not([data-theme]){")
	for _, k := range sortedKeys(light) {
		fmt.Fprintf(&b, "%s:%s;", k, light[k])
	}
	b.WriteString("color-scheme:light}}")
	if theme.CustomCSS != "" {
		b.WriteByte('\n')
		b.WriteString(theme.CustomCSS)
	}
	return b.String()
}

func writeBlock(b *strings.Builder, selector string, tokens map[string]string, scheme string) {
	b.WriteString(selector)
	b.WriteByte('{')
	for _, k := range sortedKeys(tokens) {
		fmt.Fprintf(b, "%s:%s;", k, tokens[k])
	}
	fmt.Fprintf(b, "color-scheme:%s}", scheme)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func stringMap(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = fmt.Sprint(v)
	}
	return out
}

// ── Contrast (WCAG) ──

func luminance(hexColorStr string) float64 {
	v := strings.TrimPrefix(hexColorStr, "#")
	if len(v) == 3 {
		v = string([]byte{v[0], v[0], v[1], v[1], v[2], v[2]})
	}
	var channels [3]float64
	for i := 0; i < 3; i++ {
		n, _ := strconv.ParseInt(v[i*2:i*2+2], 16, 32)
		c := float64(n) / 255
		if c <= 0.03928 {
			channels[i] = c / 12.92
		} else {
			channels[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*channels[0] + 0.7152*channels[1] + 0.0722*channels[2]
}

// Ratio computes the WCAG contrast ratio between two hex colors.
func Ratio(fg, bg string) float64 {
	a, b := luminance(fg), luminance(bg)
	if a < b {
		a, b = b, a
	}
	return round2((a + 0.05) / (b + 0.05))
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// ContrastIssues checks the text pairs the shrippen contract cares about
// and returns every pair failing WCAG AA (4.5:1) in either mode.
func ContrastIssues(dark, light map[string]string) []ContrastIssue {
	baseDark, baseLight := Contract()
	var issues []ContrastIssue
	modes := []struct {
		mode   Mode
		tokens map[string]string
	}{
		{ModeDark, merge(baseDark, dark)},
		{ModeLight, merge(merge(baseDark, baseLight), light)},
	}
	for _, m := range modes {
		for _, pair := range textPairs {
			a, b := m.tokens[pair[0]], m.tokens[pair[1]]
			if !hexColor.MatchString(a) || !hexColor.MatchString(b) {
				continue
			}
			if r := Ratio(a, b); r < aaText {
				issues = append(issues, ContrastIssue{Mode: m.mode, FG: pair[0], BG: pair[1], Ratio: r})
			}
		}
	}
	return issues
}

// ── Builtin ──

// EnsureBuiltin creates or refreshes the shipped shrippen theme from the
// embedded tokens.css, and returns its id.
func EnsureBuiltin(d *sql.DB) (int64, error) {
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		metaRaw, err := builtinFiles.ReadFile("builtin/shrippen/theme.json")
		if err != nil {
			return err
		}
		var meta struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(metaRaw, &meta); err != nil {
			return err
		}
		tokensRaw, err := builtinFiles.ReadFile("builtin/shrippen/tokens.css")
		if err != nil {
			return err
		}
		dark, light := ParseCSS(string(tokensRaw))
		digestInput, _ := json.Marshal([]map[string]string{dark, light})
		sum := sha256.Sum256(digestInput)
		digest := fmt.Sprintf("%x", sum)[:12]

		theme, err := misc.BuiltinTheme(tx, builtinSlug)
		if err != nil {
			return err
		}
		if theme == nil {
			theme = &model.Theme{Slug: builtinSlug, Name: meta.Name, Builtin: true, Version: 1}
			if err := misc.AddTheme(tx, theme); err != nil {
				return err
			}
		}
		if theme.Digest != digest {
			theme.Dark = anyMap(merge(dark, extraDark))
			theme.Light = anyMap(merge(light, extraLight))
			theme.Contract = contractVer
			theme.Digest = digest
			theme.Version++
			if err := misc.UpdateTheme(tx, theme); err != nil {
				return err
			}
		}
		id = theme.ID
		return nil
	})
	return id, err
}

func anyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ── Selection ──

// Active resolves the theme that applies: board forces > personal choice >
// team default > instance default > shrippen.
func Active(d *sql.DB, who *access.Principal, boardTheme *int64, spaceID *int64) (int64, error) {
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		var candidates []*int64
		candidates = append(candidates, boardTheme)
		if who != nil {
			u, err := users.Get(tx, who.UserID)
			if err != nil {
				return err
			}
			if u != nil {
				candidates = append(candidates, u.ThemeID)
			}
		}
		if spaceID != nil {
			space, err := content.Space(tx, *spaceID)
			if err != nil {
				return err
			}
			if space != nil && space.Kind == enums.SpaceTeam {
				if raw, ok := space.Settings["theme_id"]; ok {
					if n, ok := raw.(float64); ok {
						themeID := int64(n)
						candidates = append(candidates, &themeID)
					}
				}
			}
		}
		def, err := defaultThemeID(tx)
		if err != nil {
			return err
		}
		candidates = append(candidates, def)

		for _, c := range candidates {
			if c == nil || *c == 0 {
				continue
			}
			t, err := misc.Theme(tx, *c)
			if err != nil {
				return err
			}
			if t != nil {
				id = t.ID
				return nil
			}
		}
		builtin, err := misc.BuiltinTheme(tx, builtinSlug)
		if err != nil {
			return err
		}
		if builtin == nil {
			return errors.New("themes: shrippen not seeded")
		}
		id = builtin.ID
		return nil
	})
	return id, err
}

func defaultThemeID(q db.Queryer) (*int64, error) {
	setting, err := misc.Setting(q, defaultSetting)
	if err != nil {
		return nil, err
	}
	if raw, ok := setting["id"].(float64); ok {
		id := int64(raw)
		return &id, nil
	}
	return nil, nil
}

// Stylesheet returns a theme's rendered CSS and version (for cache
// busting); an unknown id falls back to shrippen.
func Stylesheet(d *sql.DB, themeID int64) (string, int, error) {
	var css string
	var version int
	err := db.WithTx(d, func(tx *sql.Tx) error {
		theme, err := misc.Theme(tx, themeID)
		if err != nil {
			return err
		}
		if theme == nil {
			theme, err = misc.BuiltinTheme(tx, builtinSlug)
			if err != nil {
				return err
			}
			if theme == nil {
				return errors.New("themes: shrippen not seeded")
			}
		}
		css, version = Render(theme), theme.Version
		return nil
	})
	return css, version, err
}

// ── Management ──

func right(q db.Queryer, who *access.Principal, theme *model.Theme) (enums.Right, error) {
	if theme.Builtin {
		return enums.RightUse, nil
	}
	space, err := access.SpaceOf(q, who, spaceIDOf(theme))
	if err != nil {
		return enums.RightNone, err
	}
	return access.Right(who, enums.ResourceTheme, theme.ID, space, nil), nil
}

func spaceIDOf(theme *model.Theme) int64 {
	if theme.SpaceID == nil {
		return 0
	}
	return *theme.SpaceID
}

// Listing returns the themes who may at least USE.
func Listing(d *sql.DB, who *access.Principal) ([]Ref, error) {
	var out []Ref
	err := db.WithTx(d, func(tx *sql.Tx) error {
		spaceIDs := make([]int64, 0, len(who.Spaces))
		for id := range who.Spaces {
			spaceIDs = append(spaceIDs, id)
		}
		all, err := misc.Themes(tx, spaceIDs)
		if err != nil {
			return err
		}
		for _, t := range all {
			granted, err := right(tx, who, t)
			if err != nil {
				return err
			}
			if granted < enums.RightUse {
				continue
			}
			out = append(out, Ref{ID: t.ID, Slug: t.Slug, Name: t.Name, Builtin: t.Builtin,
				SpaceID: t.SpaceID, CanEdit: granted >= enums.RightEdit})
		}
		return nil
	})
	return out, err
}

// Get returns a theme and the caller's right on it. Requires USE.
func Get(d *sql.DB, who *access.Principal, themeID int64) (*model.Theme, enums.Right, error) {
	var theme *model.Theme
	var granted enums.Right
	err := db.WithTx(d, func(tx *sql.Tx) error {
		t, err := misc.Theme(tx, themeID)
		if err != nil {
			return err
		}
		if t == nil {
			return ErrNotFound
		}
		g, err := right(tx, who, t)
		if err != nil {
			return err
		}
		if err := access.Need(g, enums.RightUse); err != nil {
			return err
		}
		theme, granted = t, g
		return nil
	})
	return theme, granted, err
}

// Duplicate copies a theme into a space the caller may edit.
func Duplicate(d *sql.DB, who *access.Principal, themeID, spaceID int64, name string) (int64, error) {
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		source, err := misc.Theme(tx, themeID)
		if err != nil {
			return err
		}
		if source == nil {
			return ErrNotFound
		}
		g, err := right(tx, who, source)
		if err != nil {
			return err
		}
		if err := access.Need(g, enums.RightUse); err != nil {
			return err
		}
		space, err := access.SpaceOf(tx, who, spaceID)
		if err != nil {
			return err
		}
		if err := access.Need(access.SpaceRight(who, space), enums.RightEdit); err != nil {
			return err
		}

		existing, err := misc.Themes(tx, []int64{spaceID})
		if err != nil {
			return err
		}
		taken := map[string]bool{}
		for _, t := range existing {
			if t.SpaceID != nil && *t.SpaceID == spaceID {
				taken[t.Slug] = true
			}
		}
		label := strings.TrimSpace(name)
		if label == "" {
			label = source.Name
		}
		customCSS := ""
		if who.IsAdmin() {
			customCSS = source.CustomCSS
		}
		sid := spaceID
		theme := &model.Theme{
			SpaceID: &sid, Slug: util.Unique(util.Slug(name, "theme"), taken), Name: label,
			Dark: copyAnyMap(source.Dark), Light: copyAnyMap(source.Light), CustomCSS: customCSS, Version: 1,
		}
		if err := misc.AddTheme(tx, theme); err != nil {
			return err
		}
		id = theme.ID
		return audit.Log(tx, &who.UserID, "theme.created", theme.Name, "", nil)
	})
	return id, err
}

func copyAnyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Update validates and stores new tokens/custom CSS for a non-builtin
// theme. Custom CSS may only be set by admins. Returns any WCAG AA issues
// the new tokens introduce (not a hard error: colors still get saved).
func Update(d *sql.DB, who *access.Principal, themeID int64, name string, dark, light map[string]any, customCSS *string) ([]ContrastIssue, error) {
	cleanDark, err := CleanTokens(dark)
	if err != nil {
		return nil, err
	}
	cleanLight, err := CleanTokens(light)
	if err != nil {
		return nil, err
	}

	err = db.WithTx(d, func(tx *sql.Tx) error {
		theme, err := misc.Theme(tx, themeID)
		if err != nil {
			return err
		}
		if theme == nil || theme.Builtin {
			return ErrDenied
		}
		g, err := right(tx, who, theme)
		if err != nil {
			return err
		}
		if err := access.Need(g, enums.RightEdit); err != nil {
			return err
		}

		if n := strings.TrimSpace(name); n != "" {
			theme.Name = n
		}
		theme.Dark = anyMap(cleanDark)
		theme.Light = anyMap(cleanLight)
		if customCSS != nil {
			if !who.IsAdmin() {
				return ErrDenied
			}
			if len(*customCSS) > maxCSS || strings.Contains(strings.ToLower(*customCSS), "@import") {
				return ErrTheme{"theme.css_invalid"}
			}
			theme.CustomCSS = *customCSS
		}
		theme.Version++
		return misc.UpdateTheme(tx, theme)
	})
	if err != nil {
		return nil, err
	}
	return ContrastIssues(cleanDark, cleanLight), nil
}

// Delete removes a non-builtin theme and its shares. Requires MANAGE.
func Delete(d *sql.DB, who *access.Principal, themeID int64) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		theme, err := misc.Theme(tx, themeID)
		if err != nil || theme == nil || theme.Builtin {
			return err
		}
		g, err := right(tx, who, theme)
		if err != nil {
			return err
		}
		if err := access.Need(g, enums.RightManage); err != nil {
			return err
		}
		if err := misc.DropShares(tx, enums.ResourceTheme, theme.ID); err != nil {
			return err
		}
		return misc.RemoveTheme(tx, theme.ID)
	})
}

// SetDefault sets the instance-wide default theme. Admin only.
func SetDefault(d *sql.DB, who *access.Principal, themeID int64) error {
	if !who.IsAdmin() {
		return ErrDenied
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		return misc.SetSetting(tx, defaultSetting, map[string]any{"id": themeID})
	})
}

// ── Import / export ──

// ExportZip packages a theme as a zip: theme.json, tokens.css, and
// custom.css if the theme has one.
func ExportZip(d *sql.DB, who *access.Principal, themeID int64) (string, []byte, error) {
	theme, _, err := Get(d, who, themeID)
	if err != nil {
		return "", nil, err
	}

	meta := map[string]any{"name": theme.Name, "slug": theme.Slug, "contract": theme.Contract, "modes": []string{"dark", "light"}}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", nil, err
	}

	var tokens strings.Builder
	tokens.WriteString(":root{\n")
	for _, k := range sortedKeys(stringMap(theme.Dark)) {
		fmt.Fprintf(&tokens, "  %s: %s;\n", k, theme.Dark[k])
	}
	tokens.WriteString("}\n:root[data-theme=\"light\"]{\n")
	for _, k := range sortedKeys(stringMap(theme.Light)) {
		fmt.Fprintf(&tokens, "  %s: %s;\n", k, theme.Light[k])
	}
	tokens.WriteString("}\n")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeZipFile(zw, "theme.json", metaJSON); err != nil {
		return "", nil, err
	}
	if err := writeZipFile(zw, "tokens.css", []byte(tokens.String())); err != nil {
		return "", nil, err
	}
	if theme.CustomCSS != "" {
		if err := writeZipFile(zw, "custom.css", []byte(theme.CustomCSS)); err != nil {
			return "", nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return "", nil, err
	}
	return theme.Slug + ".zip", buf.Bytes(), nil
}

func writeZipFile(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// ImportZip creates a new theme in spaceID from an exported zip.
func ImportZip(d *sql.DB, who *access.Principal, spaceID int64, blob []byte) (int64, error) {
	if len(blob) > maxZip {
		return 0, ErrTheme{"theme.zip_too_large"}
	}
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return 0, ErrTheme{"theme.zip_invalid"}
	}

	var meta struct {
		Name string `json:"name"`
	}
	metaRaw, err := readZipFile(zr, "theme.json")
	if err != nil {
		return 0, ErrTheme{"theme.zip_invalid"}
	}
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return 0, ErrTheme{"theme.zip_invalid"}
	}
	tokensRaw, err := readZipFile(zr, "tokens.css")
	if err != nil {
		return 0, ErrTheme{"theme.zip_invalid"}
	}
	dark, light := ParseCSS(string(tokensRaw))
	css := ""
	if raw, err := readZipFile(zr, "custom.css"); err == nil {
		css = string(raw)
	}
	if meta.Name == "" {
		meta.Name = "Theme"
	}

	builtinID, err := EnsureBuiltin(d)
	if err != nil {
		return 0, err
	}
	newID, err := Duplicate(d, who, builtinID, spaceID, meta.Name)
	if err != nil {
		return 0, err
	}

	darkAny, lightAny := anyMap(dark), anyMap(light)
	var cssPtr *string
	if who.IsAdmin() && css != "" {
		cssPtr = &css
	}
	if _, err := Update(d, who, newID, meta.Name, darkAny, lightAny, cssPtr); err != nil {
		return 0, err
	}
	return newID, nil
}

func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	f, err := zr.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
