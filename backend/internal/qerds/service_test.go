package qerds

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
)

// fakeStore is an in-memory messageStore + addressStore for DB-free service tests.
type fakeStore struct {
	defaultAddr Address
	messages    []Message
	sent        map[uuid.UUID]qerdsprovider.SendReceipt
	seenRefs    map[string]bool
}

func newFakeStore(defaultAddress string) *fakeStore {
	f := &fakeStore{sent: map[uuid.UUID]qerdsprovider.SendReceipt{}, seenRefs: map[string]bool{}}
	if defaultAddress != "" {
		f.defaultAddr = Address{ID: uuid.New(), Address: defaultAddress, IsDefault: true}
	}
	return f
}

func (f *fakeStore) CreateOutbound(_ context.Context, orgID uuid.UUID, sender, recipient, subject, body string) (Message, error) {
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
	return uuid.Nil, ErrNoSenderAddress
}

func TestServiceSendAndPollRoundTrip(t *testing.T) {
	ctx := context.Background()
	prov := qerdsprovider.NewStubProvider()

	orgA := uuid.New()
	orgB := uuid.New()
	storeA := newFakeStore("alice@qerds.localhost")
	storeB := newFakeStore("bob@qerds.localhost")
	svcA := NewService(storeA, storeA, prov)
	svcB := NewService(storeB, storeB, prov)

	msg, err := svcA.Send(ctx, orgA, "bob@qerds.localhost", "hello", "world")
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

	received, err := svcB.Poll(ctx, orgB)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if received != 1 {
		t.Fatalf("received = %d, want 1", received)
	}

	// Stub inbox is drained; a second poll yields nothing new.
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
	prov := qerdsprovider.NewStubProvider()
	store := newFakeStore("") // no default address
	svc := NewService(store, store, prov)

	_, err := svc.Send(ctx, uuid.New(), "bob@qerds.localhost", "hi", "")
	if !errors.Is(err, ErrNoSenderAddress) {
		t.Fatalf("err = %v, want ErrNoSenderAddress", err)
	}
}
