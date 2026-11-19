package proofingengine

import (
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// A Dutch personal number is kept, masked or dropped by the policy only when
// it is a BSN; one that fails the 11-proef is dropped whatever the policy,
// and another country's number is dropped whatever the policy.
func TestApplyBSNPolicy(t *testing.T) {
	const validBSN, invalidBSN = "123456782", "123456783"
	cases := []struct {
		name, state, in string
		policy          privacy.BSNPolicy
		want            string
	}{
		{"retrieve keeps a BSN", dutchIssuingState, validBSN, privacy.BSNPolicyRetrieve, validBSN},
		{"empty policy drops a BSN", dutchIssuingState, validBSN, "", ""},
		{"mask masks a BSN", dutchIssuingState, validBSN, privacy.BSNPolicyMask, "****.**.782"},
		{"omit drops a BSN", dutchIssuingState, validBSN, privacy.BSNPolicyOmit, ""},
		{"retrieve drops a non-BSN", dutchIssuingState, invalidBSN, privacy.BSNPolicyRetrieve, ""},
		{"mask drops a non-BSN", dutchIssuingState, invalidBSN, privacy.BSNPolicyMask, ""},
		{"mask keeps an already masked BSN", dutchIssuingState, "****.**.782", privacy.BSNPolicyMask, "****.**.782"},
		{"retrieve drops another country's number", "D", invalidBSN, privacy.BSNPolicyRetrieve, ""},
		{"omit drops another country's number", "ESP", "12345678Z", privacy.BSNPolicyOmit, ""},
		{"mask drops another country's number", "ESP", "12345678Z", privacy.BSNPolicyMask, ""},
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

// A chip read of another country's document releases its DG11 personal
// number under no policy: the result's personal number is a BSN.
func TestResultDropsForeignPersonalNumber(t *testing.T) {
	sess := session.Session{RequestedAttributes: []string{attrDocument, attrDG11}}
	for _, policy := range []privacy.BSNPolicy{privacy.BSNPolicyOmit, privacy.BSNPolicyMask, privacy.BSNPolicyRetrieve} {
		req := appResultRequest{Document: &documentInfo{IssuingState: "ESP", PersonalNumber: "12345678Z", PlaceOfBirth: "Madrid"}}
		doc, ok := buildResult(sess, req, nil, nil, policy, privacy.RedactionPolicy{})["document"].(documentInfo)
		if !ok {
			t.Fatalf("%s: no document released", policy)
		}
		if doc.PersonalNumber != "" {
			t.Errorf("%s: PersonalNumber = %q, want none", policy, doc.PersonalNumber)
		}
		if doc.PlaceOfBirth != "Madrid" {
			t.Errorf("%s: PlaceOfBirth = %q, want the DG11 extras kept", policy, doc.PlaceOfBirth)
		}
	}
}
