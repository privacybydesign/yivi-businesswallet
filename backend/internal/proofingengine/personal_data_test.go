package proofingengine

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// personalDataFlow is DigiD onboarding: the Yivi app alone, accepting only the
// registered personal data, with the document photo and face in the ID scan.
var personalDataFlow = flow.FlowDefinition{
	ID: "flow", Version: 1, Name: "DigiD onboarding",
	Steps:          []flow.Step{flow.StepDocumentPhoto, flow.StepFaceVerification},
	RequiredChecks: []flow.Check{flow.CheckFaceMatch, flow.CheckFaceLiveness},
	SelfieLocation: flow.LocationBrowser, FaceProvider: flow.FaceProviderIDScan,
	Apps:                  []flow.App{flow.AppYivi},
	AcceptedDocumentTypes: []string{flow.DocumentKindPersonalData},
	RequestedAttributes:   []string{attrDocument},
}

// bothAppsChip offers both apps: the Idem app reads the identity card's chip,
// the Yivi app shares a document or the personal data.
var bothAppsChip = flow.FlowDefinition{
	ID: "flow", Version: 1, Name: "Chip or personal data",
	Steps:                 []flow.Step{flow.StepDocumentCapture, flow.StepNFCRead},
	RequiredChecks:        []flow.Check{flow.CheckNFCPassiveAuth},
	AcceptedDocumentTypes: []string{flow.DocumentKindIdentityCard, flow.DocumentKindPersonalData},
	RequestedAttributes:   []string{attrDocument},
}

func TestPersonalDataFlowValidation(t *testing.T) {
	for name, fd := range map[string]flow.FlowDefinition{"the Yivi app alone": personalDataFlow, "both apps": bothAppsChip} {
		if err := flow.Validate(fd); err != nil {
			t.Fatalf("%s: Validate = %v, want valid", name, err)
		}
	}
	if personalDataFlow.BrowserOnly() {
		t.Error("BrowserOnly() = true, want the Yivi app after the browser's steps")
	}
	idemOnly := personalDataFlow
	idemOnly.Apps, idemOnly.AcceptedDocumentTypes = []flow.App{flow.AppIdem}, nil
	noDocumentForIdem := personalDataFlow
	noDocumentForIdem.Apps = nil
	personalDataWithoutYivi := personalDataFlow
	personalDataWithoutYivi.Apps = []flow.App{flow.AppIdem}
	bothAppsBrowserOnly := personalDataFlow
	bothAppsBrowserOnly.Apps = []flow.App{flow.AppYivi, flow.AppIdem}
	bothAppsBrowserOnly.AcceptedDocumentTypes = []string{flow.DocumentKindIdentityCard, flow.DocumentKindPersonalData}
	chipPersonalDataOnly := bothAppsChip
	chipPersonalDataOnly.AcceptedDocumentTypes = []string{flow.DocumentKindPersonalData}
	repeated := personalDataFlow
	repeated.Apps = []flow.App{flow.AppIdem, flow.AppIdem}
	unknown := personalDataFlow
	unknown.Apps = []flow.App{"irma"}
	for name, fd := range map[string]flow.FlowDefinition{
		"the Idem app with no app step": idemOnly, "a repeated app": repeated, "an unknown app": unknown,
		"personal data alone with the Idem app": noDocumentForIdem, "personal data without the Yivi app": personalDataWithoutYivi,
		"personal data with both apps and no app step": bothAppsBrowserOnly,
		"personal data alone with both apps":           chipPersonalDataOnly,
	} {
		if err := flow.Validate(fd); err == nil {
			t.Errorf("%s: Validate passed", name)
		}
	}
}

// A flow locked to the Yivi app refuses an Idem or a browser session.
func TestYiviOnlyFlowRefusesOtherMethods(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	s := New(DefaultConfig(), newMemSessions(), singleFlow{fd: personalDataFlow}, nil, nil)
	if _, err := s.CreateSession(ctx, tenant, pp.SessionInput{FlowID: "flow", Method: pp.MethodYivi}); err != nil {
		t.Fatalf("CreateSession(yivi) = %v", err)
	}
	for _, method := range []pp.Method{pp.MethodIdem, pp.MethodBrowser} {
		var refused *pp.RejectedError
		if _, err := s.CreateSession(ctx, tenant, pp.SessionInput{FlowID: "flow", Method: method}); !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
			t.Errorf("CreateSession(%s) = %v, want 400", method, err)
		}
	}
}

