package export

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
)

// defaultMediaType is what an opaque payload is labelled: attachment bytes are
// provider-supplied and content-opaque to us.
const defaultMediaType = "application/octet-stream"

type qerdsReader interface {
	List(ctx context.Context, orgID uuid.UUID) ([]qerds.Message, error)
	GetWithEvidence(ctx context.Context, orgID, id uuid.UUID) (qerds.MessageWithEvidence, error)
	GetAttachmentContent(ctx context.Context, orgID, messageID, attachmentID uuid.UUID) (qerds.AttachmentContent, error)
	ListAddresses(ctx context.Context, orgID uuid.UUID) ([]qerds.Address, error)
	ListContacts(ctx context.Context, orgID uuid.UUID) ([]qerds.Contact, error)
}

// QerdsWriter fills the communication-logs section: messages with their status
// timeline, the evidence chain behind each, attachment payloads, and the
// organization's addresses and address book.
type QerdsWriter struct {
	store qerdsReader
	now   func() time.Time
}

func NewQerdsWriter(store qerdsReader) *QerdsWriter {
	return &QerdsWriter{store: store, now: time.Now}
}

func (w *QerdsWriter) Key() string { return SectionQerds }

func (w *QerdsWriter) Write(ctx context.Context, orgID uuid.UUID, s *SectionBundle) error {
	messages, err := w.store.List(ctx, orgID)
	if err != nil {
		return fmt.Errorf("export: reading qerds messages: %w", err)
	}

	records := make([]messageRecord, 0, len(messages))
	evidenceCount, attachmentCount := 0, 0
	for _, m := range messages {
		record, counts, err := w.message(ctx, orgID, m, s)
		if err != nil {
			return err
		}
		evidenceCount += counts.evidence
		attachmentCount += counts.attachments
		records = append(records, record)
	}
	s.Count("messages", len(records))
	s.Count("evidence", evidenceCount)
	s.Count("attachments", attachmentCount)
	if err := s.AddJSON("messages.json", records); err != nil {
		return err
	}

	addresses, err := w.store.ListAddresses(ctx, orgID)
	if err != nil {
		return fmt.Errorf("export: reading qerds addresses: %w", err)
	}
	addressRecords := make([]addressRecord, 0, len(addresses))
	for _, a := range addresses {
		addressRecords = append(addressRecords, addressRecordOf(a))
	}
	s.Count("addresses", len(addressRecords))
	if err := s.AddJSON("addresses.json", addressRecords); err != nil {
		return err
	}

	contacts, err := w.store.ListContacts(ctx, orgID)
	if err != nil {
		return fmt.Errorf("export: reading qerds contacts: %w", err)
	}
	contactRecords := make([]contactRecord, 0, len(contacts))
	for _, c := range contacts {
		contactRecords = append(contactRecords, contactRecordOf(c))
	}
	s.Count("contacts", len(contactRecords))
	return s.AddJSON("contacts.json", contactRecords)
}

type messageCounts struct {
	evidence    int
	attachments int
}

func (w *QerdsWriter) message(ctx context.Context, orgID uuid.UUID, m qerds.Message, s *SectionBundle) (messageRecord, messageCounts, error) {
	detail, err := w.store.GetWithEvidence(ctx, orgID, m.ID)
	if err != nil {
		return messageRecord{}, messageCounts{}, fmt.Errorf("export: reading qerds message %s: %w", m.ID, err)
	}

	record := messageRecordOf(detail.Message)
	counts := messageCounts{}

	if len(detail.Evidence) > 0 {
		path, err := w.evidence(detail, s)
		if err != nil {
			return messageRecord{}, messageCounts{}, err
		}
		record.EvidencePath = path
		counts.evidence = len(detail.Evidence)
	}

	for _, att := range detail.Attachments {
		attachment, carried, err := w.attachment(ctx, orgID, m.ID, att, s)
		if err != nil {
			return messageRecord{}, messageCounts{}, err
		}
		if carried {
			counts.attachments++
		}
		record.Attachments = append(record.Attachments, attachment)
	}
	return record, counts, nil
}

// evidence packages the message's chain as ASiC-E. The container is always
// carried whatever the budget: raw evidence is what gives a message legal
// effect, so dropping it to save space is not a trade this export makes.
func (w *QerdsWriter) evidence(detail qerds.MessageWithEvidence, s *SectionBundle) (string, error) {
	blobs := make([]evidenceBlob, 0, len(detail.Evidence))
	for _, e := range detail.Evidence {
		blobs = append(blobs, evidenceBlob{
			ID:                 e.ID.String(),
			Type:               e.Type,
			ProviderRef:        e.ProviderRef,
			QualifiedTimestamp: e.QualifiedTimestamp,
			Raw:                e.Raw,
		})
	}

	container, err := buildASiCE(detail.ID.String(), blobs, w.now())
	if err != nil {
		return "", err
	}
	name := "evidence/" + detail.ID.String() + asiceExtension
	if err := s.writeUnbudgeted(name, asiceMediaType, container); err != nil {
		return "", err
	}
	return s.path(name), nil
}

