package proofing

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// maxAttributeReasonLength bounds a reason in each language; the person reads
// it under the item it explains.
const maxAttributeReasonLength = 300

// attributeOutcomeOnly is the whole list of a flow that collects no data.
const attributeOutcomeOnly = "outcome_only"

// withReasons checks the reasons a flow version gives for the data it collects
// and returns the version with them trimmed. The person reads a reason in the
// language of the hosted page, so every item the version lists needs one in
// each language offered: a language that is not supported, a reason past
// maxAttributeReasonLength, or a missing one is refused.
//
// A version that lists no items leaves the engine to release whatever its
// steps collect, which only the engine can say, so it is not checked for
// missing reasons. The flow editor always lists the items.
func withReasons(in proofingprovider.FlowSpec, offered FlowHosted) (proofingprovider.FlowSpec, error) {
	reasons := make(map[string]map[string]string, len(in.AttributeReasons))
	for attribute, byLocale := range in.AttributeReasons {
		tidy := make(map[string]string, len(byLocale))
		for tag, reason := range byLocale {
			if _, ok := email.ParseLocale(tag); !ok {
				return in, fmt.Errorf("%w: unsupported language %q for the reason of %s", ErrInvalidInput, tag, attribute)
			}
			reason = strings.TrimSpace(reason)
			if utf8.RuneCountInString(reason) > maxAttributeReasonLength {
				return in, fmt.Errorf("%w: the reason of %s in %s is longer than %d characters",
					ErrInvalidInput, attribute, tag, maxAttributeReasonLength)
			}
			if reason != "" {
				tidy[tag] = reason
			}
		}
		if len(tidy) > 0 {
			reasons[attribute] = tidy
		}
	}
	in.AttributeReasons = nil
	if len(reasons) > 0 {
		in.AttributeReasons = reasons
	}

	// The hosted page offers every supported language when it names none.
	locales := offered.Locales
	if len(locales) == 0 {
		locales = email.Locales()
	}
	for _, attribute := range in.RequestedAttributes {
		if attribute == attributeOutcomeOnly {
			continue
		}
		for _, locale := range locales {
			if reasons[attribute][string(locale)] == "" {
				return in, fmt.Errorf("%w: give %s a reason in %s", ErrInvalidInput, attribute, locale)
			}
		}
	}
	return in, nil
}
