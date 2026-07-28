package email

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Mail is rendered server-side, so the branding derivation the frontend does in
// `frontend/src/lib/theme.ts` has to exist a second time here. The two are
// deliberately kept to the same rules — same WCAG relative-luminance formula,
// same near-white / warm-dark foreground candidates, same step size when nudging
// a colour to the contrast floor — and brand_test.go pins the results so a change
// on one side shows up as a failing test rather than as mail that no longer looks
// like the app.
//
// Only the LIGHT half of that derivation is mirrored. The app ships a
// `prefers-color-scheme: dark` block; mail cannot (see shell.go), so the mail
// palette is one light-mode palette chosen to stay readable when a client
// force-inverts it. Every colour is nudged toward black rather than white for the
// same reason: a darker foreground survives inversion better than a lighter one.

// Seeds are the presentational branding seeds of one organization, mirroring the
// stored themesettings colours the mail shell can use. Every colour is a CSS hex
// string and "" means "not set" — the default Yivi value is then used, exactly as
// an unset token falls back to its :root default in the app.
type Seeds struct {
	PrimaryColor string
	TextColor    string
	SurfaceColor string
	BorderColor  string
	LinkColor    string
	FontFamily   string
}

// Brand is the resolved, mail-ready palette: every field is a concrete value that
// can be written straight into an inline style attribute. Derived from Seeds by
// resolveBrand, which guarantees the WCAG-AA pairs mail needs (body text, muted
// footer text and links on the card, the CTA label on the primary fill).
type Brand struct {
	// Page is the outer background the message card sits on.
	Page string
	// Card is the background of the message itself, and the background every
	// foreground colour below is contrast-checked against.
	Card string
	// Text is body text on Card; Muted is the de-emphasised footer text, still at
	// AA (the app's --yb-muted deliberately runs below AA for incidental captions,
	// which is not a trade mail should make — the footer is the only place a
	// recipient learns who sent this and how to reply).
	Text  string
	Muted string
	// Border is the card outline and the rules between sections (decorative, no
	// text sits on it).
	Border string
	// Link is inline link text on Card.
	Link string
	// Button is the CTA fill and ButtonText its label, contrast-checked against it.
	Button     string
	ButtonText string
	// FontFamily is a CSS font-family list for the whole message.
	FontFamily string
}

// Default palette values, mirrored from COLOR_FIELD_DEFAULTS in
// frontend/src/lib/theme.ts (themselves the index.css :root tokens). Mail from an
// org with no theme configured must look like the default Yivi wallet, not like a
// third palette.
const (
	defaultPrimary = "#484747"
	defaultText    = "#484747"
	defaultSurface = "#faf8f6"
	defaultBorder  = "#eae5e2"
	defaultLink    = "#1d4e89"
	// The card is the app's lightest surface tier; the page keeps the tinted
	// surface behind it so the card reads as a card.
	defaultCard = "#ffffff"
	// Mail clients ignore webfonts, so the stack is system fonts only.
	defaultFontFamily = "Helvetica, Arial, sans-serif"
)

// Foreground candidates for text on a themed fill: near-white or the app's
// warm-dark ink (LIGHT_FG / DARK_FG in theme.ts).
const (
	lightFG = "#ffffff"
	darkFG  = "#211f1f"
)

// aaContrast is the WCAG 2.2 AA ratio for normal-size text. The CTA label is
// ~15px semibold, which is still "normal" text, so this is the bar every
// foreground the shell writes must clear.
const aaContrast = 4.5

// Step size and iteration cap when nudging a colour toward the contrast floor
// (ADJUST_STEP / ADJUST_MAX_STEPS in theme.ts).
const (
	adjustStep     = 0.06
	adjustMaxSteps = 24
)

// How strongly a tenant surface seed tints the card, and how far the muted footer
// text fades toward the card before being pulled back to AA.
const (
	cardTint = 0.08
	mutedMix = 0.32
)

// fontFamilyPattern gates a stored font-family string before it is written into
// an inline style attribute: letters, digits, spaces, commas, quotes and hyphens
// only, so no value can carry style-injection punctuation (;{}()/*). Mirrors
// isSafeFontFamily in frontend/src/lib/theme.ts.
var fontFamilyPattern = regexp.MustCompile(`^[A-Za-z0-9 ,'"-]{1,120}$`)