// attachment carries one payload. The on-disk name is the attachment's uuid with
// no extension: naming by uuid removes the path-traversal and duplicate-name
// class outright instead of sanitising a provider-supplied filename, which the
// JSON record keeps.
func (w *QerdsWriter) attachment(ctx context.Context, orgID, messageID uuid.UUID, att qerds.Attachment, s *SectionBundle) (attachmentRecord, bool, error) {
	record := attachmentRecordOf(att)
	name := "attachments/" + messageID.String() + "/" + att.ID.String()

	content, err := w.store.GetAttachmentContent(ctx, orgID, messageID, att.ID)
	if errors.Is(err, qerds.ErrAttachmentNotFound) {
		s.Omit(name, ReasonUnavailable, att.SizeBytes, storedChecksum(att.ContentHash))
		return record, false, nil
	}
	if err != nil {
		return attachmentRecord{}, false, fmt.Errorf("export: reading qerds attachment %s: %w", att.ID, err)
	}

	before := len(s.omitted)
	if err := s.AddBytes(name, defaultMediaType, content.Content); err != nil {
		return attachmentRecord{}, false, err
	}
	if len(s.omitted) > before {
		// Over budget: the record still describes the payload, and the manifest
		// says why the bytes are absent.
		return record, false, nil
	}

	record.Path = s.path(name)
	return record, true, nil
}

// messageRecord is one entry of the communication log. Body is text on the
// message row and stays inline; only attachment payloads become files.
type messageRecord struct {
	ID                     uuid.UUID          `json:"id"`
	Direction              string             `json:"direction"`
	SenderAddress          string             `json:"senderAddress"`
	RecipientAddress       string             `json:"recipientAddress"`
	Subject                string             `json:"subject"`
	Body                   string             `json:"body"`
	ProviderRef            string             `json:"providerRef,omitempty"`
	Status                 string             `json:"status"`
	SubmittedAt            *string            `json:"submittedAt,omitempty"`
	DeliveredAt            *string            `json:"deliveredAt,omitempty"`
	QualifiedTimestampSend *string            `json:"qualifiedTimestampSend,omitempty"`
	EvidencePath           string             `json:"evidencePath,omitempty"`
	Attachments            []attachmentRecord `json:"attachments"`
	CreatedAt              string             `json:"createdAt"`
	UpdatedAt              string             `json:"updatedAt"`
}

func messageRecordOf(m qerds.Message) messageRecord {
	return messageRecord{
		ID:               m.ID,
		Direction:        m.Direction,
		SenderAddress:    m.SenderAddress,
		RecipientAddress: m.RecipientAddress,
		Subject:          m.Subject,
		Body:             m.Body,
		// The QTSP's own handle for this message, so a receiver can correlate
		// with the provider's records.
		ProviderRef:            m.ProviderRef,
		Status:                 m.Status,
		SubmittedAt:            optionalTimestamp(m.SubmittedAt),
		DeliveredAt:            optionalTimestamp(m.DeliveredAt),
		QualifiedTimestampSend: optionalTimestamp(m.QualifiedTimestampSend),
		Attachments:            []attachmentRecord{},
		CreatedAt:              timestamp(m.CreatedAt),
		UpdatedAt:              timestamp(m.UpdatedAt),
	}
}

// attachmentRecord describes a payload. Path is empty when the bundle carries no
// bytes for it — the manifest's omitted list then says why, and ContentHash is
// still the stored integrity metadata a receiver can verify against.
type attachmentRecord struct {
	ID          uuid.UUID `json:"id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"contentType"`
	ContentHash string    `json:"contentHash"`
	SizeBytes   int64     `json:"sizeBytes"`
	Path        string    `json:"path,omitempty"`
	CreatedAt   string    `json:"createdAt"`
}

func attachmentRecordOf(a qerds.Attachment) attachmentRecord {
	return attachmentRecord{
		ID:          a.ID,
		Filename:    a.Filename,
		ContentType: a.ContentType,
		ContentHash: a.ContentHash,
		SizeBytes:   a.SizeBytes,
		CreatedAt:   timestamp(a.CreatedAt),
	}
}

type addressRecord struct {
	ID          uuid.UUID `json:"id"`
	Address     string    `json:"address"`
	IsDefault   bool      `json:"isDefault"`
	ProviderRef string    `json:"providerRef,omitempty"`
	CreatedAt   string    `json:"createdAt"`
}

func addressRecordOf(a qerds.Address) addressRecord {
	return addressRecord{
		ID:          a.ID,
		Address:     a.Address,
		IsDefault:   a.IsDefault,
		ProviderRef: a.ProviderRef,
		CreatedAt:   timestamp(a.CreatedAt),
	}
}

type contactRecord struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Address   string    `json:"address"`
	LegalName *string   `json:"legalName,omitempty"`
	KVKNumber *string   `json:"kvkNumber,omitempty"`
	EUID      *string   `json:"euid,omitempty"`
	CreatedAt string    `json:"createdAt"`
	UpdatedAt string    `json:"updatedAt"`
}

func contactRecordOf(c qerds.Contact) contactRecord {
	return contactRecord{
		ID:        c.ID,
		Name:      c.Name,
		Address:   c.Address,
		LegalName: c.LegalName,
		KVKNumber: c.KVKNumber,
		EUID:      c.EUID,
		CreatedAt: timestamp(c.CreatedAt),
		UpdatedAt: timestamp(c.UpdatedAt),
	}
}

// storedChecksum reports the stored content hash as a manifest checksum when it
// is a bare sha-256 digest, and nothing when the store recorded something else —
// an omission with no checksum is honest, one with a mislabelled digest is not.
func storedChecksum(hash string) *Checksum {
	const sha256HexLen = 64
	if len(hash) != sha256HexLen {
		return nil
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return nil
		}
	}
	return &Checksum{Algorithm: checksumAlgorithm, Value: hash}
}
