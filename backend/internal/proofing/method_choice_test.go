package proofing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// An on-screen session starts with its flow and no app: the app is chosen
// first and gives the QR code, and the document photo is taken after it.
func TestOnScreenChoosesAppBeforeTheScan(t *testing.T) {
	f, _ := newScanFixture(t)
	ctx := context.Background()
	f.ips.noClaim = true
	sent := f.sendOnScreen(t, photoFlow.ID, proofingprovider.MethodPending)
	id := sent.Request.ID
	if f.ips.sessions[0].Method != proofingprovider.MethodPending || f.requests.stored.Method != "" || sent.DeepLink != "" {
		t.Fatalf("started with %q, stored %q, link %q; want a session with no app yet", f.ips.sessions[0].Method, f.requests.stored.Method, sent.DeepLink)
	}

	if _, err := f.svc.ChooseMethod(ctx, testOrg.ID, id, nil, proofingprovider.MethodBrowser); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("the browser as the app: %v, want ErrInvalidInput", err)
	}
	chosen, err := f.svc.ChooseMethod(ctx, testOrg.ID, id, nil, proofingprovider.MethodIdem)
	if err != nil {
		t.Fatalf("ChooseMethod: %v", err)
	}
	if chosen.DeepLink == "" || f.requests.stored.Method != proofingprovider.MethodIdem {
		t.Errorf("link %q, stored %q; want the Idem QR and the app kept", chosen.DeepLink, f.requests.stored.Method)
	}
	if _, err := f.svc.ChooseMethod(ctx, testOrg.ID, id, nil, proofingprovider.MethodYivi); !errors.Is(err, ErrLinkStarted) {
		t.Errorf("a second choice: %v, want ErrLinkStarted", err)
	}
	if _, err := f.svc.StartDocumentScan(ctx, testOrg.ID, id, nil, "en"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.FinishDocumentScan(ctx, testOrg.ID, id, nil, "trx-1", "res-1"); err != nil {
		t.Fatal(err)
	}
}

// A flow without a document photo starts with no app too; the Yivi app,
// chosen after, gets no QR from the engine: its page starts the disclosure.
func TestOnScreenChoosesYiviLater(t *testing.T) {
	f, _ := newScanFixture(t)
	f.settings.selection = FlowSelection{FlowIDs: []string{appFlow.ID}, DefaultFlowID: appFlow.ID}
	f.ips.noClaim = true
	sent := f.sendOnScreen(t, appFlow.ID, proofingprovider.MethodPending)
	chosen, err := f.svc.ChooseMethod(context.Background(), testOrg.ID, sent.Request.ID, nil, proofingprovider.MethodYivi)
	if err != nil {
		t.Fatalf("ChooseMethod(yivi): %v", err)
	}
	if chosen.DeepLink != "" || f.requests.stored.Method != proofingprovider.MethodYivi {
		t.Errorf("link %q, stored %q; want no QR and the Yivi app kept", chosen.DeepLink, f.requests.stored.Method)
	}
}

// A hosted link starts its session at Continue with no app, and the app is
// chosen before the document photo.
func TestHostedChoosesAppBeforeTheScan(t *testing.T) {
	f, _ := newScanFixture(t)
	f.svc.SetHostedBaseURL(testHostedBase)
	ctx := context.Background()
	sent, err := f.svc.CreateRequest(ctx, testOrg, Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)},
		NewRequest{FlowID: photoFlow.ID, Channel: ChannelHosted})
	if err != nil {
		t.Fatalf("CreateRequest(hosted): %v", err)
	}
	token := strings.TrimPrefix(sent.HostedURL, testHostedBase)
	if _, err := f.svc.StartHosted(ctx, token, proofingprovider.MethodPending); err != nil {
		t.Fatalf("StartHosted(pending): %v", err)
	}
	if f.requests.stored.Method != "" {
		t.Fatalf("stored method %q, want none before the choice", f.requests.stored.Method)
	}
	chosen, err := f.svc.HostedChooseMethod(ctx, token, proofingprovider.MethodIdem)
	if err != nil {
		t.Fatalf("HostedChooseMethod: %v", err)
	}
	if chosen.DeepLink == "" || f.requests.stored.Method != proofingprovider.MethodIdem {
		t.Errorf("link %q, stored %q; want the Idem QR", chosen.DeepLink, f.requests.stored.Method)
	}
	if _, err := f.svc.HostedStartDocumentScan(ctx, token, "en"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.HostedFinishDocumentScan(ctx, token, "trx-1", "res-1"); err != nil {
		t.Fatal(err)
	}
}
