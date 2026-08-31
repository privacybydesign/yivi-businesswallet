//go:build integration

package integration

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
)

type offerResp struct {
	ID             string `json:"id"`
	SenderOrgName  string `json:"senderOrgName"`
	CredentialName string `json:"credentialName"`
	Status         string `json:"status"`
	// Not part of the API. Decoded anyway so the test can assert the deeplink is
	// absent from the wire, not merely absent from the Go struct.
	Offer string `json:"offer"`
}

type heldResp struct {
	ID     string `json:"id"`
	VCT    string `json:"vct"`
	Source string `json:"source"`
}

// deliverOffer plays the inbound QERDS side: it stores a received message for the
// org and runs the real OfferReceiver over it, which is what the poller does.
// Returns nothing — the offer is now in the org's queue, or it is not, and the
// HTTP assertions are what say which.
func deliverOffer(t *testing.T, env *testEnv, orgID uuid.UUID, providerRef, credentialName string) {
	t.Helper()
	ctx := context.Background()

	body, err := attestation.MarshalCredentialOfferEnvelope("Ver.ID", credentialName,
		"openid-credential-offer://?credential_offer=%7B%22x%22%3A1%7D")
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	var messageID uuid.UUID
	const insert = `INSERT INTO qerds_messages
		(organization_id, direction, sender_address, recipient_address, subject, body, provider_ref, status)
		VALUES ($1, 'inbound', 'verid@ver.id', 'acme@qerds.localhost', 'Credential offer', $2, $3, 'received')
		RETURNING id`
	if err := env.pool.QueryRow(ctx, insert, orgID, body, providerRef).Scan(&messageID); err != nil {
		t.Fatalf("store inbound message: %v", err)
	}

	receiver := attestation.NewOfferReceiver(
		attestation.NewStore(env.pool, audit.NewDBRecorder()),
		attestation.NewTrustedOfferSenders(nil, nil),
	)
	if err := receiver.OnInboundMessage(ctx, qerds.Inbound{
		OrgID:     orgID,
		MessageID: messageID,
		FromParty: "verid-qerds",
		Sender:    "verid@ver.id",
		Subject:   "Credential offer",
		Body:      body,
	}); err != nil {
		t.Fatalf("receive offer: %v", err)
	}
}

