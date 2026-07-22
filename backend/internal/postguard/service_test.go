package postguard

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// fakeStore records calls and returns canned values.
type fakeStore struct {
	setKey       string
	setErr       error
	setEncSecret string
	setEncErr    error
	setEncCalls  int
	decrypted    string
	decryptErr   error
	recorded     SentFile
	recordErr    error
	recordCalls  int
	delivery     NotificationDelivery
	deliveryErr  error
	setDelivery  NotificationDelivery
	setDeliveryN int
}

func (f *fakeStore) EncryptionKeyInfo(context.Context, uuid.UUID) (EncryptionKeyInfo, error) {
	return EncryptionKeyInfo{}, nil
}

func (f *fakeStore) SetEncryptionKey(_ context.Context, _ uuid.UUID, secret string) error {
	f.setEncCalls++
	f.setEncSecret = secret
	return f.setEncErr
}

func (f *fakeStore) RemoveEncryptionKey(context.Context, uuid.UUID) error { return nil }

func (f *fakeStore) APIKeyInfo(context.Context, uuid.UUID) (APIKeyInfo, error) {
	return APIKeyInfo{}, nil
}

func (f *fakeStore) SetAPIKey(_ context.Context, _ uuid.UUID, apiKey string) error {
	f.setKey = apiKey
	return f.setErr
}
func (f *fakeStore) DeleteAPIKey(context.Context, uuid.UUID) error { return nil }
func (f *fakeStore) DecryptedAPIKey(context.Context, uuid.UUID) (string, error) {
	return f.decrypted, f.decryptErr
}

func (f *fakeStore) NotificationDelivery(context.Context, uuid.UUID) (NotificationDelivery, error) {
	if f.delivery == "" && f.deliveryErr == nil {
		return NotifyPostGuard, nil
	}
	return f.delivery, f.deliveryErr
}

func (f *fakeStore) SetNotificationDelivery(_ context.Context, _ uuid.UUID, method NotificationDelivery) error {
	f.setDeliveryN++
	f.setDelivery = method
	return nil
}

func (f *fakeStore) RecordSentFile(_ context.Context, _, _ uuid.UUID, s SentFile) (SentFile, error) {
	f.recordCalls++
	f.recorded = s
	return s, f.recordErr
}
func (f *fakeStore) ListSentFiles(context.Context, uuid.UUID) ([]SentFile, error) { return nil, nil }

type fakeSender struct {
	uuid      string
	err       error
	gotAPIKey string
	gotNotify bool
	calls     int
}

func (f *fakeSender) Send(_ context.Context, req sendRequest) (string, error) {
	f.calls++
	f.gotAPIKey = req.APIKey
	f.gotNotify = req.Notify
	return f.uuid, f.err
}

// fakeNotifier records the "own SMTP" notification path.
type fakeNotifier struct {
	err            error
	calls          int
	gotRecipients  []string
	gotOrgName     string
	gotMessage     string
	gotDownloadURL string
}

func (f *fakeNotifier) SendPostguardNotification(_ context.Context, _ uuid.UUID, recipients []string, orgName, message, downloadURL string) error {
	f.calls++
	f.gotRecipients = recipients
	f.gotOrgName = orgName
	f.gotMessage = message
	f.gotDownloadURL = downloadURL
	return f.err
}

// newService builds a Service with the given store/sender and no-op notifier,
// for tests that don't exercise the SMTP path.
func newTestService(st store, snd sender) *Service {
	return NewService(st, snd, &fakeNotifier{}, "https://postguard.eu")
}

func TestSetAPIKeyRejectsMalformed(t *testing.T) {
	for _, k := range []string{"", "PG-", "nope", "yivi_live_pg_abc"} {
		st := &fakeStore{}
		svc := newTestService(st, &fakeSender{})
		if err := svc.SetAPIKey(context.Background(), uuid.New(), k); !errors.Is(err, ErrInvalidAPIKey) {
			t.Errorf("key %q: expected ErrInvalidAPIKey, got %v", k, err)
		}
		if st.setKey != "" {
			t.Errorf("key %q: store should not have been called", k)
		}
	}
}

func TestSetAPIKeyAcceptsValidAndTrims(t *testing.T) {
	st := &fakeStore{}
	svc := newTestService(st, &fakeSender{})
	if err := svc.SetAPIKey(context.Background(), uuid.New(), "  PG-abcdef  "); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.setKey != "PG-abcdef" {
		t.Fatalf("expected trimmed key stored, got %q", st.setKey)
	}
}

