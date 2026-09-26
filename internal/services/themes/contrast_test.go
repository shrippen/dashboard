package themes

import (
	"os"
	"regexp"
	"testing"
)

// Text colours sit on these surfaces; --bg-void as text sits on filled
// badges instead and is checked against those fills.
var (
	textColor    = regexp.MustCompile(`(?:^|[\s;{])color:\s*var\((--[a-z0-9-]+)\)`)
	badgeFills   = []string{"--blue", "--yellow", "--orange", "--red"}
	badgeText    = "--bg-void"
	dashboardCSS = "../../web/static/andon.css"
)

func resolved(tokens map[string]string, name string) string {
	v := tokens[name]
	for i := 0; i < 3 && len(v) > 6 && v[:4] == "var("; i++ {
		v = tokens[v[4:len(v)-1]]
	}
	return v
}

// TestComponentContrastAA: every text colour andon.css uses reaches
// WCAG AA on the text surfaces, in both modes of the shipped theme.
func TestComponentContrastAA(t *testing.T) {
	raw, err := os.ReadFile(dashboardCSS)
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, m := range textColor.FindAllStringSubmatch(string(raw), -1) {
		used[m[1]] = true
	}

	dark, light := Contract()
	dark = merge(dark, TextRoles(dark))
	light = merge(dark, light)
	light = merge(light, TextRoles(light))

	for mode, tokens := range map[Mode]map[string]string{ModeDark: dark, ModeLight: light} {
		for fg := range used {
			surfaces := textSurfaces
			if fg == badgeText {
				surfaces = badgeFills
			}
			for _, bg := range surfaces {
				a, b := resolved(tokens, fg), resolved(tokens, bg)
				if !hexColor.MatchString(a) || !hexColor.MatchString(b) {
					t.Errorf("%s: %s or %s not a colour", mode, fg, bg)
					continue
				}
				if r := Ratio(a, b); r < aaText {
					t.Errorf("%s: %s on %s = %.2f", mode, fg, bg, r)
				}
			}
		}
	}
}
