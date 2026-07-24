package qerds

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
)

// fakeProvider is a test-local QERDS provider: Send loops the message into the
// recipient's inbox so a Fetch on the receiving address returns it, exercising
// the whole send/receive/evidence flow without the deleted in-process stub or a
// real provider. It is a test double, not shipped code.
type fakeProvider struct {
	mu    sync.Mutex
	inbox map[qerdsprovider.Address][]qerdsprovider.InboundMessage
	seq   int
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{inbox: map[qerdsprovider.Address][]qerdsprovider.InboundMessage{}}
}

func (p *fakeProvider) ResolveAddress(_ context.Context, identifier string) (qerdsprovider.Address, error) {
	return qerdsprovider.Address(identifier), nil
}

func (p *fakeProvider) Send(_ context.Context, msg qerdsprovider.OutboundMessage) (qerdsprovider.SendReceipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.seq++
	ref := fmt.Sprintf("fake-%d", p.seq)
	delivery := qerdsprovider.Evidence{Type: qerdsprovider.EvidenceDelivery, ProviderRef: ref}
	p.inbox[msg.Recipient] = append(p.inbox[msg.Recipient], qerdsprovider.InboundMessage{
		ProviderRef: ref,
		Sender:      msg.Sender,
		Recipient:   msg.Recipient,
		Subject:     msg.Subject,
		Body:        msg.Body,
		Attachments: msg.Attachments,
		Evidence:    []qerdsprovider.Evidence{delivery},
	})
	return qerdsprovider.SendReceipt{
		ProviderRef: ref,
		Status:      qerdsprovider.StatusDelivered,
		Evidence: []qerdsprovider.Evidence{
			{Type: qerdsprovider.EvidenceSubmissionAcceptance, ProviderRef: ref},
			delivery,
		},
	}, nil
}

func (p *fakeProvider) Fetch(_ context.Context, addr qerdsprovider.Address) ([]qerdsprovider.InboundMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	msgs := p.inbox[addr]
	delete(p.inbox, addr)
	return msgs, nil
}

// fakeStore is an in-memory messageStore + addressStore for DB-free service tests.
type fakeStore struct {
	defaultAddr         Address
	orgIDs              []uuid.UUID
	messages            []Message
	sent                map[uuid.UUID]qerdsprovider.SendReceipt
	seenRefs            map[string]bool
	outboundAttachments []qerdsprovider.Attachment
	inboundAttachments  []qerdsprovider.Attachment
}

func newFakeStore(defaultAddress string) *fakeStore {
	f := &fakeStore{sent: map[uuid.UUID]qerdsprovider.SendReceipt{}, seenRefs: map[string]bool{}}
	if defaultAddress != "" {
		f.defaultAddr = Address{ID: uuid.New(), Address: defaultAddress, IsDefault: true}
	}
	return f
}

func (f *fakeStore) CreateOutbound(_ context.Context, orgID uuid.UUID, sender, recipient, subject, body string, attachments []qerdsprovider.Attachment) (Message, error) {
	f.outboundAttachments = attachments
	m := Message{
		ID:               uuid.New(),
		OrganizationID:   orgID,
		Direction:        DirectionOutbound,
		SenderAddress:    sender,
		RecipientAddress: recipient,
		Subject:          subject,
		Body:             body,
		Status:           StatusSubmitted,
	}
	f.messages = append(f.messages, m)
	return m, nil
}

func (f *fakeStore) RecordSent(_ context.Context, messageID uuid.UUID, receipt qerdsprovider.SendReceipt) error {
	f.sent[messageID] = receipt
	return nil
}

func (f *fakeStore) CreateInbound(_ context.Context, orgID uuid.UUID, in qerdsprovider.InboundMessage) (Message, bool, error) {
	if f.seenRefs[in.ProviderRef] {
		return Message{}, false, nil
	}
	f.seenRefs[in.ProviderRef] = true
	f.inboundAttachments = in.Attachments
	m := Message{
		ID:               uuid.New(),
		OrganizationID:   orgID,
		Direction:        DirectionInbound,
		RecipientAddress: string(in.Recipient),
		Subject:          in.Subject,
		ProviderRef:      in.ProviderRef,
		Status:           StatusReceived,
	}
	f.messages = append(f.messages, m)
	return m, true, nil
}

func (f *fakeStore) DefaultAddress(_ context.Context, _ uuid.UUID) (Address, error) {
	if f.defaultAddr.Address == "" {
		return Address{}, ErrNoSenderAddress
	}
	return f.defaultAddr, nil
}

func (f *fakeStore) ListAddresses(_ context.Context, _ uuid.UUID) ([]Address, error) {
	if f.defaultAddr.Address == "" {
		return []Address{}, nil
	}
	return []Address{f.defaultAddr}, nil
}

func (f *fakeStore) OrgByAddress(_ context.Context, _ string) (uuid.UUID, error) {
	return uuid.Nil, ErrAddressNotFound
}

func (f *fakeStore) OrgIDsWithAddresses(_ context.Context) ([]uuid.UUID, error) {
	return f.orgIDs, nil
}

