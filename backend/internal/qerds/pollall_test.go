package qerds

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
)

// multiOrgStore is a fakeStore variant holding several orgs' addresses, so
// PollAll's deployment-wide sweep and its per-message attribution can be
// exercised without a database.
type multiOrgStore struct {
	*fakeStore
	addresses []Address
}

func newMultiOrgStore(addrs ...Address) *multiOrgStore {
	return &multiOrgStore{fakeStore: newFakeStore(""), addresses: addrs}
}

func (m *multiOrgStore) AllAddresses(_ context.Context) ([]Address, error) {
	return m.addresses, nil
}

func (m *multiOrgStore) OrgByAddress(_ context.Context, address string) (uuid.UUID, error) {
	for _, a := range m.addresses {
		if a.Address == address {
			return a.OrganizationID, nil
		}
	}
	return uuid.Nil, ErrAddressNotFound
}

// scriptedProvider returns canned inbound per polled address and can fail for one.
type scriptedProvider struct {
	inbound  map[string][]qerdsprovider.InboundMessage
	failFor  string
	fetched  []string
	drained  map[string]bool
	sendCall int
}

func (p *scriptedProvider) Send(context.Context, qerdsprovider.OutboundMessage) (qerdsprovider.SendReceipt, error) {
	p.sendCall++
	return qerdsprovider.SendReceipt{}, nil
}

func (p *scriptedProvider) ResolveAddress(_ context.Context, id string) (qerdsprovider.Address, error) {
	return qerdsprovider.Address(id), nil
}

func (p *scriptedProvider) Fetch(_ context.Context, addr qerdsprovider.Address) ([]qerdsprovider.InboundMessage, error) {
	a := string(addr)
	p.fetched = append(p.fetched, a)
	if a == p.failFor {
		return nil, errors.New("access point unreachable")
	}
	if p.drained == nil {
		p.drained = map[string]bool{}
	}
	if p.drained[a] {
		return nil, nil // retrieveMessage consumes; a second poll sees nothing
	}
	p.drained[a] = true
	return p.inbound[a], nil
}

// recordingConsumer captures what the inbound consumer was told.
type recordingConsumer struct {
	calls []struct {
		orgID  uuid.UUID
		sender string
		body   string
	}
}

func (c *recordingConsumer) OnInboundMessage(_ context.Context, orgID, _ uuid.UUID, sender, _, body string) error {
	c.calls = append(c.calls, struct {
		orgID  uuid.UUID
		sender string
		body   string
	}{orgID, sender, body})
	return nil
}

func TestPollAllSweepsEveryOrgAndThreadsSender(t *testing.T) {
	orgA, orgB := uuid.New(), uuid.New()
	store := newMultiOrgStore(
		Address{ID: uuid.New(), OrganizationID: orgA, Address: "acme@qerds.localhost", IsDefault: true},
		Address{ID: uuid.New(), OrganizationID: orgB, Address: "globex@qerds.localhost", IsDefault: true},
	)
	prov := &scriptedProvider{inbound: map[string][]qerdsprovider.InboundMessage{
		"acme@qerds.localhost": {{
			ProviderRef: "ref-acme", Sender: "verid@partners.test",
			Recipient: "acme@qerds.localhost", Body: "offer for acme",
		}},
		"globex@qerds.localhost": {{
			ProviderRef: "ref-globex", Sender: "verid@partners.test",
			Recipient: "globex@qerds.localhost", Body: "offer for globex",
		}},
	}}
	consumer := &recordingConsumer{}
	svc := NewService(store, store, prov)
	svc.SetInboundConsumer(consumer)

	got, err := svc.PollAll(context.Background())
	if err != nil {
		t.Fatalf("PollAll: %v", err)
	}
	if got != 2 {
		t.Fatalf("received = %d, want 2 (one per org)", got)
	}
	if len(prov.fetched) != 2 {
		t.Fatalf("fetched %v, want one Fetch per provisioned address", prov.fetched)
	}
	if len(consumer.calls) != 2 {
		t.Fatalf("consumer calls = %d, want 2", len(consumer.calls))
	}

	// Each org must get its own message, and the sender must reach the consumer —
	// the trusted-sender gate is useless if it always sees "".
	byOrg := map[uuid.UUID]string{}
	for _, c := range consumer.calls {
		byOrg[c.orgID] = c.body
		if c.sender != "verid@partners.test" {
			t.Errorf("consumer sender = %q, want the originalSender", c.sender)
		}
	}
	if byOrg[orgA] != "offer for acme" || byOrg[orgB] != "offer for globex" {
		t.Errorf("messages misattributed across orgs: %v", byOrg)
	}

	// Idempotent: the queue is drained, so a second sweep stores nothing new.
	again, err := svc.PollAll(context.Background())
	if err != nil {
		t.Fatalf("second PollAll: %v", err)
	}
	if again != 0 {
		t.Errorf("second sweep received = %d, want 0", again)
	}
}

