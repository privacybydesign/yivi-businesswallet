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

// Env knobs. QERDS_TEST_DOMIBUS_URL gates the whole test (unset => skip, like
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

// TestDomibusProviderSendIntegration exercises the real DomibusProvider against a
// live Domibus AS4 access point: it uploads the PMode (idempotent), pings the WS
// plugin, and submits a message, asserting Domibus accepts it and returns a
// provider reference. This is the round-trip the offline domibus_test.go cannot
// cover — envelope marshalling is unit-tested there; here the access point
// actually parses the envelope, matches it to the PMode, and enqueues it.
func TestDomibusProviderSendIntegration(t *testing.T) {
	base := strings.TrimRight(os.Getenv(envDomibusURL), "/")
	if base == "" {
		t.Skipf("%s not set; skipping live Domibus integration test", envDomibusURL)
	}

	if err := uploadPMode(t, base, pmodeXML); err != nil {
		t.Fatalf("provision PMode: %v", err)
	}

	provider := qerdsprovider.NewDomibusProvider(
		base+"/services/backend",
		qerdsprovider.NewTokenAuthenticator(""),
		// Matches the QERDS_DOMIBUS_* config defaults and the parties/service/
		// action in testdata/pmode.xml.
		qerdsprovider.DomibusConfig{
			FromParty:   "domibus-blue",
			ToParty:     "domibus-red",
			PartyType:   "urn:oasis:names:tc:ebcore:partyid-type:unregistered",
			Service:     "bdx:noprocess",
			ServiceType: "tc1",
			Action:      "TC1Leg1",
		},
		&http.Client{Timeout: 30 * time.Second},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := provider.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	receipt, err := provider.Send(ctx, qerdsprovider.OutboundMessage{
		Sender:    "sender@qerds.localhost",
		Recipient: "recipient@qerds.localhost",
		Subject:   "integration test",
		Body:      "hello from the QERDS Domibus integration test",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if receipt.ProviderRef == "" {
		t.Error("Send returned an empty ProviderRef; expected the Domibus message id")
	}
	if receipt.Status != qerdsprovider.StatusSubmitted {
		t.Errorf("Send status = %q, want %q", receipt.Status, qerdsprovider.StatusSubmitted)
	}
}

// uploadPMode logs into the Domibus admin REST API and uploads the PMode. It is
// idempotent: Domibus replaces the current PMode on each upload, so re-running
// the test is safe. Domibus uses CSRF double-submit — the XSRF-TOKEN cookie set
// at login must be echoed in the X-XSRF-TOKEN header on the upload.
func uploadPMode(t *testing.T, base string, pmode []byte) error {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	client := &http.Client{Jar: jar, Timeout: 30 * time.Second}

	user := envOrDefault(envDomibusUser, defaultDomibusUser)
	password := envOrDefault(envDomibusPassword, defaultDomibusPassword)

	loginBody, err := json.Marshal(map[string]string{"username": user, "password": password})
	if err != nil {
		return err
	}
	loginResp, err := client.Post(base+"/rest/security/authentication", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}
	_ = loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		return fmt.Errorf("authenticate: status %d", loginResp.StatusCode)
	}

	xsrf, err := xsrfToken(jar, base)
	if err != nil {
		return err
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "pmode.xml")
	if err != nil {
		return err
	}
	if _, err := part.Write(pmode); err != nil {
		return err
	}
	if err := w.WriteField("description", "qerds integration-test blue->red self-send"); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, base+"/rest/pmode", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-XSRF-TOKEN", xsrf)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upload: status %d", resp.StatusCode)
	}
	return nil
}

// xsrfToken returns the value of the XSRF-TOKEN cookie Domibus set for base.
func xsrfToken(jar *cookiejar.Jar, base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	for _, c := range jar.Cookies(u) {
		if c.Name == "XSRF-TOKEN" {
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("no XSRF-TOKEN cookie set by Domibus at %s", base)
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
