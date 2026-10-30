package proofingengine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

var chipAndPhotoFlow = flow.FlowDefinition{
	ID: "flow", Version: 1, Name: "Chip and photo",
	Steps: []flow.Step{flow.StepDocumentCapture, flow.StepNFCRead, flow.StepDocumentPhoto},
}

func documentPhotoServer(sess session.Session, fd flow.FlowDefinition) (*Server, *memSessions) {
	store := newMemSessions(sess)
	return New(DefaultConfig(), store, singleFlow{fd: fd}, nil, nil), store
}

// The browser's document photo is stored for a flow that takes one, once:
// a repeat keeps the first. A flow without the step, or a finished session,
// refuses it.
func TestSubmitDocumentPhoto(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	photo := checkerboardPNG(t)
	first := pp.DocumentPhoto{Front: pp.Image{Base64: photo, MimeType: "image/png"}, BSNRedacted: true}

	sess := deviceSession("ps_1")
	s, store := documentPhotoServer(sess, chipAndPhotoFlow)
	if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, first); err != nil {
		t.Fatalf("SubmitDocumentPhoto = %v", err)
	}
	again := pp.DocumentPhoto{Front: pp.Image{Base64: photo, MimeType: "image/png"}, Back: &pp.Image{Base64: photo, MimeType: "image/png"}}
	if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, again); err != nil {
		t.Fatalf("repeat SubmitDocumentPhoto = %v, want nil", err)
	}
	stored, _ := store.Get("org", sess.ID)
	if p := stored.Steps.DocumentPhoto; p == nil || !p.Front.BSNRedacted || p.Back != nil {
		t.Errorf("stored %+v, want the first photo, BSN redacted", p)
	}

	noPhoto := chipAndPhotoFlow
	noPhoto.Steps = []flow.Step{flow.StepDocumentCapture, flow.StepNFCRead}
	s, _ = documentPhotoServer(sess, noPhoto)
	var refused *pp.RejectedError
	if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, first); !errors.As(err, &refused) || refused.Status != http.StatusConflict {
		t.Errorf("flow without document_photo: %v, want 409", err)
	}

	done := deviceSession("ps_2")
	done.Status = session.StatusApproved
	s, _ = documentPhotoServer(done, chipAndPhotoFlow)
	if err := s.SubmitDocumentPhoto(ctx, tenant, done.ID, done.Token, first); !errors.As(err, &refused) || refused.Status != http.StatusConflict {
		t.Errorf("finished session: %v, want 409", err)
	}
}

// A photo the scan service redacted itself is released and stored as taken
// under any BSN policy: it has no region to cover, and needs none.
func TestRedactedDocumentPhotoKept(t *testing.T) {
	photo := checkerboardPNG(t)
	policy := privacy.RedactionPolicy{BlurBSN: true}
	if got := releasedDocumentImage(session.Session{}, &documentImageInfo{ImageBase64: photo, MimeType: "image/png", BSNRedacted: true}, policy); got == nil || got.ImageBase64 != photo {
		t.Error("withheld or changed a redacted photo under BlurBSN, want it released as taken")
	}
	sess := session.Session{Steps: session.StepEvidence{DocumentPhoto: &session.DocumentPhotoStepEvidence{
		Front: session.DocumentPhotoSide{Image: photo, MimeType: "image/png", BSNRedacted: true},
	}}}
	redactStepsForStorage(&sess, privacy.BSNPolicyOmit, policy)
	if stored := sess.Steps.DocumentPhoto.Front.Image; stored != photo {
		t.Error("dropped or changed a redacted photo in storage, want it kept as taken")
	}
}