// One unreachable address must not stop the sweep: otherwise a single org's
// broken address silently stops every other org from receiving.
func TestPollAllContinuesPastAFailingAddress(t *testing.T) {
	orgA, orgB := uuid.New(), uuid.New()
	store := newMultiOrgStore(
		Address{ID: uuid.New(), OrganizationID: orgA, Address: "broken@qerds.localhost"},
		Address{ID: uuid.New(), OrganizationID: orgB, Address: "globex@qerds.localhost"},
	)
	prov := &scriptedProvider{
		failFor: "broken@qerds.localhost",
		inbound: map[string][]qerdsprovider.InboundMessage{
			"globex@qerds.localhost": {{
				ProviderRef: "ref-globex", Sender: "verid@partners.test",
				Recipient: "globex@qerds.localhost", Body: "offer for globex",
			}},
		},
	}
	svc := NewService(store, store, prov)

	got, err := svc.PollAll(context.Background())
	if err == nil {
		t.Error("expected the failing address to be reported in the returned error")
	}
	if got != 1 {
		t.Fatalf("received = %d, want 1 — the healthy address must still be drained", got)
	}
	if len(prov.fetched) != 2 {
		t.Errorf("fetched %v, want the sweep to continue past the failure", prov.fetched)
	}
}

// The WS-plugin queue is shared across the access point, so a provider may hand
// back a message whose finalRecipient is a different org. It must be filed under
// the org that owns that address, not the one we happened to be polling.
func TestPollAllAttributesByFinalRecipient(t *testing.T) {
	orgA, orgB := uuid.New(), uuid.New()
	store := newMultiOrgStore(
		Address{ID: uuid.New(), OrganizationID: orgA, Address: "acme@qerds.localhost"},
		Address{ID: uuid.New(), OrganizationID: orgB, Address: "globex@qerds.localhost"},
	)
	prov := &scriptedProvider{inbound: map[string][]qerdsprovider.InboundMessage{
		// Polling acme's address yields a message addressed to globex.
		"acme@qerds.localhost": {{
			ProviderRef: "ref-strayed", Sender: "verid@partners.test",
			Recipient: "globex@qerds.localhost", Body: "offer for globex",
		}},
	}}
	consumer := &recordingConsumer{}
	svc := NewService(store, store, prov)
	svc.SetInboundConsumer(consumer)

	if _, err := svc.PollAll(context.Background()); err != nil {
		t.Fatalf("PollAll: %v", err)
	}
	if len(consumer.calls) != 1 {
		t.Fatalf("consumer calls = %d, want 1", len(consumer.calls))
	}
	if consumer.calls[0].orgID != orgB {
		t.Errorf("message filed under the polled org, not its finalRecipient owner")
	}
}

// A message for an address no org owns must be skipped, not filed under the
// polling org — that would hand one org another party's credential offer.
func TestPollAllSkipsUnknownRecipient(t *testing.T) {
	orgA := uuid.New()
	store := newMultiOrgStore(
		Address{ID: uuid.New(), OrganizationID: orgA, Address: "acme@qerds.localhost"},
	)
	prov := &scriptedProvider{inbound: map[string][]qerdsprovider.InboundMessage{
		"acme@qerds.localhost": {{
			ProviderRef: "ref-unknown", Sender: "verid@partners.test",
			Recipient: "nobody@qerds.localhost", Body: "offer for nobody",
		}},
	}}
	consumer := &recordingConsumer{}
	svc := NewService(store, store, prov)
	svc.SetInboundConsumer(consumer)

	got, err := svc.PollAll(context.Background())
	if err != nil {
		t.Fatalf("PollAll: %v", err)
	}
	if got != 0 {
		t.Errorf("received = %d, want 0 for an unowned recipient", got)
	}
	if len(consumer.calls) != 0 {
		t.Errorf("consumer was notified for an unowned recipient")
	}
}
