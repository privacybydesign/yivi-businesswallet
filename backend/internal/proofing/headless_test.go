package proofing

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/idscan"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// An org building its own page runs a whole session with its API key: it
// starts the Yivi app, photographs the document through the scan service's
// page, and its poll hands the engine the disclosure once both are in. No page
// of the wallet's takes part.
func TestHeadlessYiviWithDocumentScan(t *testing.T) {
	f, scanner := newScanFixture(t)
	f.svc.SetHostedBaseURL(testHostedBase)
	f.ips.noClaim = true
	ctx := context.Background()
	sent, err := f.svc.CreateRequest(ctx, testOrg, Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)},
		NewRequest{FlowID: photoFlow.ID, Channel: ChannelHosted})
	if err != nil {
		t.Fatalf("CreateRequest(hosted): %v", err)
	}
	id := sent.Request.ID

	if _, err := f.svc.APIStartDocumentScan(ctx, testOrg.ID, id, "nl"); !errors.Is(err, ErrSessionNotStarted) {
		t.Errorf("document scan before the session started = %v, want %v", err, ErrSessionNotStarted)
	}
	started, err := f.svc.StartHeadless(ctx, testOrg.ID, id, proofingprovider.MethodYivi)
	if err != nil || started.WalletLink == "" {
		t.Fatalf("StartHeadless = %+v, %v; want the Yivi app's link", started, err)
	}
	if _, err := f.svc.HeadlessStatus(ctx, testOrg.ID, id, proofingprovider.MethodIdem); !errors.Is(err, ErrWrongMethod) {
		t.Errorf("status under another method = %v, want %v", err, ErrWrongMethod)
	}

	// The person has not shared anything yet: the poll asks, and waits.
	if req, err := f.svc.HeadlessStatus(ctx, testOrg.ID, id, proofingprovider.MethodYivi); err != nil || req.Status.Settled() {
		t.Fatalf("status while the person is in the Yivi app = %s, %v; want running", req.Status, err)
	}
	if len(f.verifier.polled) != 1 || len(f.ips.references) != 0 {
		t.Errorf("polled %v, references %d; want the verifier asked and nothing handed over", f.verifier.polled, len(f.ips.references))
	}

	// Shared, but the document is not photographed yet: the engine waits.
	f.verifier.presentation = &openid4vpverifier.Presentation{ByCredential: map[string]map[string]string{
		"idcard": {openid4vpverifier.ClaimGivenNames: "Anna"},
	}}
	f.ips.referenceErr = &proofingprovider.RejectedError{Status: http.StatusConflict, Code: proofingprovider.CodeDocumentPhotoMissing}
	if _, err := f.svc.HeadlessStatus(ctx, testOrg.ID, id, proofingprovider.MethodYivi); err != nil {
		t.Fatalf("status while the document photo is missing = %v, want no error", err)
	}

	f.ips.referenceErr = nil
	photographOnOwnPage(t, f, scanner, id)

	if _, err := f.svc.HeadlessStatus(ctx, testOrg.ID, id, proofingprovider.MethodYivi); err != nil {
		t.Fatalf("status after the scan: %v", err)
	}
	if len(f.ips.references) == 0 {
		t.Error("the disclosure never reached the engine")
	}
}

// photographOnOwnPage runs request id's document photo through the scan
// service's page the org embeds: started, a wrong transaction refused, then
// finished, its photos reaching the engine.
func photographOnOwnPage(t *testing.T, f fixture, scanner *fakeScanner, id uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	url, err := f.svc.APIStartDocumentScan(ctx, testOrg.ID, id, "nl")
	if err != nil || url == "" || scanner.starts != 1 {
		t.Fatalf("APIStartDocumentScan = %q, %v (%d starts); want the scan page", url, err, scanner.starts)
	}
	if err := f.svc.APIFinishDocumentScan(ctx, testOrg.ID, id, "trx-other", "res-1"); !errors.Is(err, ErrDocumentScanMismatch) {
		t.Errorf("finishing another transaction = %v, want %v", err, ErrDocumentScanMismatch)
	}
	if err := f.svc.APIFinishDocumentScan(ctx, testOrg.ID, id, "trx-1", "res-1"); err != nil {
		t.Fatalf("APIFinishDocumentScan: %v", err)
	}
	if len(f.ips.documentPhotos) != 1 || scanner.reads[0] != "trx-1/res-1" {
		t.Errorf("photos %d, reads %v; want the scan's photos in", len(f.ips.documentPhotos), scanner.reads)
	}
}