func TestSetEncryptionKeyRejectsEmpty(t *testing.T) {
	for _, k := range []string{"", "   ", "\t"} {
		st := &fakeStore{}
		svc := newTestService(st, &fakeSender{})
		if err := svc.SetEncryptionKey(context.Background(), uuid.New(), k); !errors.Is(err, ErrInvalidEncryptionKey) {
			t.Errorf("key %q: expected ErrInvalidEncryptionKey, got %v", k, err)
		}
		if st.setEncCalls != 0 {
			t.Errorf("key %q: store should not have been called", k)
		}
	}
}

func TestSetEncryptionKeyAcceptsValue(t *testing.T) {
	st := &fakeStore{}
	svc := newTestService(st, &fakeSender{})
	if err := svc.SetEncryptionKey(context.Background(), uuid.New(), "my-secret"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.setEncCalls != 1 || st.setEncSecret != "my-secret" {
		t.Fatalf("expected store called with secret, got calls=%d secret=%q", st.setEncCalls, st.setEncSecret)
	}
}

func TestSendValidatesInput(t *testing.T) {
	svc := newTestService(&fakeStore{}, &fakeSender{})
	org, user := uuid.New(), uuid.New()

	if _, err := svc.Send(context.Background(), org, user, SendInput{Files: []FileBlob{{Name: "a"}}}); !errors.Is(err, ErrNoRecipients) {
		t.Errorf("expected ErrNoRecipients, got %v", err)
	}
	if _, err := svc.Send(context.Background(), org, user, SendInput{Recipients: []string{"a@b.nl"}}); !errors.Is(err, ErrNoFiles) {
		t.Errorf("expected ErrNoFiles, got %v", err)
	}
}

func TestSendUsesDecryptedKeyAndRecords(t *testing.T) {
	st := &fakeStore{decrypted: "PG-thekey"}
	snd := &fakeSender{uuid: "cryptify-123"}
	svc := newTestService(st, snd)

	sent, err := svc.Send(context.Background(), uuid.New(), uuid.New(), SendInput{
		Recipients: []string{"finance@caesar.nl"},
		Files:      []FileBlob{{Name: "report.pdf", Data: []byte("hello")}},
		Notify:     true,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if snd.gotAPIKey != "PG-thekey" {
		t.Errorf("sender got api key %q, want PG-thekey", snd.gotAPIKey)
	}
	if sent.CryptifyUUID != "cryptify-123" {
		t.Errorf("cryptify uuid = %q, want cryptify-123", sent.CryptifyUUID)
	}
	if sent.SizeBytes != 5 {
		t.Errorf("size = %d, want 5", sent.SizeBytes)
	}
	if st.recordCalls != 1 {
		t.Errorf("record called %d times, want 1", st.recordCalls)
	}
}

func TestSendPropagatesKeyNotSet(t *testing.T) {
	st := &fakeStore{decryptErr: ErrKeyNotSet}
	snd := &fakeSender{}
	svc := newTestService(st, snd)
	_, err := svc.Send(context.Background(), uuid.New(), uuid.New(), SendInput{
		Recipients: []string{"a@b.nl"},
		Files:      []FileBlob{{Name: "f", Data: []byte("x")}},
	})
	if !errors.Is(err, ErrKeyNotSet) {
		t.Fatalf("expected ErrKeyNotSet, got %v", err)
	}
	if snd.calls != 0 {
		t.Error("sender must not be called when the key is missing")
	}
}

func TestSendPostGuardDeliveryUsesSidecarNotify(t *testing.T) {
	st := &fakeStore{decrypted: "PG-key", delivery: NotifyPostGuard}
	snd := &fakeSender{uuid: "u-1"}
	notif := &fakeNotifier{}
	svc := NewService(st, snd, notif, "https://postguard.eu")

	if _, err := svc.Send(context.Background(), uuid.New(), uuid.New(), SendInput{
		Recipients: []string{"a@b.nl"},
		Files:      []FileBlob{{Name: "f", Data: []byte("x")}},
		Notify:     true,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !snd.gotNotify {
		t.Error("PostGuard delivery: sidecar should be told to notify recipients")
	}
	if notif.calls != 0 {
		t.Errorf("PostGuard delivery: own-SMTP notifier must not be called, got %d calls", notif.calls)
	}
	if st.recordCalls != 1 {
		t.Errorf("record called %d times, want 1", st.recordCalls)
	}
}

func TestSendSMTPDeliveryNotifiesOverOwnSMTP(t *testing.T) {
	st := &fakeStore{decrypted: "PG-key", delivery: NotifySMTP}
	snd := &fakeSender{uuid: "cryptify-abc"}
	notif := &fakeNotifier{}
	svc := NewService(st, snd, notif, "https://postguard.eu/")

	_, err := svc.Send(context.Background(), uuid.New(), uuid.New(), SendInput{
		Recipients: []string{"a@b.nl", "c@d.nl"},
		Files:      []FileBlob{{Name: "f", Data: []byte("x")}},
		Notify:     true,
		Message:    "please review",
		OrgName:    "Caesar BV",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if snd.gotNotify {
		t.Error("SMTP delivery: PostGuard must upload silently (sidecar notify=false)")
	}
	if notif.calls != 1 {
		t.Fatalf("SMTP delivery: notifier called %d times, want 1", notif.calls)
	}
	if len(notif.gotRecipients) != 2 || notif.gotOrgName != "Caesar BV" || notif.gotMessage != "please review" {
		t.Errorf("notifier got recipients=%v org=%q message=%q", notif.gotRecipients, notif.gotOrgName, notif.gotMessage)
	}
	if want := "https://postguard.eu/download?uuid=cryptify-abc"; notif.gotDownloadURL != want {
		t.Errorf("download URL = %q, want %q", notif.gotDownloadURL, want)
	}
	if st.recordCalls != 1 {
		t.Errorf("record called %d times, want 1", st.recordCalls)
	}
}

func TestSendSMTPDeliveryWithoutNotifyStaysSilent(t *testing.T) {
	st := &fakeStore{decrypted: "PG-key", delivery: NotifySMTP}
	snd := &fakeSender{uuid: "u-2"}
	notif := &fakeNotifier{}
	svc := NewService(st, snd, notif, "https://postguard.eu")

	if _, err := svc.Send(context.Background(), uuid.New(), uuid.New(), SendInput{
		Recipients: []string{"a@b.nl"},
		Files:      []FileBlob{{Name: "f", Data: []byte("x")}},
		Notify:     false,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if snd.gotNotify {
		t.Error("notify=false: sidecar must not notify")
	}
	if notif.calls != 0 {
		t.Errorf("notify=false: own-SMTP notifier must not be called, got %d calls", notif.calls)
	}
}

func TestSendSMTPNotConfiguredFailsBeforeRecord(t *testing.T) {
	st := &fakeStore{decrypted: "PG-key", delivery: NotifySMTP}
	snd := &fakeSender{uuid: "u-3"}
	notif := &fakeNotifier{err: ErrSMTPNotConfigured}
	svc := NewService(st, snd, notif, "https://postguard.eu")

	_, err := svc.Send(context.Background(), uuid.New(), uuid.New(), SendInput{
		Recipients: []string{"a@b.nl"},
		Files:      []FileBlob{{Name: "f", Data: []byte("x")}},
		Notify:     true,
	})
	if !errors.Is(err, ErrSMTPNotConfigured) {
		t.Fatalf("expected ErrSMTPNotConfigured, got %v", err)
	}
	if st.recordCalls != 0 {
		t.Error("transfer must not be recorded when the SMTP notification fails")
	}
}

func TestSetNotificationDeliveryValidates(t *testing.T) {
	st := &fakeStore{}
	svc := NewService(st, &fakeSender{}, &fakeNotifier{}, "https://postguard.eu")

	if err := svc.SetNotificationDelivery(context.Background(), uuid.New(), "carrier-pigeon"); !errors.Is(err, ErrInvalidNotificationDelivery) {
		t.Errorf("expected ErrInvalidNotificationDelivery, got %v", err)
	}
	if st.setDeliveryN != 0 {
		t.Error("store must not be called for an invalid method")
	}
	if err := svc.SetNotificationDelivery(context.Background(), uuid.New(), NotifySMTP); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.setDeliveryN != 1 || st.setDelivery != NotifySMTP {
		t.Errorf("store got calls=%d method=%q, want 1 smtp", st.setDeliveryN, st.setDelivery)
	}
}
