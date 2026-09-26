package themes

// Dashy theme presets, rebuilt on the shrippen token contract. Dashy
// themes have one mode, so a preset fills dark and light alike. The Nord,
// Dracula, One Dark and Material palettes follow their published colors;
// the Dashy-only ones (Callisto, Oblivion, Cyberpunk, Vaporware) are
// close approximations.
//
//	Palette ──tokens()──► {--bg-void …, --blue-n …} ──Update──► new theme

import (
	"database/sql"
	"sort"
	"strings"

	"andon/internal/services/access"
)

// Shares of the background mixed into derived colors.
const (
	neutralShare = 0.25 // --*-n: quieter semantic colors
	hoverShare   = 0.15 // --blue-hover
)

// Palette is the minimal color set a preset defines.
type Palette struct {
	Void, Hard, Bg, Panel, Surface, Border string
	Fg0, Fg1, Fg2, Fg3, Accent             string
	Blue, Aqua, Green, Yellow, Orange, Red string
	Purple                                 string
}

var presets = map[string]Palette{
	"callisto": {"#070b17", "#0b1021", "#111830", "#16203c", "#1c2848", "#2c3a62",
		"#f2f6ff", "#dfe6f6", "#bdc8e2", "#9aa8c8", "#00ccb4",
		"#5ab8ff", "#00ccb4", "#7ee081", "#ffd166", "#ff9f5a", "#ff6b6b", "#c49bff"},
	"nord": {"#242933", "#2e3440", "#2e3440", "#3b4252", "#434c5e", "#4c566a",
		"#eceff4", "#e5e9f0", "#d8dee9", "#b4bccb", "#88c0d0",
		"#81a1c1", "#8fbcbb", "#a3be8c", "#ebcb8b", "#d08770", "#d57780", "#b48ead"},
	"dracula": {"#191a21", "#21222c", "#282a36", "#2d2f3d", "#343746", "#44475a",
		"#f8f8f2", "#f8f8f2", "#e2e2dc", "#b0b8d8", "#bd93f9",
		"#8be9fd", "#80ffea", "#50fa7b", "#f1fa8c", "#ffb86c", "#ff6e6e", "#ff79c6"},
	"one-dark": {"#1b1d23", "#21252b", "#282c34", "#2c313a", "#333842", "#3e4451",
		"#e6e9ef", "#d7dae0", "#c0c6d0", "#a2a9b6", "#61afef",
		"#61afef", "#56b6c2", "#98c379", "#e5c07b", "#d19a66", "#e67c85", "#c678dd"},
	"material-dark": {"#121212", "#181818", "#1e1e1e", "#242424", "#2c2c2c", "#3a3a3a",
		"#ffffff", "#e0e0e0", "#c9c9c9", "#a8a8a8", "#bb86fc",
		"#82b1ff", "#03dac6", "#69f0ae", "#ffd740", "#ffab40", "#f28b82", "#bb86fc"},
	"material": {"#eceff1", "#e0e4e7", "#f5f5f5", "#ffffff", "#eeeeee", "#cfd8dc",
		"#111111", "#212121", "#424242", "#595959", "#3f51b5",
		"#1e5bc6", "#00695c", "#2e7d32", "#7a5d00", "#b34700", "#c62828", "#6a1b9a"},
	"high-contrast-dark": {"#000000", "#000000", "#0a0a0a", "#111111", "#1a1a1a", "#777777",
		"#ffffff", "#ffffff", "#f0f0f0", "#d0d0d0", "#ffff00",
		"#5cc8ff", "#00ffd0", "#00ff5a", "#ffff00", "#ffa500", "#ff6b6b", "#ff7aff"},
	"high-contrast-light": {"#ffffff", "#f2f2f2", "#ffffff", "#ffffff", "#f2f2f2", "#555555",
		"#000000", "#000000", "#1a1a1a", "#333333", "#0000cc",
		"#0033cc", "#006666", "#006600", "#6b5000", "#a34400", "#b00000", "#6a00a8"},
	"oblivion": {"#1c1e22", "#212428", "#2b2e33", "#32363c", "#393d44", "#4a4f57",
		"#f2f2f2", "#e0e0e0", "#c9cbce", "#a9adb3", "#d6d6d6",
		"#8fb3de", "#7fc9c0", "#9ccc65", "#e6c75c", "#e6a15c", "#ef8a8a", "#b39ddb"},
	"cyberpunk": {"#0b0b16", "#10101f", "#14142b", "#1b1b3a", "#222248", "#3c2f6b",
		"#f8f8ff", "#e8e6ff", "#cdc8f7", "#aaa5de", "#f3e600",
		"#00f0ff", "#00ffc8", "#39ff14", "#f3e600", "#ff9e00", "#ff4d85", "#e040fb"},
	"vaporware": {"#1a0f2e", "#211338", "#2a1a45", "#321f52", "#3b2560", "#5a3d85",
		"#fff5ff", "#f7e6ff", "#e3cdf2", "#c9aee0", "#ff71ce",
		"#01cdfe", "#05ffa1", "#b9f18c", "#fffb96", "#ffb86c", "#ff7a98", "#c77dff"},
}

