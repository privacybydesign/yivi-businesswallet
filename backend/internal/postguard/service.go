package postguard

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// store is the persistence surface the service coordinates.
type store interface {
	APIKeyInfo(ctx context.Context, orgID uuid.UUID) (APIKeyInfo, error)
	SetAPIKey(ctx context.Context, orgID uuid.UUID, apiKey string) error
	DeleteAPIKey(ctx context.Context, orgID uuid.UUID) error
	DecryptedAPIKey(ctx context.Context, orgID uuid.UUID) (string, error)
	RecordSentFile(ctx context.Context, orgID, senderUserID uuid.UUID, f SentFile) (SentFile, error)
	ListSentFiles(ctx context.Context, orgID uuid.UUID) ([]SentFile, error)
}

// sender is the sidecar surface the service uses to encrypt and upload.
type sender interface {
	Send(ctx context.Context, req sendRequest) (string, error)
}

// Service coordinates the API-key store and the sidecar sender.
type Service struct {
	store  store
	sender sender
}

func NewService(s store, snd sender) *Service {
	return &Service{store: s, sender: snd}
}

// APIKeyInfo returns the non-secret view of the org's stored key.
func (s *Service) APIKeyInfo(ctx context.Context, orgID uuid.UUID) (APIKeyInfo, error) {
	return s.store.APIKeyInfo(ctx, orgID)
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

	uuidStr, err := s.sender.Send(ctx, sendRequest{
		APIKey:     apiKey,
		Recipients: in.Recipients,
		Files:      in.Files,
		Notify:     in.Notify,
		Message:    in.Message,
	})
	if err != nil {
		return SentFile{}, err
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
