//go:build integration

package qerds_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// This is the wallet-side half of the two-gateway QERDS bench: a credential
// offer submitted by a FOREIGN AS4 party (ver.id's own access point, signing with
// its own key) is polled off our access point, attributed to the right
// organization, gated by the trusted-sender allowlist, and redeemed.
//
// The single-gateway loopback cannot cover this. It signs with our own key, and
// Domibus refuses a submission whose From party is not the submitting gateway's
// own — so "do we accept a message from someone else" is only answerable with two
// real gateways.
//
// Bring the bench up first:
//
//	docker compose --profile domibus --profile verid up -d --wait
//	docker compose --profile domibus --profile verid up \
//	  domibus-provision domibus-provision-verid domibus-verid-provision
//
// then run with both gateway URLs set:
//
//	QERDS_TEST_DOMIBUS_URL=http://localhost:8090/domibus \
//	QERDS_TEST_VERID_DOMIBUS_URL=http://localhost:8091/domibus \
//	TEST_DATABASE_URL=... go test -tags=integration ./internal/qerds/ -run Verid
const (
	envOurDomibusURL   = "QERDS_TEST_DOMIBUS_URL"
	envVeridDomibusURL = "QERDS_TEST_VERID_DOMIBUS_URL"

	// ebMS3 addressing from docker/development/domibus/pmode-verid.xml.
	veridPartyID  = "verid"
	ourPartyID    = "domibus-blue"
	benchPartyURN = "urn:oasis:names:tc:ebcore:partyid-type:unregistered"

	veridSenderAddress = "verid@partners.qerds.localhost"
)

func veridBenchURLs(t *testing.T) (ours, verid string) {
	t.Helper()
	ours = strings.TrimRight(os.Getenv(envOurDomibusURL), "/")
	verid = strings.TrimRight(os.Getenv(envVeridDomibusURL), "/")
	if ours == "" || verid == "" {
		t.Skipf("%s and %s must both be set (two-gateway bench); skipping",
			envOurDomibusURL, envVeridDomibusURL)
	}
	return ours, verid
}

func benchProvider(base, fromParty, toParty string) *qerdsprovider.DomibusProvider {
	return qerdsprovider.NewDomibusProvider(
		base+"/services/backend",
		qerdsprovider.NewTokenAuthenticator(""),
		qerdsprovider.DomibusConfig{
			FromParty:   fromParty,
			ToParty:     toParty,
			PartyType:   benchPartyURN,
			Service:     "bdx:noprocess",
			ServiceType: "tc1",
			Action:      "TC1Leg1",
		},
		&http.Client{Timeout: 30 * time.Second},
	)
}

// countingConsumer records what the inbound consumer saw, and wraps the real
// OfferReceiver so the allowlist decision is the production one.
type countingConsumer struct {
	inner qerds.InboundConsumer
	seen  []string // senders, in arrival order
}

func (c *countingConsumer) OnInboundMessage(ctx context.Context, orgID, messageID uuid.UUID, sender, subject, body string) error {
	c.seen = append(c.seen, sender)
	return c.inner.OnInboundMessage(ctx, orgID, messageID, sender, subject, body)
}

