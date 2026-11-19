package flow

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// minEditorCountries guards the pattern below: the ISO 3166-1 countries
// alone are 249.
const minEditorCountries = 249

// editorCountryCode matches a code of the editor's list: a key of its
// country table or an entry of its ICAO-only codes.
var editorCountryCode = regexp.MustCompile(`(?m)^\s+(?:([A-Z]{3}): "[A-Z]{2}"|"([A-Z]{3})"),$`)

// The flow editor lets an admin pick issuing states from a list of its own
// (frontend/src/lib/issuing-countries.ts). A code the engine would refuse
// makes a flow that cannot be saved, so every code on that list must pass
// ValidIssuingCountry.
func TestEditorCountriesAreValid(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "frontend", "src", "lib", "issuing-countries.ts")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the editor's country list: %v", err)
	}

	matches := editorCountryCode.FindAllStringSubmatch(string(source), -1)
	if len(matches) < minEditorCountries {
		t.Fatalf("found %d country codes in %s, want at least %d: has its layout changed?", len(matches), path, minEditorCountries)
	}
	for _, m := range matches {
		code := m[1] + m[2]
		if !ValidIssuingCountry(code) {
			t.Errorf("the editor offers %q, which the engine refuses", code)
		}
	}
}
