package proofing

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/idscan"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// photoFlow photographs the document next to the chip read.
var photoFlow = testFlow("f-photo", "Passport + photo", []string{"document_capture", "nfc_read", stepDocumentPhoto}, "native")

// fakeScanner is the document scan service: a transaction per start, and
// result for every read.
type fakeScanner struct {
	starts    int
	locales   []string
	options   []idscan.ScanOptions
	result    idscan.Result
	resultErr error
	reads     []string
}

func (s *fakeScanner) CreateTransaction(_ context.Context, locale string, opts idscan.ScanOptions) (idscan.Transaction, error) {
	s.starts++
	s.locales, s.options = append(s.locales, locale), append(s.options, opts)
	return idscan.Transaction{ID: "trx-" + string(rune('0'+s.starts)), URL: "https://scan.test/s/" + string(rune('0'+s.starts))}, nil
}

func (s *fakeScanner) Result(_ context.Context, transactionID, resultID string) (idscan.Result, error) {
	s.reads = append(s.reads, transactionID+"/"+resultID)
	return s.result, s.resultErr
}

var scannedResult = idscan.Result{
	Completed:    true,
	Authenticity: idscan.Doubtful,
	Score:        new(0.62),
	FieldsValid:  new(true),
	ZonesMatch:   new(false),
	Document: idscan.Document{
		Type: "identity_card", IssuingState: "NLD", Number: "XR1234567", Surname: "Jansen", GivenNames: "Anna",
		DateOfBirth: "1990-03-10", DateOfExpiry: "2031-07-14", PersonalNumber: "999999990",
	},
	Front: idscan.Image{MimeType: "image/jpeg", Data: []byte("front")},
	Back:  &idscan.Image{MimeType: "image/jpeg", Data: []byte("back")},
}

// newScanFixture is newFixture with photoFlow as the members' default and a
// document scan service.
func newScanFixture(t *testing.T) (fixture, *fakeScanner) {
	t.Helper()
	f := newFixture()
	f.ips.flows = append(f.ips.flows, photoFlow)
	f.settings.selection = FlowSelection{FlowIDs: []string{photoFlow.ID}, DefaultFlowID: photoFlow.ID}
	scanner := &fakeScanner{result: scannedResult}
	f.svc.SetDocumentScanner(scanner)
	return f, scanner
}

func (f fixture) sendOnScreen(t *testing.T, flowID string, method proofingprovider.Method) Sent {
	t.Helper()
	if method == proofingprovider.MethodYivi {
		f.ips.noClaim = true
	}
	sent, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{SubjectUserID: alex.UserID, FlowID: flowID, Method: method, Channel: ChannelOnScreen})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	return sent
}

