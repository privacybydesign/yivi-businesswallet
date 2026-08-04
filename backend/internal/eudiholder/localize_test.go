package eudiholder

import (
	"testing"

	"github.com/privacybydesign/irmago/eudi/storage/db/models"
	"gorm.io/datatypes"
)

// loc builds the per-locale display value the models carry. An empty v is a NULL
// (locale-less) entry, mirroring how irmago stores a display row with no locale.
func loc(v string) datatypes.NullString {
	return datatypes.NullString{V: v, Valid: v != ""}
}

func TestPickLocaleName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		names []localeName
		lang  string
		want  string
	}{
		{
			name:  "no entries yields empty",
			names: nil,
			lang:  "nl",
			want:  "",
		},
		{
			name: "matches the requested base tag over english",
			names: []localeName{
				{name: "Approved supplier", locale: loc("en")},
				{name: "Goedgekeurde leverancier", locale: loc("nl")},
			},
			lang: "nl",
			want: "Goedgekeurde leverancier",
		},
		{
			name: "region tag matches base language",
			names: []localeName{
				{name: "Approved supplier", locale: loc("en-GB")},
				{name: "Goedgekeurde leverancier", locale: loc("nl-NL")},
			},
			lang: "nl",
			want: "Goedgekeurde leverancier",
		},
		{
			name: "falls back to english when the language is missing",
			names: []localeName{
				{name: "Approved supplier", locale: loc("en")},
				{name: "Fournisseur agréé", locale: loc("fr")},
			},
			lang: "nl",
			want: "Approved supplier",
		},
		{
			name: "empty lang keeps the english preference",
			names: []localeName{
				{name: "Goedgekeurde leverancier", locale: loc("nl")},
				{name: "Approved supplier", locale: loc("en")},
			},
			lang: "",
			want: "Approved supplier",
		},
		{
			name: "falls back to a locale-less entry when no english",
			names: []localeName{
				{name: "Plain", locale: loc("")},
				{name: "Fournisseur agréé", locale: loc("fr")},
			},
			lang: "nl",
			want: "Plain",
		},
		{
			name: "falls back to the first entry as a last resort",
			names: []localeName{
				{name: "Fournisseur agréé", locale: loc("fr")},
				{name: "Anbieter", locale: loc("de")},
			},
			lang: "nl",
			want: "Fournisseur agréé",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := pickLocaleName(tc.names, tc.lang); got != tc.want {
				t.Errorf("pickLocaleName(%v, %q) = %q, want %q", tc.names, tc.lang, got, tc.want)
			}
		})
	}
}

func TestCredentialDisplay(t *testing.T) {
	t.Parallel()

	t.Run("nil metadata yields empty name and logo", func(t *testing.T) {
		t.Parallel()
		name, logo := credentialDisplay(nil, "nl")
		if name != "" || logo != "" {
			t.Errorf("credentialDisplay(nil) = (%q, %q), want empty", name, logo)
		}
	})

	t.Run("resolves the localized name and its logo", func(t *testing.T) {
		t.Parallel()
		meta := &models.CredentialMetadata{Display: []models.CredentialDisplay{
			{Name: "Approved supplier", Locale: loc("en"), LogoURI: "data:image/png;base64,EN"},
			{Name: "Goedgekeurde leverancier", Locale: loc("nl"), LogoURI: "data:image/png;base64,NL"},
		}}
		name, logo := credentialDisplay(meta, "nl")
		if name != "Goedgekeurde leverancier" {
			t.Errorf("name = %q, want the Dutch title", name)
		}
		if logo != "data:image/png;base64,NL" {
			t.Errorf("logo = %q, want the Dutch entry's logo", logo)
		}
	})

	t.Run("takes a logo from another entry when the chosen one has none", func(t *testing.T) {
		t.Parallel()
		meta := &models.CredentialMetadata{Display: []models.CredentialDisplay{
			{Name: "Approved supplier", Locale: loc("en"), LogoURI: "data:image/png;base64,EN"},
			{Name: "Goedgekeurde leverancier", Locale: loc("nl")},
		}}
		name, logo := credentialDisplay(meta, "nl")
		if name != "Goedgekeurde leverancier" {
			t.Errorf("name = %q, want the Dutch title", name)
		}
		if logo != "data:image/png;base64,EN" {
			t.Errorf("logo = %q, want the fallback entry's logo", logo)
		}
	})

	t.Run("no display rows yields empty name and logo", func(t *testing.T) {
		t.Parallel()
		name, logo := credentialDisplay(&models.CredentialMetadata{}, "nl")
		if name != "" || logo != "" {
			t.Errorf("credentialDisplay(empty) = (%q, %q), want empty", name, logo)
		}
	})
}
