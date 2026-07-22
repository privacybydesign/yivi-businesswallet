package postguard

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// store is the persistence surface the service coordinates.
type store interface {
	EncryptionKeyInfo(ctx context.Context, orgID uuid.UUID) (EncryptionKeyInfo, error)
	SetEncryptionKey(ctx context.Context, orgID uuid.UUID, secret string) error
	RemoveEncryptionKey(ctx context.Context, orgID uuid.UUID) error
	APIKeyInfo(ctx context.Context, orgID uuid.UUID) (APIKeyInfo, error)
	SetAPIKey(ctx context.Context, orgID uuid.UUID, apiKey string) error
	DeleteAPIKey(ctx context.Context, orgID uuid.UUID) error
	DecryptedAPIKey(ctx context.Context, orgID uuid.UUID) (string, error)
	NotificationDelivery(ctx context.Context, orgID uuid.UUID) (NotificationDelivery, error)
	SetNotificationDelivery(ctx context.Context, orgID uuid.UUID, method NotificationDelivery) error
	RecordSentFile(ctx context.Context, orgID, senderUserID uuid.UUID, f SentFile) (SentFile, error)
	ListSentFiles(ctx context.Context, orgID uuid.UUID) ([]SentFile, error)
}

// sender is the sidecar surface the service uses to encrypt and upload.
type sender interface {
	Send(ctx context.Context, req sendRequest) (string, error)
}

// notifier delivers the recipient notification via the org's own SMTP config,
// for the NotifySMTP delivery method. downloadURL is the link recipients follow
// to fetch the sealed package. Implementations must return ErrSMTPNotConfigured
// when the org has no usable SMTP configuration.
type notifier interface {
	SendPostguardNotification(ctx context.Context, orgID uuid.UUID, recipients []string, orgName, message, downloadURL string) error
}

// Service coordinates the API-key store, the sidecar sender and — for the "own
// SMTP" notification path — the SMTP notifier.
type Service struct {
	store      store
	sender     sender
	notifier   notifier
	websiteURL string
}

// NewService wires the service. websiteURL is the public PostGuard website base
// the "own SMTP" recipient download link points at (e.g. https://postguard.eu).
func NewService(s store, snd sender, n notifier, websiteURL string) *Service {
	return &Service{store: s, sender: snd, notifier: n, websiteURL: websiteURL}
}

// Settings returns the combined non-secret PostGuard configuration for an org.
func (s *Service) Settings(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	enc, err := s.store.EncryptionKeyInfo(ctx, orgID)
	if err != nil {
		return Settings{}, err
	}
	api, err := s.store.APIKeyInfo(ctx, orgID)
	if err != nil {
		return Settings{}, err
	}
	delivery, err := s.store.NotificationDelivery(ctx, orgID)
	if err != nil {
		return Settings{}, err
	}
	return Settings{APIKey: api, EncryptionKey: enc, Notifications: delivery}, nil
}

// SetNotificationDelivery validates and stores an org's recipient-notification
// delivery method.
func (s *Service) SetNotificationDelivery(ctx context.Context, orgID uuid.UUID, method NotificationDelivery) error {
	if !method.valid() {
		return ErrInvalidNotificationDelivery
	}
	return s.store.SetNotificationDelivery(ctx, orgID, method)
}

// SetEncryptionKey validates and stores an org's encryption key (owner-supplied).
func (s *Service) SetEncryptionKey(ctx context.Context, orgID uuid.UUID, secret string) error {
	if strings.TrimSpace(secret) == "" {
		return ErrInvalidEncryptionKey
	}
	return s.store.SetEncryptionKey(ctx, orgID, secret)
}

// RemoveEncryptionKey removes an org's encryption key (and its API key).
func (s *Service) RemoveEncryptionKey(ctx context.Context, orgID uuid.UUID) error {
	return s.store.RemoveEncryptionKey(ctx, orgID)
}

// SetAPIKey validates and stores an org's PostGuard for Business API key.
func (s *Service) SetAPIKey(ctx context.Context, orgID uuid.UUID, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if !strings.HasPrefix(apiKey, apiKeyPrefix) || len(apiKey) <= len(apiKeyPrefix) {
		return ErrInvalidAPIKey
	}
	return s.store.SetAPIKey(ctx, orgID, apiKey)
}

// DeleteAPIKey removes an org's stored key.
func (s *Service) DeleteAPIKey(ctx context.Context, orgID uuid.UUID) error {
	return s.store.DeleteAPIKey(ctx, orgID)
}

// ListSentFiles returns the org's sent-file history.
func (s *Service) ListSentFiles(ctx context.Context, orgID uuid.UUID) ([]SentFile, error) {
	return s.store.ListSentFiles(ctx, orgID)
}

// Send encrypts and uploads the files to the recipients using the org's stored
// API key, then records the transfer. The files are named in the returned
// record by the first file; total size is the sum of all parts.
func (s *Service) Send(ctx context.Context, orgID, senderUserID uuid.UUID, in SendInput) (SentFile, error) {
	if len(in.Recipients) == 0 {
		return SentFile{}, ErrNoRecipients
	}
	if len(in.Files) == 0 {
		return SentFile{}, ErrNoFiles
	}

	apiKey, err := s.store.DecryptedAPIKey(ctx, orgID)
	if err != nil {
		return SentFile{}, err
	}

	delivery, err := s.store.NotificationDelivery(ctx, orgID)
	if err != nil {
		return SentFile{}, err
	}

	// PostGuard's hosted service notifies recipients only on the PostGuard
	// delivery path. On the "own SMTP" path we upload silently and send the
	// notification ourselves below, so PostGuard must not also mail them.
	postguardNotify := in.Notify && delivery == NotifyPostGuard

	uuidStr, err := s.sender.Send(ctx, sendRequest{
		APIKey:     apiKey,
		Recipients: in.Recipients,
		Files:      in.Files,
		Notify:     postguardNotify,
		Message:    in.Message,
	})
	if err != nil {
		return SentFile{}, err
	}

	// Own-SMTP path: compose and deliver the notification via the org's SMTP
	// config before recording, mirroring how a failed upload returns before the
	// transfer is recorded. A missing SMTP config surfaces as ErrSMTPNotConfigured.
	if in.Notify && delivery == NotifySMTP {
		if err := s.notifier.SendPostguardNotification(ctx, orgID, in.Recipients, in.OrgName, in.Message, s.downloadURL(uuidStr)); err != nil {
			return SentFile{}, err
		}
	}

	var totalSize int64
	for _, f := range in.Files {
		totalSize += int64(len(f.Data))
	}

	return s.store.RecordSentFile(ctx, orgID, senderUserID, SentFile{
		FileName:     displayName(in.Files),
		SizeBytes:    totalSize,
		Recipients:   in.Recipients,
		CryptifyUUID: uuidStr,
		ExpiresAfter: in.ExpiresAfter,
	})
}

// downloadURL builds the recipient-facing PostGuard link for a sealed package.
// Files-mode uploads resolve at "<website>/download?uuid=<uuid>" — the same page
// PostGuard's own notification links to.
func (s *Service) downloadURL(cryptifyUUID string) string {
	base := strings.TrimRight(s.websiteURL, "/")
	return fmt.Sprintf("%s/download?uuid=%s", base, url.QueryEscape(cryptifyUUID))
}

// displayName names a transfer by its single file, or "<first> (+N more)".
func displayName(files []FileBlob) string {
	switch len(files) {
	case 0:
		return ""
	case 1:
		return files[0].Name
	default:
		return fmt.Sprintf("%s (+%d more)", files[0].Name, len(files)-1)
	}
}
