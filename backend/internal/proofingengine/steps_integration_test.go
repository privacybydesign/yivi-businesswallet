//go:build integration

package proofingengine_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/mrtdverify/mrtdtestfixtures"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
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
	photo := &pp.Image{MimeType: "image/jpeg", Base64: base64.StdEncoding.EncodeToString([]byte("the org's photo"))}
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
// face match needs; an EU driving licence is refused at both document steps
// (422 document_unsupported) while a passport goes through.
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

	photo := &pp.Image{MimeType: "image/jpeg", Base64: base64.StdEncoding.EncodeToString([]byte("the org's photo"))}
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
	unsupported("nfc with licence evidence", code, step)
	capture(token, device, "passport")
	if code, step = h.do(t, http.MethodPost, "/app/"+token+"/steps/nfc", device, map[string]any{"mrtdEvidence": evidence}); code != http.StatusOK {
		t.Errorf("nfc with a passport's chip = %d %v, want 200", code, step)
	}
}
