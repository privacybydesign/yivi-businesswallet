package proofingengine

import (
	"context"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

func TestReachedAge(t *testing.T) {
	cases := []struct {
		born, today string
		want        bool
	}{
		{"2008-10-21", "2026-10-20", false},
		{"2008-10-21", "2026-10-21", true},
		{"2008-10-21", "2027-01-01", true},
		{"2008-02-29", "2026-02-28", false},
		{"2008-02-29", "2026-03-01", true},
	}
	for _, tc := range cases {
		born, _ := time.Parse(time.DateOnly, tc.born)
		today, _ := time.Parse(time.DateOnly, tc.today)
		if got := reachedAge(born, 18, today.Add(23*time.Hour)); got != tc.want {
			t.Errorf("reachedAge(%s, 18, %s) = %v, want %v", tc.born, tc.today, got, tc.want)
		}
	}
}

// The age is answered only for a flow that asks one.
func TestAgeOverOf(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	doc := &documentInfo{DateOfBirth: "2008-10-22"}
	if got := ageOverOf(doc, &flow.FlowDefinition{AgeOver: 18}, now); got == nil || got.Age != 18 || got.Over {
		t.Errorf("a day short of 18 = %+v, want age 18, not over", got)
	}
	if got := ageOverOf(doc, &flow.FlowDefinition{AgeOver: 16}, now); got == nil || !got.Over {
		t.Errorf("over 16 = %+v, want over", got)
	}
	if got := ageOverOf(doc, &flow.FlowDefinition{}, now); got != nil {
		t.Errorf("no age asked = %+v, want none", got)
	}
}

// What the chip read stores keeps no date of birth: not the parsed date, nor
// the data groups carrying it. The portrait stays for the face match.
func TestWithholdBirthDate(t *testing.T) {
	req := submitNFCStepRequest{
		Document:     &documentInfo{FirstName: "Anna", DateOfBirth: "2008-10-22"},
		MrtdEvidence: &mrtdEvidenceRequest{DataGroups: map[string]string{dataGroupMRZ: "01", dataGroupPortrait: "02", dataGroupPersonalDetails: "0b"}},
	}
	withholdBirthDate(&req)
	if req.Document.DateOfBirth != "" || req.Document.FirstName != "Anna" {
		t.Errorf("document = %+v, want the name without the date of birth", req.Document)
	}
	if _, ok := req.MrtdEvidence.DataGroups[dataGroupMRZ]; ok {
		t.Error("DG1 kept")
	}
	if _, ok := req.MrtdEvidence.DataGroups[dataGroupPersonalDetails]; ok {
		t.Error("DG11 kept")
	}
	if _, ok := req.MrtdEvidence.DataGroups[dataGroupPortrait]; !ok {
		t.Error("DG2 dropped")
	}
}

// age_over releases the answer, never the document it comes from.
func TestResultReleasesAgeOverOnly(t *testing.T) {
	req := appResultRequest{Document: &documentInfo{DateOfBirth: "2008-10-22"}, AgeOver: &ageOverInfo{Age: 18, Over: true}}
	sess := session.Session{RequestedAttributes: []string{attrAgeOver}}
	result := buildResult(sess, req, nil, nil, privacy.BSNPolicyRetrieve, privacy.RedactionPolicy{})

	if _, ok := result["document"]; ok {
		t.Error("age_over released the document")
	}
	if got, ok := result["ageOver"].(*ageOverInfo); !ok || *got != (ageOverInfo{Age: 18, Over: true}) {
		t.Errorf("ageOver = %v, want age 18, over", result["ageOver"])
	}

	document := session.Session{RequestedAttributes: []string{attrDocument}}
	if _, ok := buildResult(document, req, nil, nil, privacy.BSNPolicyRetrieve, privacy.RedactionPolicy{})["ageOver"]; ok {
		t.Error("dg1 alone released ageOver")
	}
}

// Next to dg1 the age check wins: the name is released, the date of birth
// never.
func TestResultAgeOverWithholdsDateOfBirth(t *testing.T) {
	req := appResultRequest{
		Document: &documentInfo{FirstName: "Anna", DateOfBirth: "2008-10-22"},
		AgeOver:  &ageOverInfo{Age: 18, Over: true},
	}
	sess := session.Session{RequestedAttributes: []string{attrDocument, attrAgeOver}}
	result := buildResult(sess, req, nil, nil, privacy.BSNPolicyRetrieve, privacy.RedactionPolicy{})

	doc, ok := result["document"].(documentInfo)
	if !ok || doc.FirstName != "Anna" || doc.DateOfBirth != "" {
		t.Errorf("document = %+v, want the name without the date of birth", result["document"])
	}
	if _, ok := result["ageOver"]; !ok {
		t.Error("ageOver missing next to dg1")
	}
}

// A Yivi disclosure answers the age from its age claim, and keeps no date of
// birth even when the app sent one.
func TestDisclosedAgeOver(t *testing.T) {
	attributes := map[string]string{"firstName": "Anna", "over18": "Yes", "over21": "No", "dateOfBirth": "2006-10-22", "yearOfBirth": "2006"}
	if got := disclosedAgeOver(attributes, 18); got == nil || *got != (ageOverInfo{Age: 18, Over: true}) {
		t.Errorf("over18 = %+v, want age 18, over", got)
	}
	if got := disclosedAgeOver(attributes, 21); got == nil || got.Over {
		t.Errorf("over21 = %+v, want age 21, not over", got)
	}
	if got := disclosedAgeOver(attributes, 65); got != nil {
		t.Errorf("no over65 claim = %+v, want none", got)
	}

	info := disclosureInfo{Attributes: attributes, AttributeIDs: []string{"pbdf.pbdf.passport.firstName", "pbdf.pbdf.passport.dateOfBirth", "pbdf.pbdf.passport.yearOfBirth"}}
	withholdDisclosedBirthDate(&info)
	if _, ok := info.Attributes["dateOfBirth"]; ok || info.Attributes["yearOfBirth"] != "" || info.Attributes["firstName"] != "Anna" {
		t.Errorf("attributes = %v, want the name without the date of birth", info.Attributes)
	}
	if len(info.AttributeIDs) != 1 {
		t.Errorf("attribute ids = %v, want the name's only", info.AttributeIDs)
	}
}

func TestSessionIdentityAgeOver(t *testing.T) {
	sess := session.Session{
		ID: "age", TenantID: "org", Token: "tok-age", Status: session.StatusApproved,
		Steps:  session.StepEvidence{NFC: &session.NFCStepEvidence{}},
		Result: map[string]any{"assurance": map[string]any{}, "ageOver": &ageOverInfo{Age: 18, Over: true}},
	}
	s := &Server{sessions: newMemSessions(sess)}
	id, err := s.SessionIdentity(context.Background(), pp.Tenant{ID: "org"}, sess.ID, sess.Token)
	if err != nil || id.AgeOver == nil || *id.AgeOver != (pp.AgeOver{Age: 18, Over: true}) {
		t.Fatalf("identity = %+v, %v; want ageOver 18, over", id.AgeOver, err)
	}
	if id.BirthDate != "" || id.GivenName != "" {
		t.Errorf("identity carries %q %q, want no document data", id.GivenName, id.BirthDate)
	}
}