// Presets lists the preset names, sorted.
func Presets() []string {
	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// tokens expands a palette to the color tokens of the contract.
func (p Palette) tokens() map[string]string {
	out := map[string]string{
		"--bg-void": p.Void, "--bg-hard": p.Hard, "--bg0": p.Bg, "--bg-panel": p.Panel, "--bg1": p.Surface, "--bg2": p.Border,
		"--fg0": p.Fg0, "--fg1": p.Fg1, "--fg2": p.Fg2, "--fg3": p.Fg3, "--accent": p.Accent,
		"--blue": p.Blue, "--aqua": p.Aqua, "--green": p.Green, "--yellow": p.Yellow,
		"--orange": p.Orange, "--red": p.Red, "--purple": p.Purple,
		"--blue-hover": mixHex(p.Blue, p.Bg, hoverShare),
	}
	for _, name := range []string{"blue", "aqua", "green", "yellow", "orange", "red", "purple"} {
		out["--"+name+"-n"] = mixHex(out["--"+name], p.Bg, neutralShare)
	}
	return out
}

// Dashy customColors keys → palette fields.
var dashyColorKeys = map[string]func(*Palette, string){
	"primary":                 func(p *Palette, v string) { p.Accent, p.Blue = v, v },
	"background":              func(p *Palette, v string) { p.Void, p.Bg = v, v },
	"background-darker":       func(p *Palette, v string) { p.Hard = v },
	"item-group-background":   func(p *Palette, v string) { p.Surface = v },
	"item-background":         func(p *Palette, v string) { p.Panel = v },
	"item-text":               func(p *Palette, v string) { p.Fg1 = v },
	"item-group-heading-text": func(p *Palette, v string) { p.Fg0 = v },
	"outline-color":           func(p *Palette, v string) { p.Border = v },
}

// DashyPalette builds a palette from a Dashy theme name plus its
// customColors; unknown names start from Callisto, Dashy's default.
// Returns false when neither gives anything to use.
func DashyPalette(name string, colors map[string]string) (Palette, bool) {
	base, known := presets[strings.ToLower(name)]
	if !known {
		base = presets["callisto"]
	}
	used := known
	for key, value := range colors {
		apply, ok := dashyColorKeys[key]
		if !ok || !hexColor.MatchString(strings.TrimSpace(value)) {
			continue
		}
		apply(&base, strings.TrimSpace(value))
		used = true
	}
	return base, used
}

// FromPalette creates a theme in spaceID with p for both modes. Contrast
// issues are returned, not refused, like any theme edit.
func FromPalette(d *sql.DB, who *access.Principal, spaceID int64, name string, p Palette) (int64, []ContrastIssue, error) {
	builtinID, err := EnsureBuiltin(d)
	if err != nil {
		return 0, nil, err
	}
	id, err := Duplicate(d, who, builtinID, spaceID, name)
	if err != nil {
		return 0, nil, err
	}
	theme, _, err := Get(d, who, id)
	if err != nil {
		return 0, nil, err
	}
	dark, light := withColors(theme.Dark, p), withColors(theme.Light, p)
	issues, err := Update(d, who, id, name, dark, light, nil)
	return id, issues, err
}

// FromPreset creates a theme from a named preset.
func FromPreset(d *sql.DB, who *access.Principal, spaceID int64, preset string) (int64, []ContrastIssue, error) {
	p, ok := presets[preset]
	if !ok {
		return 0, nil, ErrNotFound
	}
	return FromPalette(d, who, spaceID, presetTitle(preset), p)
}

// presetTitle: "one-dark" → "One Dark".
func presetTitle(name string) string {
	words := strings.Split(name, "-")
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func withColors(base map[string]any, p Palette) map[string]any {
	out := copyAnyMap(base)
	for k, v := range p.tokens() {
		out[k] = v
	}
	return out
}
