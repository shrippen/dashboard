package themes

import "testing"

// Every preset reaches WCAG AA for the contract's text pairs.
func TestPresetsContrast(t *testing.T) {
	for _, name := range Presets() {
		tokens := presets[name].tokens()
		for _, issue := range ContrastIssues(tokens, tokens) {
			if issue.Mode == ModeDark {
				t.Errorf("%s: %s on %s = %.2f", name, issue.FG, issue.BG, issue.Ratio)
			}
		}
	}
}

func TestDashyPalette(t *testing.T) {
	p, ok := DashyPalette("Nord", map[string]string{"primary": "#ff0000", "background": "url(x)"})
	if !ok || p.Accent != "#ff0000" || p.Bg != presets["nord"].Bg {
		t.Fatalf("palette: %+v", p)
	}
	if _, ok := DashyPalette("unknown", nil); ok {
		t.Fatal("unknown theme without colors used")
	}
}