// The on-screen page photographs the document through the scan service: the
// photos of the scan it started go to the engine as document_photo, with the
// BSN covered by the service, once.
func TestDocumentScanOnScreen(t *testing.T) {
	f, scanner := newScanFixture(t)
	ctx := context.Background()
	sent := f.sendOnScreen(t, photoFlow.ID, proofingprovider.MethodIdem)
	id := sent.Request.ID
	if !f.requests.stored.DocumentScan || documentScanState(*f.requests.stored) != documentScanPending {
		t.Fatalf("request = %+v, want a pending document scan", f.requests.stored)
	}

	url, err := f.svc.StartDocumentScan(ctx, testOrg.ID, id, nil, "nl")
	if err != nil || url != "https://scan.test/s/1" {
		t.Fatalf("StartDocumentScan = %q, %v", url, err)
	}
	if scanner.locales[0] != idscan.LocaleDutch {
		t.Errorf("transaction locale = %v, want Dutch", scanner.locales)
	}

	if err := f.svc.FinishDocumentScan(ctx, testOrg.ID, id, nil, "trx-other", "res-1"); !errors.Is(err, ErrDocumentScanMismatch) {
		t.Errorf("another transaction: %v, want ErrDocumentScanMismatch", err)
	}
	scanner.result = idscan.Result{}
	if err := f.svc.FinishDocumentScan(ctx, testOrg.ID, id, nil, "trx-1", "res-1"); !errors.Is(err, ErrDocumentScanFailed) {
		t.Errorf("an unfinished scan: %v, want ErrDocumentScanFailed", err)
	}
	if len(f.ips.documentPhotos) != 0 {
		t.Fatal("an unfinished scan reached the engine")
	}

	scanner.result = scannedResult
	if err := f.svc.FinishDocumentScan(ctx, testOrg.ID, id, nil, "trx-1", "res-1"); err != nil {
		t.Fatalf("FinishDocumentScan: %v", err)
	}
	if len(f.ips.documentPhotos) != 1 {
		t.Fatalf("document photos = %d, want 1", len(f.ips.documentPhotos))
	}
	// The whole scan reaches the engine, in its terms: the BSN left out (the
	// flow does not keep it) and not blacked out (the flow does not say so).
	photo := f.ips.documentPhotos[0]
	if photo.Front.Base64 != base64.StdEncoding.EncodeToString([]byte("front")) || photo.Back == nil || photo.BSNRedacted {
		t.Errorf("photo = %+v, want front and back, the BSN not blacked out", photo)
	}
	want := proofingprovider.DocumentCheck{
		Verdict: proofingprovider.DocumentDoubtful, Score: scannedResult.Score, FieldsValid: scannedResult.FieldsValid, ZonesMatch: scannedResult.ZonesMatch,
		Document: proofingprovider.ScannedDocument{
			Kind: proofingprovider.DocumentKindIdentityCard, IssuingState: "NLD", Number: "XR1234567", Surname: "Jansen", GivenNames: "Anna",
			DateOfBirth: "1990-03-10", DateOfExpiry: "2031-07-14",
		},
	}
	if !reflect.DeepEqual(photo.Check, want) {
		t.Errorf("check = %+v, want %+v", photo.Check, want)
	}
	if documentScanState(*f.requests.stored) != documentScanDone {
		t.Errorf("state = %q, want done", documentScanState(*f.requests.stored))
	}

	// A reload of the return page finishes it again: nothing changes.
	if err := f.svc.FinishDocumentScan(ctx, testOrg.ID, id, nil, "trx-1", "res-1"); err != nil || len(f.ips.documentPhotos) != 1 {
		t.Errorf("finish again: %v, %d photos; want nothing new", err, len(f.ips.documentPhotos))
	}
	if _, err := f.svc.StartDocumentScan(ctx, testOrg.ID, id, nil, "en"); !errors.Is(err, ErrDocumentScanDone) {
		t.Errorf("a scan once the photos are in: %v, want ErrDocumentScanDone", err)
	}
}

// The scan follows the flow: the BSN blacked out only when the flow's
// "Black out the BSN" is on, and the flow's age check enforced by the scan
// service.
func TestDocumentScanFollowsTheFlow(t *testing.T) {
	for name, tc := range map[string]struct {
		flow func(*proofingprovider.Flow)
		want idscan.ScanOptions
	}{
		"as created":    {func(*proofingprovider.Flow) {}, idscan.ScanOptions{}},
		"BSN blacked":   {func(f *proofingprovider.Flow) { f.BlurBSN = new(true) }, idscan.ScanOptions{RedactBSN: true}},
		"BSN kept":      {func(f *proofingprovider.Flow) { f.BSNPolicy = bsnPolicyRetrieve }, idscan.ScanOptions{}},
		"age check, 18": {func(f *proofingprovider.Flow) { f.RequestedAttributes, f.AgeOver = []string{attributeAgeOver}, 18 }, idscan.ScanOptions{MinimumAge: 18}},
	} {
		t.Run(name, func(t *testing.T) {
			f, scanner := newScanFixture(t)
			sent := f.sendOnScreen(t, photoFlow.ID, proofingprovider.MethodIdem)
			flow := photoFlow
			flow.Version = sent.Request.FlowVersion
			tc.flow(&flow)
			f.ips.flowVersions = []proofingprovider.Flow{flow}
			if _, err := f.svc.StartDocumentScan(context.Background(), testOrg.ID, sent.Request.ID, nil, "en"); err != nil {
				t.Fatal(err)
			}
			if scanner.options[0] != tc.want || scanner.locales[0] != idscan.LocaleEnglish {
				t.Errorf("options %+v, locale %s; want %+v, English", scanner.options[0], scanner.locales[0], tc.want)
			}
		})
	}
}

