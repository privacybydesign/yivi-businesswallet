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

// photoAndScannedFace is a flow whose face ID Scan checks with the document
// photo, in the browser.
var photoAndScannedFace = flow.FlowDefinition{
	ID: "flow", Version: 1, Name: "Photo and face",
	Steps:          []flow.Step{flow.StepDocumentPhoto, flow.StepFaceVerification},
	RequiredChecks: []flow.Check{flow.CheckFaceMatch, flow.CheckFaceLiveness},
	SelfieLocation: flow.LocationBrowser, FaceProvider: flow.FaceProviderIDScan,
}

func scannedFaceSession() session.Session {
	sess := inProgress("ps_1")
	sess.Method = session.MethodBrowser
	sess.Flow, sess.FlowVersion, sess.ExpiresAt = "flow", 1, time.Now().UTC().Add(time.Hour)
	return sess
}

// ID Scan's face checks come with the document photo and are the face step:
// a live face that matches approves, one under the threshold or not live
// rejects.
func TestDocumentPhotoWithScannedFace(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	for name, tc := range map[string]struct {
		face      pp.FaceCheck
		status    session.Status
		errorCode string
	}{
		"live and matched": {pp.FaceCheck{LivenessPassed: true, MatchScore: new(0.95)}, session.StatusApproved, ""},
		"no match":         {pp.FaceCheck{LivenessPassed: true, MatchScore: new(0.5)}, session.StatusRejected, errCodeFaceNoMatch},
		"not live":         {pp.FaceCheck{MatchScore: new(0.95)}, session.StatusRejected, errCodeLivenessFailed},
	} {
		t.Run(name, func(t *testing.T) {
			face := tc.face
			face.Selfie = &pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}
			sess := scannedFaceSession()
			s, store := documentPhotoServer(sess, photoAndScannedFace)
			photo := pp.DocumentPhoto{Front: pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}, Check: authenticScan(), Face: &face}
			if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, photo); err != nil {
				t.Fatalf("SubmitDocumentPhoto = %v", err)
			}
			stored := checkDecided(t, store, sess.ID, tc.status, tc.errorCode)
			if ev := stored.Steps.Selfie; ev == nil || ev.Provider != faceProviderIDScan || ev.LivenessPassed != face.LivenessPassed {
				t.Errorf("face evidence %+v, want ID Scan's", ev)
			}
			// The verdicts show whatever the flow requested: this one asks
			// for none of the biometrics.
			checkScannedFaceEvidence(t, sessionIdentity(t, s, sess).Evidence, face)
		})
	}
}

// checkDecided checks that session id was decided as status with errorCode,
// and returns it.
func checkDecided(t *testing.T, store *memSessions, id string, status session.Status, errorCode string) session.Session {
	t.Helper()
	decided, _ := store.Get("org", id)
	if decided.Status != status || decided.ErrorCode != errorCode {
		t.Fatalf("status %q, error %q; want %q, %q", decided.Status, decided.ErrorCode, status, errorCode)
	}
	return decided
}

func sessionIdentity(t *testing.T, s *Server, sess session.Session) pp.Identity {
	t.Helper()
	identity, err := s.SessionIdentity(context.Background(), pp.Tenant{ID: sess.TenantID}, sess.ID, sess.Token)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

// checkScannedFaceEvidence checks that ev carries face's match score and
// liveness result.
func checkScannedFaceEvidence(t *testing.T, ev *pp.Evidence, face pp.FaceCheck) {
	t.Helper()
	if ev == nil || ev.FaceMatch == nil || *ev.FaceMatch != *face.MatchScore || ev.Liveness != livenessResults[face.LivenessPassed] {
		t.Errorf("evidence %+v, want the face match score and the liveness result", ev)
	}
}

// The face checks come with the photo exactly when the flow's face is ID
// Scan's.
func TestDocumentPhotoFaceOnlyForScannedFace(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	front := pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}
	var refused *pp.RejectedError

	sess := scannedFaceSession()
	s, _ := documentPhotoServer(sess, photoAndScannedFace)
	if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, pp.DocumentPhoto{Front: front}); !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
		t.Errorf("no face on an ID Scan face flow: %v, want 400", err)
	}

	sess = deviceSession("ps_2")
	s, _ = documentPhotoServer(sess, chipAndPhotoFlow)
	face := &pp.FaceCheck{LivenessPassed: true, MatchScore: new(0.95)}
	if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, pp.DocumentPhoto{Front: front, Face: face}); !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
		t.Errorf("a face on a flow without ID Scan's: %v, want 400", err)
	}
}

func TestScannedFaceFlowValidation(t *testing.T) {
	if err := flow.Validate(photoAndScannedFace); err != nil {
		t.Errorf("Validate = %v, want valid", err)
	}
	noPhoto := photoAndScannedFace
	noPhoto.Steps = []flow.Step{flow.StepFaceVerification}
	native := photoAndScannedFace
	native.SelfieLocation = flow.LocationNative
	for name, fd := range map[string]flow.FlowDefinition{"without the photo": noPhoto, "in the app": native} {
		if err := flow.Validate(fd); err == nil {
			t.Errorf("%s: Validate passed", name)
		}
	}
}