func TestVeridInboundOfferIsPolledAndRedeemed(t *testing.T) {
	ourURL, veridURL := veridBenchURLs(t)
	pool, _ := testdb.Fresh(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// An organization that owns the address ver.id will target.
	const slug = "acme"
	orgAddress := slug + "@qerds.localhost"
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Acme', $1, 'kvk-acme', 'NL.KVK.acme', $2)`, slug, orgAddress); err != nil {
		t.Fatalf("create org: %v", err)
	}
	org, err := organization.NewStore(pool, audit.NopRecorder{}).GetBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("get org: %v", err)
	}

	store := qerds.NewStore(pool, audit.NopRecorder{})
	if _, err := store.ProvisionAddress(ctx, org.ID, orgAddress, true, ""); err != nil {
		t.Fatalf("provision address: %v", err)
	}

	// The wallet side: our gateway, plus the real inbound consumer chain.
	ourProvider := benchProvider(ourURL, ourPartyID, "domibus-red")
	svc := qerds.NewService(store, store, ourProvider)

	attStore := attestation.NewStore(pool, audit.NopRecorder{})
	// eudiholder.StubHolder stands in for the irmago holder: this test is about
	// the QERDS receive path and the allowlist, not irmago's OpenID4VCI client.
	holder := attestation.NewOfferReceiver(
		eudiholder.NewStubHolder(), attStore,
		attestation.NewTrustedOfferSenders([]string{veridSenderAddress}),
	)
	consumer := &countingConsumer{inner: holder}
	svc.SetInboundConsumer(consumer)

	// ver.id's side: submit through THEIR gateway, From=verid.
	offer := "openid-credential-offer://?credential_offer=%7B%22credential_issuer%22%3A%22https%3A%2F%2Fissuer.ver.id%22%7D"
	body, err := attestation.MarshalCredentialOfferEnvelope("Ver.ID", "Bewijs van inschrijving", offer)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	veridProvider := benchProvider(veridURL, veridPartyID, ourPartyID)
	receipt, err := veridProvider.Send(ctx, qerdsprovider.OutboundMessage{
		Sender:    veridSenderAddress,
		Recipient: qerdsprovider.Address(orgAddress),
		Subject:   "Credential offer: Bewijs van inschrijving",
		Body:      body,
	})
	if err != nil {
		t.Fatalf("ver.id submit: %v", err)
	}
	t.Logf("ver.id submitted %s", receipt.ProviderRef)

	// The background poller's sweep. AS4 delivery is async, so retry.
	var received int
	for ctx.Err() == nil {
		n, err := svc.PollAll(ctx)
		if err != nil {
			t.Fatalf("PollAll: %v", err)
		}
		received += n
		if received > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if received == 0 {
		t.Fatal("PollAll never received ver.id's message")
	}

	// Attributed to the right org, with ver.id as the sender.
	messages, err := store.List(ctx, org.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	var inbound *qerds.Message
	for i := range messages {
		if messages[i].Direction == qerds.DirectionInbound {
			inbound = &messages[i]
			break
		}
	}
	if inbound == nil {
		t.Fatalf("no inbound message stored for org %s", org.ID)
	}
	if inbound.SenderAddress != veridSenderAddress {
		t.Errorf("sender = %q, want %q", inbound.SenderAddress, veridSenderAddress)
	}
	if inbound.RecipientAddress != orgAddress {
		t.Errorf("recipient = %q, want %q", inbound.RecipientAddress, orgAddress)
	}
	if _, ok := attestation.ParseCredentialOfferEnvelope(inbound.Body); !ok {
		t.Errorf("stored body is not a credential offer envelope: %q", inbound.Body)
	}

	// The consumer ran, and saw who sent it — the allowlist is only meaningful if
	// the sender actually reaches it.
	if len(consumer.seen) == 0 {
		t.Fatal("inbound consumer was never notified")
	}
	if consumer.seen[0] != veridSenderAddress {
		t.Errorf("consumer sender = %q, want %q", consumer.seen[0], veridSenderAddress)
	}

	// The offer was redeemed into the held index, linked to this message.
	held, err := attStore.ListHeld(ctx, org.ID)
	if err != nil {
		t.Fatalf("list held: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("held credentials = %d, want 1", len(held))
	}
	if held[0].Source != attestation.HeldSourceQERDS {
		t.Errorf("held source = %q, want %q", held[0].Source, attestation.HeldSourceQERDS)
	}
	t.Logf("redeemed: vct=%q source=%q", held[0].VCT, held[0].Source)
}

// The allowlist's whole point: an offer that arrives over a perfectly valid AS4
// leg, from a party our gateway trusts cryptographically, must still NOT be
// redeemed if that sender is not on the allowlist. The message is stored (it was
// legitimately delivered) but nothing is written into the wallet.
//
// This is the case content validation cannot catch: replay an offer at the wrong
// organization and the credential is authentic, correctly chained and unrevoked —
// it is simply in the wrong wallet. Only transport identity distinguishes it.
func TestVeridInboundOfferFromUntrustedSenderIsNotRedeemed(t *testing.T) {
	ourURL, veridURL := veridBenchURLs(t)
	pool, _ := testdb.Fresh(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	const slug = "globex"
	orgAddress := slug + "@qerds.localhost"
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Globex', $1, 'kvk-globex', 'NL.KVK.globex', $2)`, slug, orgAddress); err != nil {
		t.Fatalf("create org: %v", err)
	}
	org, err := organization.NewStore(pool, audit.NopRecorder{}).GetBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("get org: %v", err)
	}

	store := qerds.NewStore(pool, audit.NopRecorder{})
	if _, err := store.ProvisionAddress(ctx, org.ID, orgAddress, true, ""); err != nil {
		t.Fatalf("provision address: %v", err)
	}

	svc := qerds.NewService(store, store, benchProvider(ourURL, ourPartyID, "domibus-red"))
	attStore := attestation.NewStore(pool, audit.NopRecorder{})
	// An allowlist that does NOT include ver.id.
	svc.SetInboundConsumer(attestation.NewOfferReceiver(
		eudiholder.NewStubHolder(), attStore,
		attestation.NewTrustedOfferSenders([]string{"someone-else@partners.qerds.localhost"}),
	))

	offer := "openid-credential-offer://?credential_offer=%7B%22credential_issuer%22%3A%22https%3A%2F%2Fissuer.ver.id%22%7D"
	body, err := attestation.MarshalCredentialOfferEnvelope("Ver.ID", "Bewijs van inschrijving", offer)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if _, err := benchProvider(veridURL, veridPartyID, ourPartyID).Send(ctx, qerdsprovider.OutboundMessage{
		Sender:    veridSenderAddress,
		Recipient: qerdsprovider.Address(orgAddress),
		Subject:   "Credential offer: Bewijs van inschrijving",
		Body:      body,
	}); err != nil {
		t.Fatalf("ver.id submit: %v", err)
	}

	var received int
	for ctx.Err() == nil {
		n, err := svc.PollAll(ctx)
		if err != nil {
			t.Fatalf("PollAll: %v", err)
		}
		received += n
		if received > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if received == 0 {
		t.Fatal("PollAll never received the message")
	}

	// Delivered and stored: withholding redemption must not lose the message.
	messages, err := store.List(ctx, org.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	var found bool
	for _, m := range messages {
		if m.Direction == qerds.DirectionInbound && m.SenderAddress == veridSenderAddress {
			found = true
		}
	}
	if !found {
		t.Error("the message must still be stored in the inbox for an operator")
	}

	// But nothing entered the wallet.
	held, err := attStore.ListHeld(ctx, org.ID)
	if err != nil {
		t.Fatalf("list held: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("held credentials = %d, want 0 — an untrusted sender's offer was redeemed", len(held))
	}
}