// A credential offer delivered over QERDS must wait for a human: it shows up as
// awaiting a decision and the wallet stays empty until an admin accepts (#229).
func TestCredentialOfferRequiresAcceptanceHTTPFlow(t *testing.T) {
	env := setup(t)
	admin := env.login("admin@acme.test")
	orgID := env.createOrg("Acme", "acme")
	env.addMembership(admin.ID, orgID, organization.RoleAdmin)

	deliverOffer(t, env, orgID, "ref-accept", "Bewijs van inschrijving")

	// Nothing in the wallet yet — this is the whole point of the change.
	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/attestations/held", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list held = %d, want 200", resp.StatusCode)
	}
	if held := decodeJSON[[]heldResp](t, resp); len(held) != 0 {
		t.Fatalf("wallet holds %d credentials before anyone accepted", len(held))
	}

	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/attestations/offers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list offers = %d, want 200", resp.StatusCode)
	}
	offers := decodeJSON[[]offerResp](t, resp)
	if len(offers) != 1 {
		t.Fatalf("offers awaiting a decision = %d, want 1", len(offers))
	}
	if offers[0].Status != attestation.OfferPending {
		t.Errorf("offer status = %q, want %q", offers[0].Status, attestation.OfferPending)
	}
	if offers[0].SenderOrgName != "Ver.ID" || offers[0].CredentialName != "Bewijs van inschrijving" {
		t.Errorf("the offer does not name what was offered: %+v", offers[0])
	}
	// The deeplink is a bearer token: it must not travel to the console.
	if offers[0].Offer != "" {
		t.Errorf("the offer deeplink was served to the client: %q", offers[0].Offer)
	}

	resp = env.postJSON("/api/v1/orgs/acme/attestations/offers/"+offers[0].ID+"/accept", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accept offer = %d, want 200", resp.StatusCode)
	}
	if accepted := decodeJSON[heldResp](t, resp); accepted.Source != attestation.HeldSourceQERDS {
		t.Errorf("accepted credential source = %q, want %q", accepted.Source, attestation.HeldSourceQERDS)
	}

	// Now it is in the wallet, and no longer awaiting a decision.
	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/attestations/held", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list held after accept = %d, want 200", resp.StatusCode)
	}
	if held := decodeJSON[[]heldResp](t, resp); len(held) != 1 {
		t.Fatalf("wallet holds %d credentials after accepting, want 1", len(held))
	}
	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/attestations/offers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list offers after accept = %d, want 200", resp.StatusCode)
	}
	if remaining := decodeJSON[[]offerResp](t, resp); len(remaining) != 0 {
		t.Fatalf("accepted offer is still awaiting a decision (%d)", len(remaining))
	}

	// Accepting it twice must not hold it twice.
	resp = env.postJSON("/api/v1/orgs/acme/attestations/offers/"+offers[0].ID+"/accept", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("re-accept = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestCredentialOfferDeclineHTTPFlow(t *testing.T) {
	env := setup(t)
	admin := env.login("admin@acme.test")
	orgID := env.createOrg("Acme", "acme")
	env.addMembership(admin.ID, orgID, organization.RoleAdmin)

	deliverOffer(t, env, orgID, "ref-decline", "Bewijs van inschrijving")

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/attestations/offers", nil)
	offers := decodeJSON[[]offerResp](t, resp)
	if len(offers) != 1 {
		t.Fatalf("offers awaiting a decision = %d, want 1", len(offers))
	}

	resp = env.postJSON("/api/v1/orgs/acme/attestations/offers/"+offers[0].ID+"/decline", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("decline offer = %d, want 204", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/attestations/held", nil)
	if held := decodeJSON[[]heldResp](t, resp); len(held) != 0 {
		t.Fatalf("declining put %d credentials in the wallet", len(held))
	}
	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/attestations/offers", nil)
	if remaining := decodeJSON[[]offerResp](t, resp); len(remaining) != 0 {
		t.Fatalf("declined offer is still awaiting a decision (%d)", len(remaining))
	}

	// A declined offer cannot be accepted after the fact.
	resp = env.postJSON("/api/v1/orgs/acme/attestations/offers/"+offers[0].ID+"/accept", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("accept after decline = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// A member sees what the organization has been offered but cannot decide for it —
// accepting writes a credential into the org wallet, which is an admin action.
func TestCredentialOfferMemberCannotDecide(t *testing.T) {
	env := setup(t)
	member := env.login("member@acme.test")
	orgID := env.createOrg("Acme", "acme")
	env.addMembership(member.ID, orgID, organization.RoleMember)

	deliverOffer(t, env, orgID, "ref-member", "Bewijs van inschrijving")

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/attestations/offers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("member list offers = %d, want 200", resp.StatusCode)
	}
	offers := decodeJSON[[]offerResp](t, resp)
	if len(offers) != 1 {
		t.Fatalf("member sees %d offers, want 1", len(offers))
	}

	for _, action := range []string{"accept", "decline"} {
		resp = env.postJSON("/api/v1/orgs/acme/attestations/offers/"+offers[0].ID+"/"+action, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("member %s = %d, want 403", action, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/attestations/held", nil)
	if held := decodeJSON[[]heldResp](t, resp); len(held) != 0 {
		t.Fatalf("a member's rejected decision still put %d credentials in the wallet", len(held))
	}
}

// Two admins pressing Accept on the same offer at the same moment — or one admin
// whose double-click reaches two API replicas — get one 200 and the rest 404, and
// the organization ends up holding one credential.
//
// This pins the contract through the assembled router; it is not where the race
// itself is caught. The damage a missing claim does is a redemption whose
// credential never reaches held_attestations, and an orphan in the holder engine
// is invisible from here — /held lists the index, so it reads as 1 either way.
// The redeem count is asserted where the engine is observable:
// TestConcurrentAcceptsRedeemTheOfferOnce (service) and TestClaimOfferIsTakenOnce
// (store) are the regression tests, and both fail without the claim.
func TestConcurrentOfferAcceptsHoldOneCredential(t *testing.T) {
	env := setup(t)
	admin := env.login("admin@acme.test")
	orgID := env.createOrg("Acme", "acme")
	env.addMembership(admin.ID, orgID, organization.RoleAdmin)

	deliverOffer(t, env, orgID, "ref-concurrent", "Bewijs van inschrijving")

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/attestations/offers", nil)
	offers := decodeJSON[[]offerResp](t, resp)
	if len(offers) != 1 {
		t.Fatalf("offers awaiting a decision = %d, want 1", len(offers))
	}
	accept := env.server.URL + "/api/v1/orgs/acme/attestations/offers/" + offers[0].ID + "/accept"

	// The requests are fired from goroutines, so they collect their outcome
	// instead of failing there: t.Fatalf off the test goroutine is not allowed.
	const requests = 4
	var (
		start  sync.WaitGroup
		done   sync.WaitGroup
		mu     sync.Mutex
		status []int
		errs   []error
	)
	start.Add(1)
	for range requests {
		done.Add(1)
		go func() {
			defer done.Done()
			req, err := http.NewRequest(http.MethodPost, accept, nil)
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			start.Wait()
			resp, err := env.client.Do(req)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			_ = resp.Body.Close()
			status = append(status, resp.StatusCode)
		}()
	}
	start.Done()
	done.Wait()

	for _, err := range errs {
		t.Fatalf("accept request: %v", err)
	}
	var ok, notFound int
	for _, code := range status {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusNotFound:
			notFound++
		default:
			t.Fatalf("concurrent accept = %d, want 200 or 404", code)
		}
	}
	if ok != 1 {
		t.Fatalf("%d of %d concurrent accepts returned 200, want exactly 1", ok, requests)
	}
	if notFound != requests-1 {
		t.Fatalf("%d accepts returned 404, want %d", notFound, requests-1)
	}

	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/attestations/held", nil)
	if held := decodeJSON[[]heldResp](t, resp); len(held) != 1 {
		t.Fatalf("wallet holds %d credentials after %d concurrent accepts, want 1", len(held), requests)
	}
	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/attestations/offers", nil)
	if remaining := decodeJSON[[]offerResp](t, resp); len(remaining) != 0 {
		t.Fatalf("the offer is still awaiting a decision (%d)", len(remaining))
	}
}