// The Idem app never sees the document photo: it is not among the view's
// steps, never its current step, and does not keep the app's QR on offer.
func TestAppViewLeavesDocumentPhotoToBrowser(t *testing.T) {
	sess := deviceSession("ps_1")
	sess.Steps.Document = &session.DocumentStepEvidence{}
	sess.Steps.NFC = &session.NFCStepEvidence{}
	s, _ := documentPhotoServer(sess, chipAndPhotoFlow)
	mux := http.NewServeMux()
	s.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, appRequest(http.MethodGet, "/app/"+sess.Token, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /app = %d %s", rec.Code, rec.Body.String())
	}
	var view struct {
		Steps         []flow.Step        `json:"steps"`
		CurrentStep   *string            `json:"currentStep"`
		NativeHandoff *nativeHandoffInfo `json:"nativeHandoff"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(view.Steps, flow.StepDocumentPhoto) {
		t.Errorf("steps = %v, want no document_photo", view.Steps)
	}
	if view.CurrentStep == nil || *view.CurrentStep != "" {
		t.Errorf("currentStep = %v, want \"\"", view.CurrentStep)
	}
	if view.NativeHandoff != nil {
		t.Error("offers the Idem app's QR for the browser's document photo")
	}
}

// The app's submit before the browser's document photo came in is refused
// with its own code, not decided.
func TestSubmitWaitsForDocumentPhoto(t *testing.T) {
	sess := deviceSession("ps_1")
	sess.Steps.Document = &session.DocumentStepEvidence{}
	sess.Steps.NFC = &session.NFCStepEvidence{}
	s, _ := documentPhotoServer(sess, chipAndPhotoFlow)
	mux := http.NewServeMux()
	s.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, appRequest(http.MethodPost, "/app/"+sess.Token+"/submit", ""))
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusConflict || body.Code != pp.CodeDocumentPhotoMissing {
		t.Errorf("submit = %d %s, want 409 %s", rec.Code, rec.Body.String(), pp.CodeDocumentPhotoMissing)
	}
}

// A Yivi disclosure on a flow with a document photo waits for the browser's
// photo, then releases it in the result.
func TestYiviDisclosureWithDocumentPhoto(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	sess := session.Session{
		ID: "ps_yivi", TenantID: "org", Token: "tok-yivi", Status: session.StatusCreated,
		Method: session.MethodBiometricBoundLogin, Flow: "flow", FlowVersion: 1, ExpiresAt: time.Now().Add(time.Hour),
	}
	fd := chipAndPhotoFlow
	s, store := documentPhotoServer(sess, fd)
	ref := pp.Reference{Credential: "pbdf-staging.pbdf.passport", Attributes: map[string]string{
		"firstName": "Anna", "lastName": "Jansen", "country": "NLD",
	}}

	_, err := s.SubmitReference(ctx, tenant, sess.ID, sess.Token, ref)
	var refused *pp.RejectedError
	if !errors.As(err, &refused) || refused.Code != pp.CodeDocumentPhotoMissing {
		t.Fatalf("disclosure before the photo = %v, want %s", err, pp.CodeDocumentPhotoMissing)
	}
	if current, _ := store.Get("org", sess.ID); current.Status.Terminal() {
		t.Fatalf("decided the session (%s) without the document photo", current.Status)
	}

	photo := checkerboardPNG(t)
	if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, pp.DocumentPhoto{
		Front: pp.Image{Base64: photo, MimeType: "image/png"}, BSNRedacted: true, Check: authenticScan(),
	}); err != nil {
		t.Fatalf("SubmitDocumentPhoto = %v", err)
	}
	disclosure, err := s.SubmitReference(ctx, tenant, sess.ID, sess.Token, ref)
	if err != nil || !disclosure.OK {
		t.Fatalf("disclosure with the photo = %+v, %v, want OK", disclosure, err)
	}
	decided, _ := store.Get("org", sess.ID)
	if decided.Status != session.StatusApproved || decided.Result["documentImage"] == nil {
		t.Errorf("status %s, documentImage %v, want approved with the photo", decided.Status, decided.Result["documentImage"])
	}

	// A document its scan service judged fake rejects the disclosure.
	sess.ID, sess.Token = "ps_fake", "tok-fake"
	s, store = documentPhotoServer(sess, fd)
	if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, pp.DocumentPhoto{
		Front: pp.Image{Base64: photo, MimeType: "image/png"}, Check: pp.DocumentCheck{Verdict: pp.DocumentFake},
	}); err != nil {
		t.Fatalf("SubmitDocumentPhoto = %v", err)
	}
	if disclosure, err := s.SubmitReference(ctx, tenant, sess.ID, sess.Token, ref); err != nil || disclosure.OK {
		t.Fatalf("disclosure with a fake document = %+v, %v, want refused", disclosure, err)
	}
	if rejected, _ := store.Get("org", sess.ID); rejected.Status != session.StatusRejected || rejected.ErrorCode != errCodeDocNotAuthentic {
		t.Errorf("status %s, error %q; want rejected, %s", rejected.Status, rejected.ErrorCode, errCodeDocNotAuthentic)
	}
}

// scannedNumber is the document number the test scans read.
const scannedNumber = "XR1234567"

// authenticScan is a document scan that found a genuine, valid, unexpired
// passport.
func authenticScan() pp.DocumentCheck {
	return pp.DocumentCheck{
		Verdict: pp.DocumentAuthentic, Score: new(0.97), FieldsValid: new(true), ZonesMatch: new(true),
		Document: pp.ScannedDocument{
			Kind: pp.DocumentKindPassport, IssuingState: "NLD", Number: scannedNumber, Surname: "Jansen", GivenNames: "Anna",
			DateOfBirth: "1990-03-10", DateOfExpiry: time.Now().AddDate(1, 0, 0).Format(time.DateOnly),
		},
	}
}

// A flow of the document photo alone has no app to submit it: the scan
// decides the session. A fake document or a field that failed its check
// rejects it, as does an expired one (what it read is the session's
// document); a doubtful one, zones that read differently or no verdict send
// it to a person; an authentic one approves it, with the read document and
// the scan's check in the result.
func TestDocumentPhotoOnlyFlowDecidedByPhoto(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	photoOnly := flow.FlowDefinition{ID: "flow", Version: 1, Name: "Photo", Steps: []flow.Step{flow.StepDocumentPhoto}}
	for name, tc := range map[string]struct {
		scan      func(*pp.DocumentCheck)
		status    session.Status
		errorCode string
	}{
		"authentic":      {func(*pp.DocumentCheck) {}, session.StatusApproved, ""},
		"doubtful":       {func(c *pp.DocumentCheck) { c.Verdict = pp.DocumentDoubtful }, session.StatusNeedsReview, ""},
		"no verdict":     {func(c *pp.DocumentCheck) { *c = pp.DocumentCheck{} }, session.StatusNeedsReview, ""},
		"zones differ":   {func(c *pp.DocumentCheck) { c.ZonesMatch = new(false) }, session.StatusNeedsReview, ""},
		"fake":           {func(c *pp.DocumentCheck) { c.Verdict = pp.DocumentFake }, session.StatusRejected, errCodeDocNotAuthentic},
		"invalid fields": {func(c *pp.DocumentCheck) { c.FieldsValid = new(false) }, session.StatusRejected, errCodeDocFieldsInvalid},
		"expired":        {func(c *pp.DocumentCheck) { c.Document.DateOfExpiry = "2020-01-01" }, session.StatusRejected, errCodeDocExpired},
	} {
		t.Run(name, func(t *testing.T) {
			scan := authenticScan()
			tc.scan(&scan)
			photo := pp.DocumentPhoto{Front: pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}, BSNRedacted: true, Check: scan}
			sess := inProgress("ps_1")
			sess.Method = session.MethodBrowser
			sess.Flow, sess.FlowVersion, sess.ExpiresAt = "flow", 1, time.Now().UTC().Add(time.Hour)
			s, store := documentPhotoServer(sess, photoOnly)
			if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, photo); err != nil {
				t.Fatalf("SubmitDocumentPhoto = %v", err)
			}
			stored, _ := store.Get("org", sess.ID)
			if stored.Status != tc.status || stored.ErrorCode != tc.errorCode {
				t.Errorf("status %q, error %q; want %q, %q", stored.Status, stored.ErrorCode, tc.status, tc.errorCode)
			}
		})
	}

	// Approved, the result carries what the scan read and how it checked.
	sess := inProgress("ps_2")
	sess.Method, sess.RequestedAttributes = session.MethodBrowser, []string{attrDocument}
	sess.Flow, sess.FlowVersion, sess.ExpiresAt = "flow", 1, time.Now().UTC().Add(time.Hour)
	s, _ := documentPhotoServer(sess, photoOnly)
	if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, pp.DocumentPhoto{
		Front: pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}, Check: authenticScan(),
	}); err != nil {
		t.Fatalf("SubmitDocumentPhoto = %v", err)
	}
	identity, err := s.SessionIdentity(ctx, tenant, sess.ID, sess.Token)
	if err != nil {
		t.Fatal(err)
	}
	ev := identity.Evidence
	if identity.FamilyName != "Jansen" || identity.BirthDate != "1990-03-10" || ev == nil ||
		ev.DocumentVerdict != string(pp.DocumentAuthentic) || ev.DocumentScore == nil || *ev.DocumentScore != 0.97 ||
		ev.DocumentFields != pp.CheckValid || ev.DocumentZones != pp.CheckValid || ev.DocumentKind != pp.DocumentKindPassport {
		t.Errorf("identity = %+v, evidence %+v; want the scanned holder and an authentic, valid, matching passport", identity, ev)
	}

	// With app steps left, the photo alone decides nothing.
	sess = deviceSession("ps_3")
	s, store := documentPhotoServer(sess, chipAndPhotoFlow)
	if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, pp.DocumentPhoto{
		Front: pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}, Check: pp.DocumentCheck{Verdict: pp.DocumentFake},
	}); err != nil {
		t.Fatalf("SubmitDocumentPhoto = %v", err)
	}
	if stored, _ := store.Get("org", sess.ID); stored.Status.Terminal() {
		t.Errorf("status %q with app steps left, want undecided", stored.Status)
	}
}

// A Yivi disclosure of another document than the one scanned is refused.
func TestYiviDisclosureOfAnotherDocumentRefused(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	sess := session.Session{
		ID: "ps_yivi", TenantID: "org", Token: "tok-yivi", Status: session.StatusCreated,
		Method: session.MethodBiometricBoundLogin, Flow: "flow", FlowVersion: 1, ExpiresAt: time.Now().Add(time.Hour),
	}
	s, store := documentPhotoServer(sess, chipAndPhotoFlow)
	if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, pp.DocumentPhoto{
		Front: pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}, Check: authenticScan(),
	}); err != nil {
		t.Fatalf("SubmitDocumentPhoto = %v", err)
	}
	ref := pp.Reference{Credential: "pbdf-staging.pbdf.passport", Attributes: map[string]string{
		"firstName": "Anna", "lastName": "Jansen", "country": "NLD", "documentNumber": "NOT" + scannedNumber,
	}}
	if disclosure, err := s.SubmitReference(ctx, tenant, sess.ID, sess.Token, ref); err != nil || disclosure.OK {
		t.Fatalf("disclosure of another document = %+v, %v, want refused", disclosure, err)
	}
	if rejected, _ := store.Get("org", sess.ID); rejected.Status != session.StatusRejected || rejected.ErrorCode != errCodeDocMismatch {
		t.Errorf("status %s, error %q; want rejected, %s", rejected.Status, rejected.ErrorCode, errCodeDocMismatch)
	}
}

// A flow the browser runs alone is created with the browser method, and only
// such a flow: no app link is minted, and the session reports the browser.
func TestBrowserMethodOnlyForBrowserOnlyFlows(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	photoOnly := flow.FlowDefinition{ID: "flow", Version: 1, Name: "Photo", Steps: []flow.Step{flow.StepDocumentPhoto}}
	var refused *pp.RejectedError

	s := New(DefaultConfig(), newMemSessions(), singleFlow{fd: photoOnly}, nil, nil)
	for _, method := range []pp.Method{"", pp.MethodIdem, pp.MethodYivi} {
		if _, err := s.CreateSession(ctx, tenant, pp.SessionInput{FlowID: "flow", Method: method}); !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
			t.Errorf("method %q on a browser-only flow: %v, want 400", method, err)
		}
	}
	created, err := s.CreateSession(ctx, tenant, pp.SessionInput{FlowID: "flow", Method: pp.MethodBrowser})
	if err != nil {
		t.Fatalf("CreateSession(browser) = %v", err)
	}
	if created.Claim != nil {
		t.Error("a browser session got an app link, want none")
	}
	res, err := s.SessionStatus(ctx, tenant, created.ID, created.Token)
	if err != nil || res.Method != pp.MethodBrowser {
		t.Errorf("SessionStatus method = %q, %v; want %q", res.Method, err, pp.MethodBrowser)
	}

	s = New(DefaultConfig(), newMemSessions(), singleFlow{fd: chipAndPhotoFlow}, nil, nil)
	if _, err := s.CreateSession(ctx, tenant, pp.SessionInput{FlowID: "flow", Method: pp.MethodBrowser}); !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
		t.Errorf("browser method on a flow with app steps: %v, want 400", err)
	}
}

// An age-check flow run in the browser answers the age from the date of
// birth the scan read, and keeps the answer, never the date.
func TestDocumentScanAnswersAgeCheck(t *testing.T) {
	ctx, tenant := context.Background(), pp.Tenant{ID: "org"}
	const age = 18
	ageCheck := flow.FlowDefinition{ID: "flow", Version: 1, Name: "Age", Steps: []flow.Step{flow.StepDocumentPhoto}, AgeOver: age}
	sess := inProgress("ps_1")
	sess.Method, sess.RequestedAttributes = session.MethodBrowser, []string{attrAgeOver}
	sess.Flow, sess.FlowVersion, sess.ExpiresAt = "flow", 1, time.Now().UTC().Add(time.Hour)
	s, store := documentPhotoServer(sess, ageCheck)
	if err := s.SubmitDocumentPhoto(ctx, tenant, sess.ID, sess.Token, pp.DocumentPhoto{
		Front: pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}, Check: authenticScan(),
	}); err != nil {
		t.Fatalf("SubmitDocumentPhoto = %v", err)
	}
	stored, _ := store.Get("org", sess.ID)
	if p := stored.Steps.DocumentPhoto; p == nil || p.Check == nil || p.Check.Document.DateOfBirth != "" {
		t.Error("kept the scanned date of birth on an age check")
	}
	identity, err := s.SessionIdentity(ctx, tenant, sess.ID, sess.Token)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Status != pp.StatusApproved || identity.AgeOver == nil || identity.AgeOver.Age != age || !identity.AgeOver.Over || identity.BirthDate != "" {
		t.Errorf("identity = %+v, age %+v; want approved, %d or older, no date of birth", identity.Result, identity.AgeOver, age)
	}

	// A scan that read no date of birth leaves the age unanswered: refused.
	unread := inProgress("ps_2")
	unread.Method, unread.RequestedAttributes = session.MethodBrowser, []string{attrAgeOver}
	unread.Flow, unread.FlowVersion, unread.ExpiresAt = "flow", 1, time.Now().UTC().Add(time.Hour)
	scan := authenticScan()
	scan.Document.DateOfBirth = ""
	s, store = documentPhotoServer(unread, ageCheck)
	if err := s.SubmitDocumentPhoto(ctx, tenant, unread.ID, unread.Token, pp.DocumentPhoto{
		Front: pp.Image{Base64: checkerboardPNG(t), MimeType: "image/png"}, Check: scan,
	}); err != nil {
		t.Fatalf("SubmitDocumentPhoto = %v", err)
	}
	if stored, _ := store.Get("org", unread.ID); stored.Status != session.StatusRejected || stored.ErrorCode != errCodeAgeNotDisclosed {
		t.Errorf("no date of birth: status %q, error %q; want rejected, %s", stored.Status, stored.ErrorCode, errCodeAgeNotDisclosed)
	}
}