// A flow that photographs the document runs on a page, and only where the
// scan service is configured.
func TestDocumentScanFlowSending(t *testing.T) {
	f, _ := newScanFixture(t)
	mailed := NewRequest{SubjectUserID: alex.UserID, FlowID: photoFlow.ID}
	if _, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"}, mailed); !errors.Is(err, ErrDocumentScanNeedsPage) {
		t.Errorf("mailed: %v, want ErrDocumentScanNeedsPage", err)
	}

	f = newFixture()
	f.ips.flows = append(f.ips.flows, photoFlow)
	f.settings.selection = FlowSelection{FlowIDs: []string{photoFlow.ID}, DefaultFlowID: photoFlow.ID}
	onScreen := NewRequest{SubjectUserID: alex.UserID, FlowID: photoFlow.ID, Channel: ChannelOnScreen}
	if _, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"}, onScreen); !errors.Is(err, ErrNoDocumentScanner) {
		t.Errorf("without a scan service: %v, want ErrNoDocumentScanner", err)
	}
}

func TestDocumentScanNotAsked(t *testing.T) {
	f, _ := newScanFixture(t)
	f.settings.selection = FlowSelection{FlowIDs: []string{appFlow.ID}, DefaultFlowID: appFlow.ID}
	sent := f.sendOnScreen(t, appFlow.ID, proofingprovider.MethodIdem)
	if _, err := f.svc.StartDocumentScan(context.Background(), testOrg.ID, sent.Request.ID, nil, "en"); !errors.Is(err, ErrDocumentScanNotAsked) {
		t.Errorf("StartDocumentScan: %v, want ErrDocumentScanNotAsked", err)
	}
	if documentScanState(*f.requests.stored) != "" {
		t.Errorf("state = %q, want none", documentScanState(*f.requests.stored))
	}
}

func TestDocumentScanAfterSessionOver(t *testing.T) {
	f, _ := newScanFixture(t)
	sent := f.sendOnScreen(t, photoFlow.ID, proofingprovider.MethodIdem)
	f.requests.stored.session.ExpiresAt = time.Now().Add(-time.Minute)
	if _, err := f.svc.StartDocumentScan(context.Background(), testOrg.ID, sent.Request.ID, nil, "en"); !errors.Is(err, ErrSessionOver) {
		t.Errorf("StartDocumentScan: %v, want ErrSessionOver", err)
	}
}

// The engine refusing the photos as such fails the scan; refusing them as the
// session decided ends it.
func TestDocumentScanEngineRefusal(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		want   error
	}{
		"bad photo":       {http.StatusBadRequest, ErrDocumentScanFailed},
		"session decided": {http.StatusConflict, ErrSessionOver},
		"session gone":    {http.StatusGone, ErrSessionOver},
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := newScanFixture(t)
			sent := f.sendOnScreen(t, photoFlow.ID, proofingprovider.MethodIdem)
			if _, err := f.svc.StartDocumentScan(context.Background(), testOrg.ID, sent.Request.ID, nil, "en"); err != nil {
				t.Fatal(err)
			}
			f.ips.documentPhotoErr = &proofingprovider.RejectedError{Status: tc.status}
			if err := f.svc.FinishDocumentScan(context.Background(), testOrg.ID, sent.Request.ID, nil, "trx-1", "res-1"); !errors.Is(err, tc.want) {
				t.Errorf("FinishDocumentScan: %v, want %v", err, tc.want)
			}
			if f.requests.stored.DocumentScannedAt != nil {
				t.Error("a refused photo marked the scan done")
			}
		})
	}
}

// A flow the browser runs alone offers no app, the Yivi app included.
func TestBrowserOnlyFlowOffersNoApp(t *testing.T) {
	photoOnly := testFlow("f-photo-only", "Photo", []string{stepDocumentPhoto}, "")
	photoOnly.BrowserOnly = true
	if yiviAppAvailable(photoOnly) {
		t.Error("yiviAppAvailable(browser-only flow) = true, want no app")
	}
}

