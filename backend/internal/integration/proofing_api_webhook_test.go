//go:build integration

package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofing"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/safehttp"
)

// receivedHook is one webhook delivery as the org's endpoint got it.
type receivedHook struct {
	event, delivery, signature string
	body                       []byte
}

// verifiedHookBody is a delivery's body as a receiver reads it.
type verifiedHookBody struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		SessionID string          `json:"sessionId"`
		Status    string          `json:"status"`
		Result    json.RawMessage `json:"result"`
	} `json:"data"`
}

// A customer's whole round trip through the public API: the key lists the
// flows, creates a session and reads it settled, and the endpoint receives
// session.created and session.verified, signed with the endpoint's secret,
// the verified one carrying the same result GET /result answers.
func TestProofingAPIRoundTripWithWebhook(t *testing.T) {
	env := setup(t)
	env.proofing.Outcome = proofingprovider.StatusApproved
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	flow := env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "Onboarding platform")

	hooks := make(chan receivedHook, 10)
	receiver := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		hooks <- receivedHook{r.Header.Get(proofing.EventHeader), r.Header.Get(proofing.DeliveryHeader), r.Header.Get(proofing.SignatureHeader), body}
	}))
	t.Cleanup(receiver.Close)
	// The route takes only a public https endpoint; the test then points it at
	// its own receiver, which the deliverer below may reach.
	resp := env.putJSON("/api/v1/orgs/acme/identity-proofing/webhook",
		map[string]any{"url": "https://hooks.example.org/yivi", "events": []string{"session.created", "session.verified"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set webhook = %d, want 200", resp.StatusCode)
	}
	secret := decodeJSON[proofingWebhookResp](t, resp).Secret
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE identity_proofing_webhooks SET url = $1 WHERE organization_id = $2`, receiver.URL, orgID); err != nil {
		t.Fatalf("point the webhook at the receiver: %v", err)
	}

	flows := decodeJSON[[]struct {
		ID string `json:"id"`
	}](t, env.apiCall(http.MethodGet, "/api/v1/proofing/flows", key.Secret, nil))
	if len(flows) != 1 || flows[0].ID != flow.ID {
		t.Fatalf("flows = %+v, want the org's flow", flows)
	}
	session := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "anna@example.org", "flowId": flow.ID, "sendMail": false}))
	if read := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+session.ID,
		key.Secret, nil)); read.Status != "approved" {
		t.Fatalf("session = %+v, want approved", read)
	}
	resp = env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+session.ID+"/result", key.Secret, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("result = %d, want 200", resp.StatusCode)
	}
	apiResult, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	deliverer := proofing.NewDeliverer(env.webhooks, safehttp.Policy{AllowInsecureHTTP: true})
	deliverer.SetResults(env.proofingService)
	if n, err := deliverer.DeliverDue(context.Background()); err != nil || n != 2 {
		t.Fatalf("DeliverDue = %d, %v; want session.created and session.verified", n, err)
	}
	got := map[string]receivedHook{}
	for range 2 {
		hook := <-hooks
		got[hook.event] = hook
		if !validHookSignature(hook.signature, hook.body, secret) {
			t.Errorf("%s: signature %q does not verify under the endpoint's secret", hook.event, hook.signature)
		}
	}
	if created := got["session.created"]; strings.Contains(string(created.body), `"result"`) {
		t.Errorf("session.created carries a result: %s", created.body)
	}

	verified := got["session.verified"]
	var body verifiedHookBody
	if err := json.Unmarshal(verified.body, &body); err != nil {
		t.Fatalf("session.verified body: %v", err)
	}
	if body.ID != verified.delivery || body.Data.SessionID != session.ID || body.Data.Status != "approved" {
		t.Errorf("session.verified = %+v; want the approved session under its delivery id", body)
	}
	var fromHook, fromAPI map[string]any
	if err := json.Unmarshal(body.Data.Result, &fromHook); err != nil {
		t.Fatalf("session.verified result: %v (%s)", err, body.Data.Result)
	}
	if err := json.Unmarshal(apiResult, &fromAPI); err != nil {
		t.Fatalf("API result: %v", err)
	}
	for _, field := range []string{"id", "status", "identity", "evidence", "photo"} {
		if fromHook[field] == nil || !sameJSON(fromHook[field], fromAPI[field]) {
			t.Errorf("result %s: webhook %v, API %v; want the same, set", field, fromHook[field], fromAPI[field])
		}
	}
	if fromHook["selfie"] != nil || fromAPI["selfie"] != nil {
		t.Error("a result carries the selfie")
	}
}

// validHookSignature checks Yivi-Signature as the docs tell a receiver to:
// HMAC-SHA256 of "<t>.<body>" under the secret, in any v1.
func validHookSignature(header string, body []byte, secret string) bool {
	var ts string
	var signatures []string
	for part := range strings.SplitSeq(header, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			signatures = append(signatures, v)
		}
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + string(body)))
	want := hex.EncodeToString(mac.Sum(nil))
	for _, s := range signatures {
		if hmac.Equal([]byte(s), []byte(want)) {
			return ts != ""
		}
	}
	return false
}

func sameJSON(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ja) == string(jb)
}
