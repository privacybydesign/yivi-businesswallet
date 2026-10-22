package proofing

import (
	"errors"
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

// ErrReasonsMissing is a hosted page that would offer a language the flow's
// active version gives an item it collects no reason in.
var ErrReasonsMissing = errors.New("proofing: the flow gives no reason in a language its hosted page offers")

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

	if attribute, locale, missing := missingReason(in, offeredLocales(offered)); missing {
		return in, fmt.Errorf("%w: give %s a reason in %s", ErrInvalidInput, attribute, locale)
	}
	return in, nil
}

// offeredLocales are the languages a hosted page offers: every supported one
// when it names none.
func offeredLocales(offered FlowHosted) []email.Locale {
	if len(offered.Locales) == 0 {
		return email.Locales()
	}
	return offered.Locales
}

// missingReason is the first item spec collects that has no reason in one of
// locales, with that locale; missing is false when each item has one in each.
func missingReason(spec proofingprovider.FlowSpec, locales []email.Locale) (attribute string, locale email.Locale, missing bool) {
	for _, attribute := range spec.RequestedAttributes {
		if attribute == attributeOutcomeOnly {
			continue
		}
		for _, locale := range locales {
			if strings.TrimSpace(spec.AttributeReasons[attribute][string(locale)]) == "" {
				return attribute, locale, true
			}
		}
	}
	return "", "", false
}

// hostedOffersReasons is ErrReasonsMissing when the hosted page f would offer
// a language flow's active version gives an item no reason in. A version saved
// before reasons existed gives none at all and stays allowed (#329): its page
// shows no "Why" line in any language until the next edit adds them.
func hostedOffersReasons(flow proofingprovider.Flow, f FlowHosted) error {
	if len(flow.AttributeReasons) == 0 {
		return nil
	}
	if attribute, locale, missing := missingReason(flow.FlowSpec, offeredLocales(f)); missing {
		return fmt.Errorf("%w: the active version gives %s no reason in %s; add one to the flow before its page offers %s",
			ErrReasonsMissing, attribute, locale, locale)
	}
	return nil
}
