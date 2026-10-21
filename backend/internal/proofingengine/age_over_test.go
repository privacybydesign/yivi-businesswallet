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

// The age is answered only from a date of birth read off the chip, and only
// for a flow that asks one.
func TestAgeOverOf(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	chipDoc := func(source string) session.Session {
		return session.Session{Steps: session.StepEvidence{Document: &session.DocumentStepEvidence{
			Source: source, Parsed: session.ParsedMRZ{DateOfBirth: "2008-10-22"},
		}}}
	}
	fd := &flow.FlowDefinition{AgeOver: 18}
	if got := ageOverOf(chipDoc(session.DocumentSourceChip), fd, now); got == nil || got.Age != 18 || got.Over {
		t.Errorf("chip, a day short of 18 = %+v, want age 18, not over", got)
	}
	if got := ageOverOf(chipDoc(session.DocumentSourceChip), &flow.FlowDefinition{AgeOver: 16}, now); got == nil || !got.Over {
		t.Errorf("chip, over 16 = %+v, want over", got)
	}
	if got := ageOverOf(chipDoc(session.DocumentSourceMRZ), fd, now); got != nil {
		t.Errorf("MRZ only = %+v, want none", got)
	}
	if got := ageOverOf(chipDoc(session.DocumentSourceChip), &flow.FlowDefinition{}, now); got != nil {
		t.Errorf("no age asked = %+v, want none", got)
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
