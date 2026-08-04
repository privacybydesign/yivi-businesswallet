//go:build integration

package qerdsprovider_test

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
)

// pmodeXML is the blue->red self-send PMode the bench (and this test) relies on;
// it is the same file the compose `domibus-provision` service uploads.
//
//go:embed testdata/pmode.xml
var pmodeXML []byte

// Env knobs. QERDS_TEST_DOMIBUS_URL gates the whole suite (unset => skip, like
// testdb's TEST_DATABASE_URL). It is the Domibus base URL, e.g.
// http://localhost:8090/domibus; the WS-plugin and admin REST endpoints hang off
// it. Admin creds default to the image's demo account.
const (
	envDomibusURL      = "QERDS_TEST_DOMIBUS_URL"
	envDomibusUser     = "QERDS_TEST_DOMIBUS_USER"
	envDomibusPassword = "QERDS_TEST_DOMIBUS_PASSWORD"

	defaultDomibusUser     = "admin"
	defaultDomibusPassword = "123456"
)

// domibusConfig mirrors the QERDS_DOMIBUS_* config defaults and the
// parties/service/action in testdata/pmode.xml.
var domibusConfig = qerdsprovider.DomibusConfig{
	FromParty:   "domibus-blue",
	ToParty:     "domibus-red",
	PartyType:   "urn:oasis:names:tc:ebcore:partyid-type:unregistered",
	Service:     "bdx:noprocess",
	ServiceType: "tc1",
	Action:      "TC1Leg1",
}

func domibusBaseURL(t *testing.T) string {
	t.Helper()
	base := strings.TrimRight(os.Getenv(envDomibusURL), "/")
	if base == "" {
		t.Skipf("%s not set; skipping live Domibus integration test", envDomibusURL)
	}
	return base
}

func newProvider(base string) *qerdsprovider.DomibusProvider {
	return qerdsprovider.NewDomibusProvider(
		base+"/services/backend",
		qerdsprovider.NewTokenAuthenticator(""),
		domibusConfig,
		&http.Client{Timeout: 30 * time.Second},
	)
}

const (
	// pmodeReloadWindow bounds how long sendAwaitingPMode retries the transient
	// post-upload PMode race (see its doc comment).
	pmodeReloadWindow = 30 * time.Second
	pmodeRetryDelay   = time.Second
	// pmodeMismatchMarker is the ebMS error name Domibus returns while the just-
	// uploaded PMode has not yet reached the submit path (EBMS:0010).
	pmodeMismatchMarker = "ProcessingModeMismatch"
)

// sendAwaitingPMode submits msg, retrying ONLY the transient
// "ProcessingModeMismatch: PMode could not be found" fault until the PMode is
// live or pmodeReloadWindow elapses. uploadPMode persists the PMode
// synchronously (the /rest/pmode POST returns 200) but the MSH submit path
// picks it up via an async PModeProvider refresh, so the first submit after a
// fresh CI boot can race ahead of that reload and be rejected with EBMS:0010.
// Such a submit is rejected outright (nothing is queued), so retrying is safe —
// it cannot double-deliver. Any other error fails the test immediately.
func sendAwaitingPMode(ctx context.Context, t *testing.T, provider *qerdsprovider.DomibusProvider, msg qerdsprovider.OutboundMessage) qerdsprovider.SendReceipt {
	t.Helper()
	deadline := time.Now().Add(pmodeReloadWindow)
	for {
		receipt, err := provider.Send(ctx, msg)
		if err == nil {
			return receipt
		}
		if !strings.Contains(err.Error(), pmodeMismatchMarker) {
			t.Fatalf("Send: %v", err)
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			t.Fatalf("Send: PMode still not loaded after %s: %v", pmodeReloadWindow, err)
		}
		time.Sleep(pmodeRetryDelay)
	}
}

// TestDomibusProviderSendIntegration exercises the real DomibusProvider against a
// live Domibus AS4 access point: it provisions Domibus, pings the WS plugin, and
// submits a message, asserting Domibus accepts it and returns a provider ref.
func TestDomibusProviderSendIntegration(t *testing.T) {
	base := domibusBaseURL(t)
	provisionDomibus(t, base)
	provider := newProvider(base)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := provider.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	receipt := sendAwaitingPMode(ctx, t, provider, qerdsprovider.OutboundMessage{
		Sender:    "sender@qerds.localhost",
		Recipient: "recipient@qerds.localhost",
		Subject:   "integration test",
		Body:      "hello from the QERDS Domibus integration test",
	})
	if receipt.ProviderRef == "" {
		t.Error("Send returned an empty ProviderRef; expected the Domibus message id")
	}
	if receipt.Status != qerdsprovider.StatusSubmitted {
		t.Errorf("Send status = %q, want %q", receipt.Status, qerdsprovider.StatusSubmitted)
	}
}