// The scan routes work only on the org's own external sessions.
func TestAPIDocumentScanNeedsTheOrgsSession(t *testing.T) {
	f, _ := newScanFixture(t)
	ctx := context.Background()
	if _, err := f.svc.APIStartDocumentScan(ctx, testOrg.ID, uuid.New(), "en"); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("start on an unknown session = %v, want %v", err, ErrRequestNotFound)
	}
	if err := f.svc.APIFinishDocumentScan(ctx, testOrg.ID, uuid.New(), "trx-1", "res-1"); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("finish on an unknown session = %v, want %v", err, ErrRequestNotFound)
	}
	// A member's own request is not one the API reaches.
	sent := f.sendOnScreen(t, photoFlow.ID, proofingprovider.MethodIdem)
	if _, err := f.svc.APIStartDocumentScan(ctx, testOrg.ID, sent.Request.ID, "en"); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("start on a member's request = %v, want %v", err, ErrRequestNotFound)
	}
}

// A hosted session cancelled before it started is over: the scan routes say
// so, not that it waits to be started.
func TestAPIDocumentScanOnCancelledSession(t *testing.T) {
	f, _ := newScanFixture(t)
	f.svc.SetHostedBaseURL(testHostedBase)
	ctx := context.Background()
	sent, err := f.svc.CreateRequest(ctx, testOrg, Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)},
		NewRequest{FlowID: photoFlow.ID, Channel: ChannelHosted})
	if err != nil {
		t.Fatalf("CreateRequest(hosted): %v", err)
	}
	id := sent.Request.ID
	if _, err := f.svc.CancelRequest(ctx, testOrg.ID, id); err != nil {
		t.Fatalf("CancelRequest: %v", err)
	}
	if _, err := f.svc.APIStartDocumentScan(ctx, testOrg.ID, id, "nl"); !errors.Is(err, ErrSessionOver) {
		t.Errorf("scan on a cancelled session = %v, want %v", err, ErrSessionOver)
	}
	if err := f.svc.APIFinishDocumentScan(ctx, testOrg.ID, id, "trx-1", "res-1"); !errors.Is(err, ErrSessionOver) {
		t.Errorf("finish on a cancelled session = %v, want %v", err, ErrSessionOver)
	}
}

// On an org's own page the scan service's page posts nothing once its attempts
// ran out: the headless poll fails the session.
func TestHeadlessPollFailsSpentDocumentScan(t *testing.T) {
	f, scanner := newScanFixture(t)
	f.svc.SetHostedBaseURL(testHostedBase)
	f.ips.noClaim = true
	ctx := context.Background()
	sent, err := f.svc.CreateRequest(ctx, testOrg, Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)},
		NewRequest{FlowID: photoFlow.ID, Channel: ChannelHosted})
	if err != nil {
		t.Fatalf("CreateRequest(hosted): %v", err)
	}
	id := sent.Request.ID
	if _, err := f.svc.StartHeadless(ctx, testOrg.ID, id, proofingprovider.MethodYivi); err != nil {
		t.Fatalf("StartHeadless: %v", err)
	}
	if _, err := f.svc.APIStartDocumentScan(ctx, testOrg.ID, id, "nl"); err != nil {
		t.Fatalf("APIStartDocumentScan: %v", err)
	}
	scanner.status = idscan.Status{State: idscan.StateFailed, Failure: &idscan.Failure{
		Task: idscan.TaskDocumentScan, State: idscan.StateMaximumAttemptsExceeded, Attempts: 3,
	}}
	if _, err := f.svc.HeadlessStatus(ctx, testOrg.ID, id, proofingprovider.MethodYivi); err != nil {
		t.Fatalf("HeadlessStatus: %v", err)
	}
	if len(f.ips.scanFailures) != 1 {
		t.Errorf("failures %+v; want the session failed once the attempts ran out", f.ips.scanFailures)
	}
}