// The photographed document is tied to the registered personal data by the
// date of birth and surname: another date rejects, another surname goes to a
// reviewer, and the document in the result is the photographed one.
func TestPersonalDataDisclosure(t *testing.T) {
	for name, tc := range map[string]personalDataCase{
		"same person":        {"Jansen", "10-03-1990", session.StatusApproved, ""},
		"prefix and accents": {"Jánsen", "1990-03-10", session.StatusApproved, ""},
		"another surname":    {"de Vries", "10-03-1990", session.StatusNeedsReview, ""},
		"another birth date": {"Jansen", "11-03-1990", session.StatusRejected, errCodeDocMismatch},
		"no birth date":      {"Jansen", "", session.StatusRejected, errCodeDocMismatch},
	} {
		t.Run(name, func(t *testing.T) { checkPersonalDataDisclosure(t, personalDataFlow, tc) })
	}
}

// A flow that always reviews sends the disclosure it would approve to a
// reviewer, and still rejects the one it would reject.
func TestPersonalDataDisclosureAlwaysReview(t *testing.T) {
	fd := personalDataFlow
	fd.AlwaysReview = true
	for name, tc := range map[string]personalDataCase{
		"same person":        {"Jansen", "10-03-1990", session.StatusNeedsReview, ""},
		"another birth date": {"Jansen", "11-03-1990", session.StatusRejected, errCodeDocMismatch},
	} {
		t.Run(name, func(t *testing.T) { checkPersonalDataDisclosure(t, fd, tc) })
	}
}

type personalDataCase struct {
	lastName, born string
	status         session.Status
	errorCode      string
}

// checkPersonalDataDisclosure photographs the passport, discloses tc's
// personal data in a session of fd, and checks the session's decision.
func checkPersonalDataDisclosure(t *testing.T, fd flow.FlowDefinition, tc personalDataCase) {
	t.Helper()
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	sess := session.Session{
		ID: "ps_brp", TenantID: "org", Token: "tok-brp", Status: session.StatusCreated,
		Method: session.MethodBiometricBoundLogin, Flow: "flow", FlowVersion: 1, ExpiresAt: time.Now().Add(time.Hour),
		RequestedAttributes: fd.RequestedAttributes,
	}
	s, store := documentPhotoServer(sess, fd)
	face := pp.FaceCheck{LivenessPassed: true, MatchScore: new(0.95), Selfie: &pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}}
	if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, pp.DocumentPhoto{
		Front: pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}, Check: authenticScan(), Face: &face,
	}); err != nil {
		t.Fatalf("SubmitDocumentPhoto = %v", err)
	}
	attributes := map[string]string{"firstName": "Anna", "lastName": tc.lastName}
	if tc.born != "" {
		attributes["dateOfBirth"] = tc.born
	}
	if _, err := s.SubmitReference(ctx, tenant, sess.ID, sess.Token, pp.Reference{Credential: "pbdf.gemeente.personalData", Attributes: attributes}); err != nil {
		t.Fatalf("SubmitReference = %v", err)
	}
	decided, _ := store.Get("org", sess.ID)
	if decided.Status != tc.status || decided.ErrorCode != tc.errorCode {
		t.Fatalf("status %q, error %q; want %q, %q", decided.Status, decided.ErrorCode, tc.status, tc.errorCode)
	}
	if tc.status == session.StatusRejected {
		return
	}
	doc, ok := decided.Result["document"].(documentInfo)
	if !ok || doc.Number != scannedNumber || doc.Kind != pp.DocumentKindPassport {
		t.Errorf("document %+v, want the photographed passport", decided.Result["document"])
	}
}

func TestSameSurname(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"de Vries", "DE<VRIES", true},
		{"Müller", "MULLER", true},
		{"van den Berg-Jansen", "VAN DEN BERG JANSEN", true},
		{"Jansen", "Janssen", false},
		{"", "", false},
	} {
		if got := sameSurname(tc.a, tc.b); got != tc.want {
			t.Errorf("sameSurname(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// Personal data accepted next to documents is no document: the photographed
// one must be among the documents, and personal data alone accepts any.
func TestAcceptsDocumentTypeIgnoresPersonalData(t *testing.T) {
	for _, tc := range []struct {
		accepted []string
		kind     string
		want     bool
	}{
		{[]string{flow.DocumentKindPersonalData}, flow.DocumentKindPassport, true},
		{[]string{flow.DocumentKindIdentityCard, flow.DocumentKindPersonalData}, flow.DocumentKindPassport, false},
		{[]string{flow.DocumentKindIdentityCard, flow.DocumentKindPersonalData}, flow.DocumentKindIdentityCard, true},
		{[]string{flow.DocumentKindPassport, flow.DocumentKindIdentityCard, flow.DocumentKindResidencePermit, flow.DocumentTypeDrivingLicence, flow.DocumentKindPersonalData}, "", true},
	} {
		fd := flow.FlowDefinition{AcceptedDocumentTypes: tc.accepted}
		if got := fd.AcceptsDocumentType("", tc.kind); got != tc.want {
			t.Errorf("AcceptsDocumentType(%v, %q) = %v, want %v", tc.accepted, tc.kind, got, tc.want)
		}
	}
}
