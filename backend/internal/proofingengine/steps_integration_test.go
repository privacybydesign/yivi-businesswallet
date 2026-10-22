//go:build integration

package proofingengine_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gmrtd/gmrtd/cms"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/mrtdverify"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/mrtdverify/mrtdtestfixtures"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// livenessRegula knows the liveness transactions in live (id → confirmed),
// all tagged tag, and matches every face with similarity 0.9.
type livenessRegula struct {
	fakeRegula
	tag  *string
	live map[string]bool
}

func (f livenessRegula) GetLiveness(_ context.Context, id string) (regula.LivenessTransaction, error) {
	confirmed, ok := f.live[id]
	if !ok {
		return regula.LivenessTransaction{}, regula.ErrTransactionNotFound
	}
	status := 1
	if confirmed {
		status = 0
	}
	return regula.LivenessTransaction{Status: &status, Tag: *f.tag}, nil
}

func (livenessRegula) Match(context.Context, string, string) (regula.MatchResult, error) {
	return regula.MatchResult{Similarity: 0.9}, nil
}

// claimApp claims sess's mailed link as the app does: its path token and
// device token.
func (h harness) claimApp(t *testing.T, sess pp.Session) (token, device string) {
	t.Helper()
	link, err := url.Parse(sess.Claim.DeepLink)
	if err != nil {
		t.Fatalf("deep link: %v", err)
	}
	code, claim := h.do(t, http.MethodPost, "/app/handover/"+link.Query().Get("handover")+"/claim", "", nil)
	if code != http.StatusOK {
		t.Fatalf("claim = %d %v", code, claim)
	}
	return claim["token"].(string), claim["deviceToken"].(string)
}

var faceOnlySpec = pp.FlowSpec{
	Name: "Face only", Steps: []string{"face_verification"}, FaceProvider: "regula", SelfieLocation: "native",
	RequiredChecks: []string{"face.match", "face.liveness"},
}