// A face ID Scan checks is matched against the photographed document: a
// session of such a flow takes no reference photo.
func TestScannedFaceSessionNeedsNoReferencePhoto(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	s := New(DefaultConfig(), newMemSessions(), singleFlow{fd: photoAndScannedFace}, nil, nil)
	if _, err := s.CreateSession(ctx, tenant, pp.SessionInput{FlowID: "flow", Method: pp.MethodBrowser}); err != nil {
		t.Fatalf("CreateSession = %v, want a session without a reference photo", err)
	}
	photo := &pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}
	var refused *pp.RejectedError
	if _, err := s.CreateSession(ctx, tenant, pp.SessionInput{FlowID: "flow", Method: pp.MethodBrowser, ReferencePhoto: photo}); !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
		t.Errorf("with a reference photo: %v, want 400", err)
	}
}

// A Yivi disclosure decides a session whose face ID Scan checked with the
// document photo: the face must have verified the person, as on the other
// paths, and its selfie and verdicts are in the result.
func TestYiviDisclosureWithScannedFace(t *testing.T) {
	for name, tc := range map[string]struct {
		face      pp.FaceCheck
		status    session.Status
		errorCode string
	}{
		"live and matched": {pp.FaceCheck{LivenessPassed: true, MatchScore: new(0.95)}, session.StatusApproved, ""},
		"no match":         {pp.FaceCheck{LivenessPassed: true, MatchScore: new(0.5)}, session.StatusRejected, errCodeFaceNoMatch},
		"not live":         {pp.FaceCheck{MatchScore: new(0.95)}, session.StatusRejected, errCodeLivenessFailed},
	} {
		t.Run(name, func(t *testing.T) {
			s, store, sess, disclosure := discloseWithScannedFace(t, yiviScannedFaceFlow(true), tc.face)
			checkDecided(t, store, sess.ID, tc.status, tc.errorCode)
			if tc.status == session.StatusRejected {
				if disclosure.OK || disclosure.Code != disclosureCodeFaceNotVerified {
					t.Errorf("disclosure = %+v, want refused with %s", disclosure, disclosureCodeFaceNotVerified)
				}
				return
			}
			identity := sessionIdentity(t, s, sess)
			if identity.Selfie == nil {
				t.Error("no selfie in the result, want ID Scan's live face")
			}
			checkScannedFaceEvidence(t, identity.Evidence, tc.face)
		})
	}
}

// A Yivi decision on a flow that keeps no face photos drops ID Scan's live
// still from the stored session, approved or rejected, and keeps the verdicts.
func TestYiviDisclosureDropsScannedSelfie(t *testing.T) {
	for name, face := range map[string]pp.FaceCheck{
		"approved": {LivenessPassed: true, MatchScore: new(0.95)},
		"rejected": {LivenessPassed: true, MatchScore: new(0.5)},
	} {
		t.Run(name, func(t *testing.T) {
			_, store, sess, _ := discloseWithScannedFace(t, yiviScannedFaceFlow(false), face)
			decided, _ := store.Get("org", sess.ID)
			ev := decided.Steps.Selfie
			if ev == nil || ev.Image != "" || ev.MimeType != "" {
				t.Fatalf("stored face evidence %+v, want no image", ev)
			}
			if ev.LivenessPassed != face.LivenessPassed || ev.FaceMatchScore == nil {
				t.Errorf("stored face evidence %+v, want the verdicts kept", ev)
			}
		})
	}
}

// yiviScannedFaceFlow is a flow decided by a Yivi disclosure whose face ID
// Scan checks with the document photo.
func yiviScannedFaceFlow(keepFacePhotos bool) flow.FlowDefinition {
	return flow.FlowDefinition{
		ID: "flow", Version: 1, Name: "Chip, photo and face",
		Steps:          []flow.Step{flow.StepDocumentCapture, flow.StepDocumentPhoto, flow.StepNFCRead, flow.StepFaceVerification},
		RequiredChecks: []flow.Check{flow.CheckFaceMatch, flow.CheckFaceLiveness},
		SelfieLocation: flow.LocationBrowser, FaceProvider: flow.FaceProviderIDScan,
		RequestedAttributes: []string{attrDocument, attrSelfie, attrBiometrics, attrDocumentImage},
		KeepFacePhotos:      &keepFacePhotos,
	}
}

// discloseWithScannedFace submits the document photo with ID Scan's face
// checks face on a Yivi-method session of fd, then its disclosure.
func discloseWithScannedFace(t *testing.T, fd flow.FlowDefinition, face pp.FaceCheck) (*Server, *memSessions, session.Session, pp.YiviDisclosure) {
	t.Helper()
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	sess := session.Session{
		ID: "ps_yivi", TenantID: "org", Token: "tok-yivi", Status: session.StatusCreated,
		Method: session.MethodBiometricBoundLogin, Flow: "flow", FlowVersion: 1, ExpiresAt: time.Now().Add(time.Hour),
		RequestedAttributes: fd.RequestedAttributes,
	}
	s, store := documentPhotoServer(sess, fd)
	face.Selfie = &pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}
	if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, pp.DocumentPhoto{
		Front: pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}, Check: authenticScan(), Face: &face,
	}); err != nil {
		t.Fatalf("SubmitDocumentPhoto = %v", err)
	}
	ref := pp.Reference{Credential: "pbdf-staging.pbdf.passport", Attributes: map[string]string{
		"firstName": "Anna", "lastName": "Jansen", "country": "NLD",
	}}
	disclosure, err := s.SubmitReference(ctx, tenant, sess.ID, sess.Token, ref)
	if err != nil {
		t.Fatalf("SubmitReference = %v", err)
	}
	return s, store, sess, disclosure
}
