// Package postguard is the org-scoped PostGuard capability: it stores each
// organization's PostGuard for Business API key (AES-GCM encrypted at rest) and
// sends encrypted files by proxying to the internal PostGuard sidecar, which
// runs the @e4a/pg-js SDK. The sidecar is never reached by clients directly —
// only this backend holds the shared secret and the org's API key.
package postguard

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// apiKeyPrefix is the documented prefix of a PostGuard for Business API key.
const apiKeyPrefix = "PG-"

var (
	// ErrNotConfigured is returned when the deployment lacks the master
	// key-encryption key or sidecar configuration needed to run the feature.
	ErrNotConfigured = errors.New("postguard: feature not configured")
	// ErrEncryptionKeyNotSet is returned when an org has not configured its
	// per-org encryption key yet (required before an API key can be stored).
	ErrEncryptionKeyNotSet = errors.New("postguard: encryption key not set")
	// ErrKeyNotSet is returned when an org has no PostGuard API key stored yet.
	ErrKeyNotSet = errors.New("postguard: api key not set")
	// ErrInvalidAPIKey is returned for a malformed API key on upload.
	ErrInvalidAPIKey = errors.New("postguard: invalid api key")
	// ErrInvalidEncryptionKey is returned for an empty encryption key value.
	ErrInvalidEncryptionKey = errors.New("postguard: invalid encryption key")
	// ErrNoRecipients / ErrNoFiles guard the send request.
	ErrNoRecipients = errors.New("postguard: at least one recipient is required")
	ErrNoFiles      = errors.New("postguard: at least one file is required")
	// ErrPayloadTooLarge is returned when the upload exceeds the sidecar's cap.
	ErrPayloadTooLarge = errors.New("postguard: payload too large")
	// ErrSidecar is returned when the sidecar rejects or fails the request.
	ErrSidecar = errors.New("postguard: sidecar request failed")
	// ErrInvalidNotificationDelivery is returned for an unknown delivery method.
	ErrInvalidNotificationDelivery = errors.New("postguard: invalid notification delivery method")
	// ErrSMTPNotConfigured is returned when the org selected the "own SMTP"
	// delivery method but has no usable (present + enabled) SMTP configuration.
	ErrSMTPNotConfigured = errors.New("postguard: organization SMTP is not configured")
)

// NotificationDelivery is how recipient notifications are delivered for an org.
type NotificationDelivery string

const (
	// NotifyPostGuard lets PostGuard's hosted mailing service notify recipients;
	// the sender address comes from the business's PostGuard account. This is the
	// default and preserves the behaviour from before the setting existed.
	NotifyPostGuard NotificationDelivery = "postguard"
	// NotifySMTP has the backend deliver the notification itself using the org's
	// own SMTP configuration (internal/email); PostGuard stays silent.
	NotifySMTP NotificationDelivery = "smtp"
)

// valid reports whether d is a recognised delivery method.
func (d NotificationDelivery) valid() bool {
	return d == NotifyPostGuard || d == NotifySMTP
}

// APIKeyInfo is the non-secret view of an org's stored API key.
type APIKeyInfo struct {
	Configured bool       `json:"configured"`
	Last4      string     `json:"last4,omitempty"`
	UpdatedAt  *time.Time `json:"updatedAt,omitempty"`
}

// EncryptionKeyInfo is the non-secret view of an org's per-org encryption key.
type EncryptionKeyInfo struct {
	Configured bool       `json:"configured"`
	UpdatedAt  *time.Time `json:"updatedAt,omitempty"`
}

// Settings is the combined, non-secret PostGuard configuration for an org.
type Settings struct {
	APIKey        APIKeyInfo           `json:"apiKey"`
	EncryptionKey EncryptionKeyInfo    `json:"encryptionKey"`
	Notifications NotificationDelivery `json:"notifications"`
}

// SentFile is a record of an encrypted file transfer.
type SentFile struct {
	ID           uuid.UUID `json:"id"`
	FileName     string    `json:"fileName"`
	SizeBytes    int64     `json:"sizeBytes"`
	Recipients   []string  `json:"recipients"`
	CryptifyUUID string    `json:"cryptifyUuid"`
	ExpiresAfter string    `json:"expiresAfter,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
}

// FileBlob is one file to encrypt, buffered in memory.
type FileBlob struct {
	Name        string
	ContentType string
	Data        []byte
}

// SendInput is the request to encrypt and send one or more files.
type SendInput struct {
	Recipients   []string
	Files        []FileBlob
	Notify       bool
	Message      string
	ExpiresAfter string
	// OrgName is the sending organization's display name, used only to compose the
	// "own SMTP" notification e-mail. The handler fills it from the request org.
	OrgName string
}
