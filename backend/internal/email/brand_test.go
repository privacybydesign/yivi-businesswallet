package email

import (
	"testing"
)

// seedColours is the matrix every contrast assertion runs over: the defaults, the
// brand pinks/reds the design system uses, plus the mid-tones where neither
// near-white nor the warm ink clears AA on its own (the case a naive
// light-or-dark foreground pick gets wrong).
var seedColours = []string{
	"#484747", "#ba3354", "#1d4e89", "#00973a", "#eba73b", "#bd1919",
	"#808080", "#7f7f7f", "#767676", "#949494", "#a0a0a0", "#c0c0c0",
	"#ffffff", "#000000", "#faf8f6", "#0b1020", "#f5f2ef", "#3a3736",
}

func TestResolveBrandUnsetSeedsUsesDefaults(t *testing.T) {
	b := resolveBrand(Seeds{})

	if b.Page != defaultSurface {
		t.Errorf("page = %q, want the default surface %q", b.Page, defaultSurface)
	}
	if b.Card != defaultCard {
		t.Errorf("card = %q, want the default card %q", b.Card, defaultCard)
	}
	if b.Button != defaultPrimary {
		t.Errorf("button = %q, want the default primary %q", b.Button, defaultPrimary)
	}
	if b.Border != defaultBorder {
		t.Errorf("border = %q, want the default border %q", b.Border, defaultBorder)
	}
	if b.FontFamily != defaultFontFamily {
		t.Errorf("fontFamily = %q, want %q", b.FontFamily, defaultFontFamily)
	}
}

func TestResolveBrandIgnoresMalformedSeeds(t *testing.T) {
	for _, seed := range []string{"", "red", "#fff", "#12345g", "rgb(0,0,0)", "#ffffff;color:red"} {
		b := resolveBrand(Seeds{PrimaryColor: seed, BorderColor: seed, SurfaceColor: seed, LinkColor: seed})
		if b.Button != defaultPrimary || b.Border != defaultBorder || b.Page != defaultSurface {
			t.Errorf("seed %q leaked into the palette: %+v", seed, b)
		}
	}
}

// A tenant font family is written straight into a style attribute, so anything
// carrying CSS punctuation must fall back to the default stack.
func TestResolveBrandGatesFontFamily(t *testing.T) {
	if got := resolveBrand(Seeds{FontFamily: "Inter, Arial, sans-serif"}).FontFamily; got != "Inter, Arial, sans-serif" {
		t.Errorf("a plain font stack was rejected: %q", got)
	}
	for _, seed := range []string{
		"Inter;color:red",
		"Inter}body{display:none",
		"url(https://x/y)",
		"/*x*/Inter",
	} {
		if got := resolveBrand(Seeds{FontFamily: seed}).FontFamily; got != defaultFontFamily {
			t.Errorf("font family %q was accepted as %q", seed, got)
		}
	}
}

// Every foreground the shell writes has to clear WCAG 2.2 AA against the surface
// it sits on, for ANY seed a tenant can save: the theme settings form does not gate
// contrast for mail, so the derivation has to.
func TestResolveBrandForegroundsClearAA(t *testing.T) {
	for _, primary := range seedColours {
		for _, surface := range seedColours {
			for _, text := range seedColours {
				b := resolveBrand(Seeds{PrimaryColor: primary, SurfaceColor: surface, TextColor: text, LinkColor: text})
				pairs := map[string][2]string{
					"button label on button": {b.ButtonText, b.Button},
					"body text on card":      {b.Text, b.Card},
					"muted text on card":     {b.Muted, b.Card},
					"link on card":           {b.Link, b.Card},
				}
				for what, pair := range pairs {
					if ratio := contrastRatio(pair[0], pair[1]); ratio < aaContrast {
						t.Errorf("primary=%s surface=%s text=%s: %s is %.2f:1, want >= %.1f:1 (%s on %s)",
							primary, surface, text, what, ratio, aaContrast, pair[0], pair[1])
					}
				}
			}
		}
	}
}

// The colour maths is mirrored from frontend/src/lib/theme.ts. These pin the
// shared primitives so the two implementations cannot drift silently.
func TestContrastRatioMatchesWCAG(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"#ffffff", "#000000", 21},
		{"#ffffff", "#ffffff", 1},
		{"#000000", "#000000", 1},
		// The default link on the default card, as the app renders it.
		{"#1d4e89", "#ffffff", 8.39},
	}
	for _, tc := range tests {
		got := contrastRatio(tc.a, tc.b)
		if diff := got - tc.want; diff > 0.01 || diff < -0.01 {
			t.Errorf("contrastRatio(%s, %s) = %.4f, want %.2f", tc.a, tc.b, got, tc.want)
		}
	}
	if got := contrastRatio("nonsense", "#ffffff"); got != 0 {
		t.Errorf("contrastRatio with a malformed colour = %v, want 0", got)
	}
}

func TestReadableForegroundAAPrefersTheHigherContrastCandidate(t *testing.T) {
	if got := readableForegroundAA("#000000"); got != lightFG {
		t.Errorf("on black the foreground is %q, want %q", got, lightFG)
	}
	if got := readableForegroundAA("#ffffff"); got == lightFG {
		t.Errorf("on white the foreground is %q, want a dark value", got)
	}
	// A mid grey clears AA against neither candidate as shipped, so the dark one
	// gets pushed until it does.
	mid := "#808080"
	got := readableForegroundAA(mid)
	if contrastRatio(got, mid) < aaContrast {
		t.Errorf("on %s the foreground %q is only %.2f:1", mid, got, contrastRatio(got, mid))
	}
}
