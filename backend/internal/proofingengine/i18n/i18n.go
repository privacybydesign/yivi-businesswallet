// Package i18n decides which language a proofing session's app view is in
// (Dutch and English are shipped): the session's own language, else the
// device's Accept-Language, else English. The Idem app renders its own copy.
package i18n

import (
	"slices"
	"strings"

	"golang.org/x/text/language"
)

// Default is the language used when neither the session nor the request
// names a shipped one.
const Default = "en"

// Supported lists the shipped languages, Default first.
var Supported = []string{"en", "nl"}

// Valid reports whether tag is well-formed BCP 47; an unshipped language is
// valid and resolves to another (Resolve).
func Valid(tag string) bool {
	_, err := language.Parse(tag)
	return err == nil
}

// Resolve picks the shipped language for a request: the session's own
// language (the relying party's choice at creation) when it names one, else
// the first shipped language in the Accept-Language header by weight, else
// Default. Regional variants match on their base language ("nl-BE" → "nl").
func Resolve(sessionLanguage, acceptLanguage string) string {
	if lang, ok := shipped(sessionLanguage); ok {
		return lang
	}
	tags, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err == nil {
		for _, tag := range tags { // already sorted by weight, q=0 dropped
			if lang, ok := shippedBase(tag); ok {
				return lang
			}
		}
	}
	return Default
}

func shipped(tag string) (string, bool) {
	if strings.TrimSpace(tag) == "" {
		return "", false
	}
	t, err := language.Parse(tag)
	if err != nil {
		return "", false
	}
	return shippedBase(t)
}

func shippedBase(t language.Tag) (string, bool) {
	base, _ := t.Base()
	lang := base.String()
	return lang, slices.Contains(Supported, lang)
}