func TestServiceSendAndPollRoundTrip(t *testing.T) {
	ctx := context.Background()
	prov := newFakeProvider()

	orgA := uuid.New()
	orgB := uuid.New()
	storeA := newFakeStore("alice@qerds.localhost")
	storeB := newFakeStore("bob@qerds.localhost")
	svcA := NewService(storeA, storeA, prov)
	svcB := NewService(storeB, storeB, prov)

	attachments := []qerdsprovider.Attachment{
		{Filename: "filing.pdf", ContentType: "application/pdf", Content: []byte("%PDF-1.4 stub")},
	}
	msg, err := svcA.Send(ctx, orgA, "", "bob@qerds.localhost", "hello", "world", attachments)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if msg.Status != StatusDelivered {
		t.Fatalf("status = %q, want %q", msg.Status, StatusDelivered)
	}
	if msg.ProviderRef == "" {
		t.Fatal("expected provider ref on sent message")
	}
	if _, ok := storeA.sent[msg.ID]; !ok {
		t.Fatal("expected RecordSent to be called for the message")
	}
	if len(storeA.outboundAttachments) != 1 || storeA.outboundAttachments[0].Filename != "filing.pdf" {
		t.Fatalf("outbound attachments = %+v, want the filing.pdf attachment persisted", storeA.outboundAttachments)
	}

	received, err := svcB.Poll(ctx, orgB)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if received != 1 {
		t.Fatalf("received = %d, want 1", received)
	}
	// The provider loops the attachment through to the recipient's inbox.
	if len(storeB.inboundAttachments) != 1 || storeB.inboundAttachments[0].Filename != "filing.pdf" {
		t.Fatalf("inbound attachments = %+v, want the looped-back attachment", storeB.inboundAttachments)
	}

	// The inbox is drained on fetch; a second poll yields nothing new.
	again, err := svcB.Poll(ctx, orgB)
	if err != nil {
		t.Fatalf("Poll (again): %v", err)
	}
	if again != 0 {
		t.Fatalf("second poll received = %d, want 0", again)
	}
}

func TestServiceSendWithoutSenderAddress(t *testing.T) {
	ctx := context.Background()
	prov := newFakeProvider()
	store := newFakeStore("") // no default address
	svc := NewService(store, store, prov)

	_, err := svc.Send(ctx, uuid.New(), "", "bob@qerds.localhost", "hi", "", nil)
	if !errors.Is(err, ErrNoSenderAddress) {
		t.Fatalf("err = %v, want ErrNoSenderAddress", err)
	}
}

func TestServiceSendWithChosenSender(t *testing.T) {
	ctx := context.Background()
	prov := newFakeProvider()
	store := newFakeStore("alice@qerds.localhost")
	svc := NewService(store, store, prov)

	// Sending explicitly from an owned address is honoured.
	msg, err := svc.Send(ctx, uuid.New(), "alice@qerds.localhost", "bob@qerds.localhost", "hi", "", nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if msg.SenderAddress != "alice@qerds.localhost" {
		t.Fatalf("sender = %q, want alice@qerds.localhost", msg.SenderAddress)
	}

	// Sending from an address the org does not own is rejected.
	if _, err := svc.Send(ctx, uuid.New(), "eve@qerds.localhost", "bob@qerds.localhost", "hi", "", nil); !errors.Is(err, ErrSenderNotOwned) {
		t.Fatalf("err = %v, want ErrSenderNotOwned", err)
	}
}

// TestServicePollAll checks the background poll worker's fan-out: PollAll polls
// every org reported by OrgIDsWithAddresses and sums what each stored.
func TestServicePollAll(t *testing.T) {
	ctx := context.Background()
	prov := newFakeProvider()

	orgA := uuid.New()
	orgB := uuid.New()
	// One shared store standing in for two orgs that both have an address; the
	// poll worker drives every org OrgIDsWithAddresses returns.
	store := newFakeStore("alice@qerds.localhost")
	store.orgIDs = []uuid.UUID{orgA, orgB}
	svc := NewService(store, store, prov)

	// Two messages waiting in the polled address's inbox.
	sender := newFakeStore("carol@qerds.localhost")
	sendSvc := NewService(sender, sender, prov)
	for _, subject := range []string{"one", "two"} {
		if _, err := sendSvc.Send(ctx, uuid.New(), "", "alice@qerds.localhost", subject, "", nil); err != nil {
			t.Fatalf("Send %q: %v", subject, err)
		}
	}

	received, err := svc.PollAll(ctx)
	if err != nil {
		t.Fatalf("PollAll: %v", err)
	}
	if received != 2 {
		t.Fatalf("received = %d, want 2", received)
	}

	// Inbox drained: a second sweep stores nothing new.
	again, err := svc.PollAll(ctx)
	if err != nil {
		t.Fatalf("PollAll (again): %v", err)
	}
	if again != 0 {
		t.Fatalf("second PollAll received = %d, want 0", again)
	}
}
