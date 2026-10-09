package proofingengine

import (
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
)

// A Dutch personal number is kept, masked or dropped by the policy only when
// it is a BSN; one that fails the 11-proef is dropped whatever the policy,
// and another country's number is left alone.
func TestApplyBSNPolicy(t *testing.T) {
	const validBSN, invalidBSN = "123456782", "123456783"
	cases := []struct {
		name, state, in string
		policy          privacy.BSNPolicy
		want            string
	}{
		{"retrieve keeps a BSN", dutchIssuingState, validBSN, privacy.BSNPolicyRetrieve, validBSN},
		{"empty policy keeps a BSN", dutchIssuingState, validBSN, "", validBSN},
		{"mask masks a BSN", dutchIssuingState, validBSN, privacy.BSNPolicyMask, "****.**.782"},
		{"omit drops a BSN", dutchIssuingState, validBSN, privacy.BSNPolicyOmit, ""},
		{"retrieve drops a non-BSN", dutchIssuingState, invalidBSN, privacy.BSNPolicyRetrieve, ""},
		{"mask drops a non-BSN", dutchIssuingState, invalidBSN, privacy.BSNPolicyMask, ""},
		{"mask keeps an already masked BSN", dutchIssuingState, "****.**.782", privacy.BSNPolicyMask, "****.**.782"},
		{"another country is untouched", "D", invalidBSN, privacy.BSNPolicyRetrieve, invalidBSN},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := documentInfo{IssuingState: c.state, PersonalNumber: c.in}
			applyBSNPolicy(&doc, c.policy)
			if doc.PersonalNumber != c.want {
				t.Errorf("PersonalNumber = %q, want %q", doc.PersonalNumber, c.want)
			}
		})
	}
}