// resolveBrand turns an org's stored seeds into the palette the mail shell
// writes. A missing or malformed seed falls back to the default value, and every
// foreground is nudged until it clears AA against the surface it sits on, so a
// tenant cannot configure unreadable mail.
func resolveBrand(s Seeds) Brand {
	// A tenant surface seed colours the page and only tints the card, so the AA
	// floors below are checked against a background that stays near-white — the
	// same reason the app keeps SURFACE_TINT subtle.
	page := seedOr(s.SurfaceColor, defaultSurface)
	card := defaultCard
	if isHex(s.SurfaceColor) {
		card = mix(defaultCard, s.SurfaceColor, cardTint)
	}

	text := adjustToContrast(seedOr(s.TextColor, defaultText), card)
	button := seedOr(s.PrimaryColor, defaultPrimary)

	font := defaultFontFamily
	if fontFamilyPattern.MatchString(s.FontFamily) {
		font = s.FontFamily
	}

	return Brand{
		Page:       page,
		Card:       card,
		Text:       text,
		Muted:      adjustToContrast(mix(text, card, mutedMix), card),
		Border:     seedOr(s.BorderColor, defaultBorder),
		Link:       adjustToContrast(seedOr(s.LinkColor, defaultLink), card),
		Button:     button,
		ButtonText: readableForegroundAA(button),
		FontFamily: font,
	}
}

// seedOr returns the seed when it is a usable hex colour, else the fallback.
func seedOr(seed, fallback string) string {
	if isHex(seed) {
		return strings.ToLower(seed)
	}
	return fallback
}

type rgb struct{ r, g, b float64 }

// hexPattern accepts the 6-digit "#rrggbb" form, the only format the theme store
// holds.
var hexPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func isHex(s string) bool { return hexPattern.MatchString(s) }

func parseHex(s string) (rgb, bool) {
	if !isHex(s) {
		return rgb{}, false
	}
	channel := func(from, to int) float64 {
		v, _ := strconv.ParseUint(s[from:to], 16, 8)
		return float64(v)
	}
	return rgb{r: channel(1, 3), g: channel(3, 5), b: channel(5, 7)}, true
}

func toHex(c rgb) string {
	channel := func(v float64) int {
		return int(math.Min(255, math.Max(0, math.Round(v))))
	}
	return fmt.Sprintf("#%02x%02x%02x", channel(c.r), channel(c.g), channel(c.b))
}

// relativeLuminance follows the WCAG definition (sRGB -> linear, weighted sum).
func relativeLuminance(c rgb) float64 {
	linear := func(v float64) float64 {
		s := v / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.r) + 0.7152*linear(c.g) + 0.0722*linear(c.b)
}

// contrastRatio returns the WCAG contrast ratio (1-21) between two hex colours,
// or 0 when either is malformed.
func contrastRatio(a, b string) float64 {
	ca, okA := parseHex(a)
	cb, okB := parseHex(b)
	if !okA || !okB {
		return 0
	}
	la, lb := relativeLuminance(ca), relativeLuminance(cb)
	lighter, darker := math.Max(la, lb), math.Min(la, lb)
	return (lighter + 0.05) / (darker + 0.05)
}

// readableForegroundAA picks the foreground that reads best on the given fill and
// GUARANTEES the AA floor: for a mid-tone fill neither near-white nor the warm
// ink may reach 4.5:1, so the dark candidate is pushed toward black until it
// clears AA, then the higher-contrast candidate wins. Mirrors
// readableForegroundAA in frontend/src/lib/theme.ts; used for the CTA label,
// whose fill is not gated at save time.
func readableForegroundAA(background string) string {
	dark := adjustToContrast(darkFG, background)
	if contrastRatio(dark, background) >= contrastRatio(lightFG, background) {
		return dark
	}
	return lightFG
}

// adjustToContrast nudges a colour toward black in small steps until it clears
// the AA floor against the background, or the step cap is hit (by which point it
// is near-black and clears AA against any light surface). The app's equivalent
// also has a lighten direction, for its dark-mode half; mail has no dark half.
func adjustToContrast(color, background string) string {
	current := color
	for i := 0; i < adjustMaxSteps; i++ {
		if contrastRatio(current, background) >= aaContrast {
			break
		}
		current = darken(current, adjustStep)
	}
	return current
}

func darken(hex string, amount float64) string {
	c, ok := parseHex(hex)
	if !ok {
		return hex
	}
	scale := 1 - amount
	return toHex(rgb{r: c.r * scale, g: c.g * scale, b: c.b * scale})
}

// mix blends base toward tint by fraction t (0 = base, 1 = tint). A malformed
// tint leaves the base unchanged.
func mix(base, tint string, t float64) string {
	a, okA := parseHex(base)
	b, okB := parseHex(tint)
	if !okA || !okB {
		return base
	}
	return toHex(rgb{
		r: a.r + (b.r-a.r)*t,
		g: a.g + (b.g-a.g)*t,
		b: a.b + (b.b-a.b)*t,
	})
}