// A browser-only flow runs with the browser method, whatever app is left
// unasked; asking for an app is refused, as is the browser on a flow with app
// steps.
func TestSessionMethod(t *testing.T) {
	photoOnly := testFlow("f-photo-only", "Photo", []string{stepDocumentPhoto}, "")
	photoOnly.BrowserOnly = true
	for _, tc := range []struct {
		flow  proofingprovider.Flow
		asked proofingprovider.Method
		want  proofingprovider.Method
	}{
		{photoOnly, "", proofingprovider.MethodBrowser},
		{photoOnly, proofingprovider.MethodBrowser, proofingprovider.MethodBrowser},
		{photoOnly, proofingprovider.MethodIdem, ""},
		{photoFlow, "", proofingprovider.MethodIdem},
		{photoFlow, proofingprovider.MethodYivi, proofingprovider.MethodYivi},
		{photoFlow, proofingprovider.MethodBrowser, ""},
	} {
		got, err := sessionMethod(tc.flow, tc.asked)
		if got != tc.want || (tc.want == "") != errors.Is(err, ErrInvalidInput) {
			t.Errorf("sessionMethod(%s, %q) = %q, %v; want %q", tc.flow.Name, tc.asked, got, err, tc.want)
		}
	}
}

// The Yivi app runs a flow that photographs the document: the photo is taken
// in the browser. A disclosure before it is in waits for it.
func TestYiviRunsDocumentPhotoFlow(t *testing.T) {
	if !yiviAppAvailable(photoFlow) {
		t.Fatal("yiviAppAvailable(photoFlow) = false, want the Yivi app offered")
	}
	f, _ := newScanFixture(t)
	sent := f.sendOnScreen(t, photoFlow.ID, proofingprovider.MethodYivi)
	if _, err := f.svc.StartYivi(context.Background(), testOrg.ID, sent.Request.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.verifier.presentation = &openid4vpverifier.Presentation{ByCredential: map[string]map[string]string{
		"idcard": {openid4vpverifier.ClaimGivenNames: "Anna"},
	}}
	f.ips.referenceErr = &proofingprovider.RejectedError{Status: http.StatusConflict, Code: proofingprovider.CodeDocumentPhotoMissing}
	if _, err := f.svc.YiviDisclosure(context.Background(), testOrg.ID, sent.Request.ID, nil); !errors.Is(err, ErrDocumentScanPending) {
		t.Errorf("YiviDisclosure: %v, want ErrDocumentScanPending", err)
	}
}

func TestHostedDocumentScan(t *testing.T) {
	f, scanner := newScanFixture(t)
	f.svc.SetHostedBaseURL(testHostedBase)
	sent, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)},
		NewRequest{FlowID: photoFlow.ID, Channel: ChannelHosted})
	if err != nil {
		t.Fatalf("CreateRequest(hosted): %v", err)
	}
	token := strings.TrimPrefix(sent.HostedURL, testHostedBase)
	if _, err := f.svc.StartHosted(context.Background(), token, proofingprovider.MethodIdem); err != nil {
		t.Fatalf("StartHosted: %v", err)
	}
	if got := newHostedProgress(*f.requests.stored, time.Now()).DocumentScan; got != documentScanPending {
		t.Fatalf("progress documentScan = %q, want pending", got)
	}
	if _, err := f.svc.HostedStartDocumentScan(context.Background(), token, "en"); err != nil {
		t.Fatalf("HostedStartDocumentScan: %v", err)
	}
	if err := f.svc.HostedFinishDocumentScan(context.Background(), token, "trx-1", "res-1"); err != nil {
		t.Fatalf("HostedFinishDocumentScan: %v", err)
	}
	if len(f.ips.documentPhotos) != 1 || scanner.reads[0] != "trx-1/res-1" {
		t.Errorf("photos %d, reads %v; want the scan's photos in", len(f.ips.documentPhotos), scanner.reads)
	}
	if got := newHostedProgress(*f.requests.stored, time.Now()).DocumentScan; got != documentScanDone {
		t.Errorf("progress documentScan = %q, want done", got)
	}
}