// TestDomibusProviderInboundRoundTripIntegration is the full loopback: send a
// message, then Fetch it back on the recipient address. This is the path that
// covers listPendingMessages + retrieveMessage against a real access point — the
// offline marshalling tests can't (a namespace bug in the retrieveMessage
// request slipped through them and only surfaced here). It relies on the message
// filter routing inbound to the WS plugin, which provisionDomibus configures.
func TestDomibusProviderInboundRoundTripIntegration(t *testing.T) {
	base := domibusBaseURL(t)
	provisionDomibus(t, base)
	provider := newProvider(base)

	// A dedicated recipient so Fetch's finalRecipient filter only ever sees (and
	// consumes) this test's own message. A per-run marker in the body lets us
	// distinguish it from any leftover.
	recipient := qerdsprovider.Address("inbound-it@qerds.localhost")
	body := fmt.Sprintf("inbound round-trip %d", time.Now().UnixNano())
	subject := fmt.Sprintf("subject %d", time.Now().UnixNano())

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	sendAwaitingPMode(ctx, t, provider, qerdsprovider.OutboundMessage{
		Sender:    "sender@qerds.localhost",
		Recipient: recipient,
		Subject:   subject,
		Body:      body,
	})

	// AS4 delivery is async; poll Fetch until the message lands (or time out).
	var got *qerdsprovider.InboundMessage
	for got == nil && ctx.Err() == nil {
		msgs, err := provider.Fetch(ctx, recipient)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		for i := range msgs {
			if msgs[i].Body == body {
				got = &msgs[i]
				break
			}
		}
		if got == nil {
			time.Sleep(2 * time.Second)
		}
	}
	if got == nil {
		t.Fatal("inbound message was not delivered/retrievable within the timeout")
	}
	if got.Recipient != recipient {
		t.Errorf("inbound recipient = %q, want %q", got.Recipient, recipient)
	}
	if got.Subject != subject {
		t.Errorf("inbound subject = %q, want %q (subject must round-trip)", got.Subject, subject)
	}
	if got.ProviderRef == "" {
		t.Error("inbound message has an empty ProviderRef")
	}
}

// provisionDomibus makes a fresh Domibus usable end to end: upload the PMode and
// persist a message filter routing inbound to the WS plugin. Both are idempotent,
// so tests can call it unconditionally. In dev the compose `domibus-provision`
// service does the same; in CI the Domibus service container starts unprovisioned,
// so the test must do it.
func provisionDomibus(t *testing.T, base string) {
	t.Helper()
	client, xsrf := domibusLogin(t, base)
	uploadPMode(t, base, client, xsrf)
	configureMessageFilter(t, base, client, xsrf)
}

// domibusLogin authenticates to the admin REST API and returns a cookie-jar
// client plus the XSRF-TOKEN value (Domibus uses CSRF double-submit: the token
// cookie set at login must be echoed in the X-XSRF-TOKEN header on writes).
func domibusLogin(t *testing.T, base string) (*http.Client, string) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar, Timeout: 30 * time.Second}

	loginBody, err := json.Marshal(map[string]string{
		"username": envOrDefault(envDomibusUser, defaultDomibusUser),
		"password": envOrDefault(envDomibusPassword, defaultDomibusPassword),
	})
	if err != nil {
		t.Fatalf("marshal login: %v", err)
	}
	resp, err := client.Post(base+"/rest/security/authentication", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticate: status %d", resp.StatusCode)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	for _, c := range jar.Cookies(u) {
		if c.Name == "XSRF-TOKEN" {
			return client, c.Value
		}
	}
	t.Fatalf("no XSRF-TOKEN cookie set by Domibus at %s", base)
	return nil, ""
}

// uploadPMode uploads the embedded PMode. Idempotent: Domibus replaces the
// current PMode on each upload.
func uploadPMode(t *testing.T, base string, client *http.Client, xsrf string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "pmode.xml")
	if err != nil {
		t.Fatalf("multipart: %v", err)
	}
	if _, err := part.Write(pmodeXML); err != nil {
		t.Fatalf("write pmode: %v", err)
	}
	if err := w.WriteField("description", "qerds integration-test blue->red self-send"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, base+"/rest/pmode", &body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-XSRF-TOKEN", xsrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload pmode: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload pmode: status %d", resp.StatusCode)
	}
}

// configureMessageFilter persists a message filter with the WS plugin first (no
// routing criteria => it catches everything). The image ships WS + JMS + FS
// plugins with no persisted filter, so an inbound message otherwise matches no
// backend and is dropped to notification.unknown — retrieveMessage never sees it.
func configureMessageFilter(t *testing.T, base string, client *http.Client, xsrf string) {
	t.Helper()
	const filters = `[` +
		`{"entityId":0,"index":0,"routingCriterias":[],"backendName":"backendWebservice","persisted":true},` +
		`{"entityId":0,"index":1,"routingCriterias":[],"backendName":"backendFSPlugin","persisted":true},` +
		`{"entityId":0,"index":2,"routingCriterias":[],"backendName":"Jms","persisted":true}]`
	req, err := http.NewRequest(http.MethodPut, base+"/rest/messagefilters", strings.NewReader(filters))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-XSRF-TOKEN", xsrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("set message filter: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set message filter: status %d", resp.StatusCode)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