// A face step that failed liveness is submitted again, up to the engine's
// bound; a passed one, or the last failed one past the bound, stands.
func TestFailedLivenessRetries(t *testing.T) {
	tag := ""
	fake := livenessRegula{tag: &tag, live: map[string]bool{"fail-1": false, "fail-2": false, "fail-3": false, "pass": true}}
	h := newHarness(t, func(c *proofingengine.Config) { c.Regula = fake })
	ctx := context.Background()
	f, err := h.engine.CreateFlow(ctx, h.tenant, faceOnlySpec)
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	photo := orgPhoto(t)
	newSession := func() (string, string) {
		t.Helper()
		sess, err := h.engine.CreateSession(ctx, h.tenant, pp.SessionInput{FlowID: f.ID, ReferencePhoto: photo})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		stored, err := h.sessions.Get(h.tenant.ID, sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		tag = "ips-" + stored.TenantReference
		return h.claimApp(t, sess)
	}
	selfie := func(token, device, tx string) (int, map[string]any) {
		t.Helper()
		return h.do(t, http.MethodPost, "/app/"+token+"/steps/selfie", device, map[string]any{"livenessTransactionId": tx})
	}

	token, device := newSession()
	if code, step := selfie(token, device, "fail-1"); code != http.StatusOK || step["alreadyRecorded"] == true {
		t.Fatalf("failed attempt = %d %v", code, step)
	}
	code, step := selfie(token, device, "pass")
	if bio, _ := step["biometrics"].(map[string]any); code != http.StatusOK || step["alreadyRecorded"] == true || bio["livenessResult"] != "passed" {
		t.Fatalf("retry after a failed liveness = %d %v; want the passed attempt recorded", code, step)
	}
	if code, step := selfie(token, device, "fail-2"); code != http.StatusOK || step["alreadyRecorded"] != true {
		t.Errorf("attempt after a pass = %d %v; want already recorded", code, step)
	}

	token, device = newSession()
	for _, tx := range []string{"fail-1", "fail-2", "fail-3"} {
		if code, step := selfie(token, device, tx); code != http.StatusOK || step["alreadyRecorded"] == true {
			t.Fatalf("attempt %s = %d %v; want recorded", tx, code, step)
		}
	}
	if code, step := selfie(token, device, "pass"); code != http.StatusOK || step["alreadyRecorded"] != true {
		t.Errorf("attempt past the bound = %d %v; want already recorded", code, step)
	}
}

// The chip read is refused where the flow has none and without the photo a
// face match needs; a non-Dutch driving licence is refused at document_capture
// (422 document_unsupported) while a Dutch one and a passport go through.
func TestNFCStepRefusals(t *testing.T) {
	h := newHarness(t, func(c *proofingengine.Config) { c.Regula = fakeRegula{similarity: 0.9} })
	ctx := context.Background()
	evidence := map[string]any{
		"efSod":      mrtdtestfixtures.TestSodHex,
		"dataGroups": map[string]string{"DG1": mrtdtestfixtures.TestDg1Hex, "DG2": mrtdtestfixtures.Dg2Hex},
	}
	start := func(spec pp.FlowSpec, in pp.SessionInput) (string, string) {
		t.Helper()
		f, err := h.engine.CreateFlow(ctx, h.tenant, spec)
		if err != nil {
			t.Fatalf("CreateFlow(%s): %v", spec.Name, err)
		}
		in.FlowID = f.ID
		sess, err := h.engine.CreateSession(ctx, h.tenant, in)
		if err != nil {
			t.Fatalf("CreateSession(%s): %v", spec.Name, err)
		}
		return h.claimApp(t, sess)
	}
	postCapture := func(token, device, docType, chipType string) (int, map[string]any) {
		t.Helper()
		return h.do(t, http.MethodPost, "/app/"+token+"/steps/document_capture", device, map[string]any{
			"document":   map[string]any{"type": docType, "number": "099250692", "issuingState": "GBR"},
			"chipAccess": map[string]any{"documentType": chipType, "documentNumber": "099250692", "countryCode": "GBR", "dateOfBirth": "1978-04-05", "dateOfExpiry": "2022-07-17"},
		})
	}
	capture := func(token, device, documentType string) {
		t.Helper()
		if code, step := postCapture(token, device, "P", documentType); code != http.StatusOK {
			t.Fatalf("document_capture = %d %v", code, step)
		}
	}
	unsupported := func(what string, code int, step map[string]any) {
		t.Helper()
		if code != http.StatusUnprocessableEntity || step["code"] != "document_unsupported" {
			t.Errorf("%s = %d %v, want 422 document_unsupported", what, code, step)
		}
	}

	photo := orgPhoto(t)
	token, device := start(faceOnlySpec, pp.SessionInput{ReferencePhoto: photo})
	if code, step := h.do(t, http.MethodPost, "/app/"+token+"/steps/nfc", device, map[string]any{"mrtdEvidence": evidence}); code != http.StatusBadRequest {
		t.Errorf("nfc on a flow without nfc_read = %d %v, want 400", code, step)
	}

	token, device = start(pp.FlowSpec{
		Name: "Chip and face", Steps: []string{"document_capture", "nfc_read", "face_verification"},
		FaceProvider: "regula", SelfieLocation: "native", RequiredChecks: []string{"nfc.passive_auth", "face.match"},
	}, pp.SessionInput{})
	capture(token, device, "passport")
	code, step := h.do(t, http.MethodPost, "/app/"+token+"/steps/nfc", device, map[string]any{"mrtdEvidence": evidence})
	if msg, _ := step["error"].(string); code != http.StatusBadRequest || !strings.HasPrefix(msg, "photo is required") {
		t.Errorf("nfc without a photo on a face-match flow = %d %v, want 400 photo is required", code, step)
	}

	token, device = start(chipFlow, pp.SessionInput{})
	code, step = postCapture(token, device, "P", "drivers_license")
	unsupported("document_capture with a licence chip access key", code, step)
	code, step = postCapture(token, device, "drivers_license", "drivers_license")
	unsupported("document_capture with a licence document", code, step)
	licence := map[string]any{"documentType": "eu_driving_licence", "efSod": evidence["efSod"], "dataGroups": evidence["dataGroups"]}
	code, step = h.do(t, http.MethodPost, "/app/"+token+"/steps/nfc", device, map[string]any{"mrtdEvidence": licence})
	if msg, _ := step["error"].(string); code != http.StatusBadRequest || !strings.Contains(msg, "no driving licence personal data") {
		t.Errorf("nfc with a passport's chip as a licence = %d %v, want 400 no driving licence personal data", code, step)
	}
	capture(token, device, "passport")
	if code, step = h.do(t, http.MethodPost, "/app/"+token+"/steps/nfc", device, map[string]any{"mrtdEvidence": evidence}); code != http.StatusOK {
		t.Errorf("nfc with a passport's chip = %d %v, want 200", code, step)
	}

	token, device = start(chipFlow, pp.SessionInput{})
	code, step = h.do(t, http.MethodPost, "/app/"+token+"/steps/document_capture", device, map[string]any{
		"document":   map[string]any{"type": "drivers_license", "number": "5012345678", "issuingState": "NLD"},
		"chipAccess": map[string]any{"documentType": "drivers_license", "documentNumber": "5012345678", "countryCode": "NLD", "version": "1", "randomData": "ABCDEFGHIJK", "configuration": "1"},
	})
	if code != http.StatusOK {
		t.Errorf("document_capture with a Dutch licence = %d %v, want 200", code, step)
	}
}

// A Dutch licence chip passes nfc_read and the session approves with the
// chip's identity, its EF.SOD anchored in the configured CSCA; a licence chip
// from another state is refused at nfc_read (422 document_unsupported).
func TestDrivingLicenceChip(t *testing.T) {
	dutch, err := mrtdtestfixtures.NewLicenceChip(mrtdverify.DrivingLicenceIssuingState)
	if err != nil {
		t.Fatalf("NewLicenceChip(NLD): %v", err)
	}
	german, err := mrtdtestfixtures.NewLicenceChip("DEU")
	if err != nil {
		t.Fatalf("NewLicenceChip(DEU): %v", err)
	}
	var trusted cms.GenericCertPool
	for _, csca := range [][]byte{dutch.CSCA, german.CSCA} {
		if err := trusted.Add(csca); err != nil {
			t.Fatalf("trust CSCA: %v", err)
		}
	}
	h := newHarness(t, func(c *proofingengine.Config) { c.DrivingLicenceCertPool = &trusted })
	ctx := context.Background()
	f, err := h.engine.CreateFlow(ctx, h.tenant, chipFlow)
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	start := func() (pp.Session, string, string) {
		t.Helper()
		sess, err := h.engine.CreateSession(ctx, h.tenant, pp.SessionInput{FlowID: f.ID})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		token, device := h.claimApp(t, sess)
		code, step := h.do(t, http.MethodPost, "/app/"+token+"/steps/document_capture", device, map[string]any{
			"document":   map[string]any{"type": "drivers_license", "number": mrtdtestfixtures.LicenceDocumentNumber, "issuingState": "NLD"},
			"chipAccess": map[string]any{"documentType": "drivers_license", "documentNumber": mrtdtestfixtures.LicenceDocumentNumber, "countryCode": "NLD", "version": "1", "randomData": "ABCDEFGHIJK", "configuration": "1"},
		})
		if code != http.StatusOK {
			t.Fatalf("document_capture = %d %v", code, step)
		}
		return sess, token, device
	}
	nfc := func(token, device string, chip mrtdtestfixtures.LicenceChip) (int, map[string]any) {
		t.Helper()
		return h.do(t, http.MethodPost, "/app/"+token+"/steps/nfc", device, map[string]any{"mrtdEvidence": map[string]any{
			"documentType": "eu_driving_licence", "efSod": chip.SODHex, "dataGroups": map[string]string{"DG1": chip.DG1Hex},
		}})
	}

	sess, token, device := start()
	if code, step := nfc(token, device, dutch); code != http.StatusOK || step["readyToSubmit"] != true {
		t.Fatalf("nfc with a Dutch licence chip = %d %v; want ready to submit", code, step)
	}
	if code, step := h.do(t, http.MethodPost, "/app/"+token+"/submit", device, nil); code != http.StatusOK || step["lifecycle"] != "COMPLETE" {
		t.Fatalf("submit = %d %v; want the session complete", code, step)
	}
	res, err := h.engine.SessionResult(ctx, h.tenant, sess.ID, sess.Token)
	if err != nil || res.Status != pp.StatusApproved {
		t.Errorf("result = %+v, %v; want approved", res, err)
	}
	id, err := h.engine.SessionIdentity(ctx, h.tenant, sess.ID, sess.Token)
	if err != nil || id.Evidence == nil || id.Evidence.PassiveAuth == pp.CheckNotPerformed {
		t.Errorf("identity = %+v, %v; want chip evidence with passive authentication run", id, err)
	}
	if id.FamilyName != mrtdtestfixtures.LicenceSurname || id.GivenName != mrtdtestfixtures.LicenceOtherNames || id.BirthDate != mrtdtestfixtures.LicenceDateOfBirth {
		t.Errorf("identity = %s %s, %s; want the licence chip's holder", id.GivenName, id.FamilyName, id.BirthDate)
	}

	_, token, device = start()
	if code, step := nfc(token, device, german); code != http.StatusUnprocessableEntity || step["code"] != "document_unsupported" {
		t.Errorf("nfc with a German licence chip = %d %v, want 422 document_unsupported", code, step)
	}
}

// An age-check flow never stores the date of birth: not from the MRZ scan,
// not from the chip read, not in the result. The name still comes back, and
// the chip checks kept at the chip read score the finish.
func TestAgeCheckStoresNoBirthDate(t *testing.T) {
	const birthDate = "1978-04-05"
	h := newHarness(t)
	ctx := context.Background()
	f, err := h.engine.CreateFlow(ctx, h.tenant, pp.FlowSpec{
		Name: "Age check", Steps: []string{"document_capture", "nfc_read"},
		RequestedAttributes: []string{"dg1", "age_over", "chip_checks"}, AgeOver: 18,
		RequiredChecks: []string{"nfc.passive_auth"},
	})
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	sess, err := h.engine.CreateSession(ctx, h.tenant, pp.SessionInput{FlowID: f.ID})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	token, device := h.claimApp(t, sess)
	leaks := []string{birthDate, mrtdtestfixtures.TestDg1Hex}

	code, step := h.do(t, http.MethodPost, "/app/"+token+"/steps/document_capture", device, map[string]any{
		"document":   map[string]any{"type": "P", "number": "099250692", "issuingState": "GBR", "dateOfBirth": birthDate},
		"chipAccess": map[string]any{"documentType": "passport", "documentNumber": "099250692", "countryCode": "GBR", "dateOfBirth": birthDate, "dateOfExpiry": "2022-07-17"},
	})
	if code != http.StatusOK {
		t.Fatalf("document_capture = %d %v", code, step)
	}
	if got := h.storedWithout(t, sess.ID, "document_capture", leaks...); got.Steps.Document == nil || got.Steps.Document.ChipAccess != nil {
		t.Errorf("document = %+v, want one without the chip access key", got.Steps.Document)
	}

	code, step = h.do(t, http.MethodPost, "/app/"+token+"/steps/nfc", device, map[string]any{"mrtdEvidence": map[string]any{
		"efSod":      mrtdtestfixtures.TestSodHex,
		"dataGroups": map[string]string{"DG1": mrtdtestfixtures.TestDg1Hex, "DG2": mrtdtestfixtures.Dg2Hex},
	}})
	if code != http.StatusOK {
		t.Fatalf("nfc = %d %v", code, step)
	}
	if got := h.storedWithout(t, sess.ID, "nfc_read", leaks...); got.Steps.NFC == nil || got.Steps.NFC.AgeOver == nil || *got.Steps.NFC.AgeOver != (session.AgeOver{Age: 18, Over: true}) {
		t.Errorf("nfc evidence = %+v, want age 18, over", got.Steps.NFC)
	}

	if code, step = h.do(t, http.MethodPost, "/app/"+token+"/submit", device, nil); code != http.StatusOK {
		t.Fatalf("submit = %d %v", code, step)
	}
	h.storedWithout(t, sess.ID, "submit", leaks...)
	id, err := h.engine.SessionIdentity(ctx, h.tenant, sess.ID, sess.Token)
	if err != nil {
		t.Fatalf("SessionIdentity: %v", err)
	}
	if id.FamilyName == "" || id.BirthDate != "" {
		t.Errorf("identity = %q, %q; want the name without the date of birth", id.FamilyName, id.BirthDate)
	}
	if id.AgeOver == nil || *id.AgeOver != (pp.AgeOver{Age: 18, Over: true}) {
		t.Errorf("ageOver = %+v, want age 18, over", id.AgeOver)
	}
	if id.Evidence == nil || id.Evidence.PassiveAuth == pp.CheckNotPerformed {
		t.Errorf("evidence = %+v, want passive authentication scored", id.Evidence)
	}
}

// The Yivi app answers an age check from its age claim. A date of birth it
// discloses anyway is never stored, and the name still comes back. The flow's
// face step is the Idem app's: the Yivi session never calls Regula.
func TestYiviAgeCheckStoresNoBirthDate(t *testing.T) {
	const birthDate = "1990-05-01"
	var regulaCalls atomic.Int32
	h := newHarness(t, func(c *proofingengine.Config) { c.Regula = fakeRegula{similarity: 0.9, calls: &regulaCalls} })
	ctx := context.Background()
	f, err := h.engine.CreateFlow(ctx, h.tenant, pp.FlowSpec{
		Name: "Yivi age check", Steps: []string{"document_capture", "nfc_read", "face_verification"},
		RequestedAttributes: []string{"dg1", "age_over"}, AgeOver: 18,
		FaceProvider: "regula", SelfieLocation: "native", RequiredChecks: []string{"nfc.passive_auth", "face.match"},
	})
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	sess, err := h.engine.CreateSession(ctx, h.tenant, pp.SessionInput{FlowID: f.ID, Method: pp.MethodYivi})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, err := h.engine.SubmitReference(ctx, h.tenant, sess.ID, sess.Token, pp.Reference{
		Credential: "pbdf-staging.pbdf.passport",
		Attributes: map[string]string{"firstName": "Anna", "lastName": "Jansen", "over18": "Yes", "dateOfBirth": birthDate, "yearOfBirth": "1990"},
	})
	if err != nil || !got.OK {
		t.Fatalf("SubmitReference = %+v, %v", got, err)
	}
	h.storedWithout(t, sess.ID, "the disclosure", birthDate, "yearOfBirth")

	id, err := h.engine.SessionIdentity(ctx, h.tenant, sess.ID, sess.Token)
	if err != nil {
		t.Fatalf("SessionIdentity: %v", err)
	}
	if id.GivenName != "Anna" || id.BirthDate != "" {
		t.Errorf("identity = %q, %q; want the name without the date of birth", id.GivenName, id.BirthDate)
	}
	if id.AgeOver == nil || *id.AgeOver != (pp.AgeOver{Age: 18, Over: true}) {
		t.Errorf("ageOver = %+v, want age 18, over", id.AgeOver)
	}
	if got := regulaCalls.Load(); got != 0 {
		t.Errorf("Regula called %d times, want none", got)
	}
}

// storedWithout is session id as stored, failing t where it holds any of
// leaks.
func (h harness) storedWithout(t *testing.T, id, when string, leaks ...string) session.Session {
	t.Helper()
	got, err := h.sessions.Get(h.tenant.ID, id)
	if err != nil {
		t.Fatalf("Get after %s: %v", when, err)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range leaks {
		if strings.Contains(string(data), leak) {
			t.Errorf("stored session after %s holds %.20q", when, leak)
		}
	}
	return got
}
