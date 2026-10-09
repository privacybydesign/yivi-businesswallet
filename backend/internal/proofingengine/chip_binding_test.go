package proofingengine

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
)

// Only a photo inside DG2 counts as the chip's.
func TestPhotoFromChip(t *testing.T) {
	portrait := []byte("portrait-jpeg-bytes")
	dg2 := hex.EncodeToString(append(append([]byte{0x75, 0x82, 0x01, 0x00}, portrait...), 0x00))
	ev := &mrtdEvidenceRequest{DataGroups: map[string]string{dataGroupPortrait: dg2}}
	cases := []struct {
		name  string
		photo photoInfo
		ev    *mrtdEvidenceRequest
		want  bool
	}{
		{"the chip's portrait", photoInfo{ImageBase64: base64.StdEncoding.EncodeToString(portrait)}, ev, true},
		{"the portrait as a data URL", photoInfo{ImageBase64: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(portrait)}, ev, true},
		{"another face", photoInfo{ImageBase64: base64.StdEncoding.EncodeToString([]byte("someone-else"))}, ev, false},
		{"no photo", photoInfo{}, ev, false},
		{"not base64", photoInfo{ImageBase64: "%%%"}, ev, false},
		{"no DG2", photoInfo{ImageBase64: base64.StdEncoding.EncodeToString(portrait)}, &mrtdEvidenceRequest{DataGroups: map[string]string{}}, false},
		{"DG2 not hex", photoInfo{ImageBase64: base64.StdEncoding.EncodeToString(portrait)}, &mrtdEvidenceRequest{DataGroups: map[string]string{dataGroupPortrait: "zz"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := photoFromChip(&c.photo, c.ev); got != c.want {
				t.Errorf("photoFromChip = %v, want %v", got, c.want)
			}
		})
	}
}

// Only the session's own challenge counts for Active Authentication.
func TestAAChallengeMatches(t *testing.T) {
	const challenge = "a1b2c3d4e5f60718"
	cases := []struct {
		name, nonce, expected string
		want                  bool
	}{
		{"the session's challenge", challenge, challenge, true},
		{"the same bytes in upper case", "A1B2C3D4E5F60718", challenge, true},
		{"another nonce", "8899aabbccddeeff", challenge, false},
		{"a prefix of the challenge", "a1b2c3d4", challenge, false},
		{"no nonce", "", challenge, false},
		{"no challenge", challenge, "", false},
		{"nonce not hex", "zz", challenge, false},
		{"challenge not hex", challenge, "zz", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := aaChallengeMatches(c.nonce, c.expected); got != c.want {
				t.Errorf("aaChallengeMatches(%q, %q) = %v, want %v", c.nonce, c.expected, got, c.want)
			}
		})
	}
}

// Mask and omit drop a Dutch document's raw DG11, and nothing else.
func TestRedactBSNFromEvidence(t *testing.T) {
	const dg11 = "6b0a5f1001"
	cases := []struct {
		name   string
		doc    *documentInfo
		policy privacy.BSNPolicy
		want   bool
	}{
		{"mask drops DG11", &documentInfo{IssuingState: dutchIssuingState}, privacy.BSNPolicyMask, false},
		{"omit drops DG11", &documentInfo{IssuingState: dutchIssuingState}, privacy.BSNPolicyOmit, false},
		{"omit drops DG11 without a parsed BSN", &documentInfo{IssuingState: dutchIssuingState, PersonalNumber: ""}, privacy.BSNPolicyOmit, false},
		{"retrieve keeps DG11", &documentInfo{IssuingState: dutchIssuingState}, privacy.BSNPolicyRetrieve, true},
		{"an empty policy keeps DG11", &documentInfo{IssuingState: dutchIssuingState}, "", true},
		{"another country keeps DG11", &documentInfo{IssuingState: "D"}, privacy.BSNPolicyOmit, true},
		{"no document keeps DG11", nil, privacy.BSNPolicyOmit, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := &mrtdEvidenceRequest{DataGroups: map[string]string{dataGroupPersonalDetails: dg11, dataGroupPortrait: "00"}}
			redactBSNFromEvidence(ev, c.doc, c.policy)
			if _, ok := ev.DataGroups[dataGroupPersonalDetails]; ok != c.want {
				t.Errorf("DG11 kept = %v, want %v", ok, c.want)
			}
			if _, ok := ev.DataGroups[dataGroupPortrait]; !ok {
				t.Error("DG2 dropped, want only DG11 to go")
			}
		})
	}

	redactBSNFromEvidence(nil, &documentInfo{IssuingState: dutchIssuingState}, privacy.BSNPolicyOmit)
}
