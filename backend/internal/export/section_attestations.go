package export

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
)

// sdJwtMediaType and sdJwtExtension are the format profile's tokens for an
// SD-JWT VC. Every credential record names its format explicitly so a reader
// never infers it from the file extension.
const (
	sdJwtFormat    = "dc+sd-jwt"
	sdJwtMediaType = "application/dc+sd-jwt"
	sdJwtExtension = ".sdjwt"
)

type attestationReader interface {
	ListIssued(ctx context.Context, orgID uuid.UUID) ([]attestation.Issued, error)
	ListHeld(ctx context.Context, orgID uuid.UUID) ([]attestation.HeldAttestation, error)
	ListSchemas(ctx context.Context, orgID uuid.UUID) ([]attestation.Schema, error)
	ListTemplates(ctx context.Context, orgID uuid.UUID) ([]attestation.Template, error)
	ListKeys(ctx context.Context, orgID uuid.UUID) ([]attestation.Key, error)
}

type credentialReader interface {
	Raw(ctx context.Context, orgID uuid.UUID, ref string) ([]byte, error)
}

// AttestationsWriter fills the EAA section: the issuance ledger, the held index
// and the credential material behind it.
type AttestationsWriter struct {
	store  attestationReader
	holder credentialReader
}

func NewAttestationsWriter(store attestationReader, holder credentialReader) *AttestationsWriter {
	return &AttestationsWriter{store: store, holder: holder}
}

func (w *AttestationsWriter) Key() string { return SectionAttestations }

func (w *AttestationsWriter) Write(ctx context.Context, orgID uuid.UUID, s *SectionBundle) error {
	issued, err := w.store.ListIssued(ctx, orgID)
	if err != nil {
		return fmt.Errorf("export: reading issued attestations: %w", err)
	}
	issuedRecords := make([]issuedRecord, 0, len(issued))
	for _, i := range issued {
		issuedRecords = append(issuedRecords, issuedRecordOf(i))
	}
	s.Count("issued", len(issuedRecords))
	if err := s.AddJSON("issued.json", issuedRecords); err != nil {
		return err
	}

	held, err := w.store.ListHeld(ctx, orgID)
	if err != nil {
		return fmt.Errorf("export: reading held attestations: %w", err)
	}
	heldRecords := make([]heldRecord, 0, len(held))
	carried := 0
	for _, h := range held {
		record, ok, err := w.credential(ctx, orgID, h, s)
		if err != nil {
			return err
		}
		if ok {
			carried++
		}
		heldRecords = append(heldRecords, record)
	}
	s.Count("held", len(heldRecords))
	s.Count("credentials", carried)
	if err := s.AddJSON("held.json", heldRecords); err != nil {
		return err
	}

	schemas, err := w.store.ListSchemas(ctx, orgID)
	if err != nil {
		return fmt.Errorf("export: reading attestation schemas: %w", err)
	}
	schemaRecords := make([]schemaRecord, 0, len(schemas))
	for _, sc := range schemas {
		schemaRecords = append(schemaRecords, schemaRecordOf(sc))
	}
	s.Count("schemas", len(schemaRecords))
	if err := s.AddJSON("schemas.json", schemaRecords); err != nil {
		return err
	}

	templates, err := w.store.ListTemplates(ctx, orgID)
	if err != nil {
		return fmt.Errorf("export: reading attestation templates: %w", err)
	}
	templateRecords := make([]templateRecord, 0, len(templates))
	for _, t := range templates {
		templateRecords = append(templateRecords, templateRecordOf(t))
	}
	s.Count("templates", len(templateRecords))
	if err := s.AddJSON("templates.json", templateRecords); err != nil {
		return err
	}

	keys, err := w.store.ListKeys(ctx, orgID)
	if err != nil {
		return fmt.Errorf("export: reading attestation keys: %w", err)
	}
	keyRecords := make([]keyRecord, 0, len(keys))
	for _, k := range keys {
		keyRecords = append(keyRecords, keyRecordOf(k))
	}
	s.Count("keys", len(keyRecords))
	return s.AddJSON("keys.json", keyRecords)
}

// credential carries one held credential's material and returns the index row
// pointing at it. A credential the engine cannot return is recorded as an
// omission rather than failing the export: the held row is still the org's
// record that it holds one.
func (w *AttestationsWriter) credential(ctx context.Context, orgID uuid.UUID, h attestation.HeldAttestation, s *SectionBundle) (heldRecord, bool, error) {
	record := heldRecordOf(h)
	if h.CredentialRef == "" {
		return record, false, nil
	}

	name := "credentials/" + h.CredentialRef + sdJwtExtension
	raw, err := w.holder.Raw(ctx, orgID, h.CredentialRef)
	if errors.Is(err, eudiholder.ErrCredentialNotFound) {
		s.Omit(name, ReasonUnavailable, 0, nil)
		return record, false, nil
	}
	if err != nil {
		return heldRecord{}, false, fmt.Errorf("export: reading held credential %s: %w", h.CredentialRef, err)
	}
	// AddBytes copies the issuer-signed token verbatim; re-encoding it would
	// break the signature it carries.
	if err := s.AddBytes(name, sdJwtMediaType, raw); err != nil {
		return heldRecord{}, false, err
	}

	record.Format = sdJwtFormat
	record.Path = s.path(name)
	return record, true, nil
}

// issuedRecord is one row of the Art 5(1)(m) issuance ledger. The recipient's
// wallet holds the signed EAA; we hold this row, so there is no credential file
// on the issued side. claimToken, offerUri, IssuanceID and CredentialUUID are
// left out: each is a live handle on this deployment or its issuer, not the
// org's data.
type issuedRecord struct {
	ID              uuid.UUID         `json:"id"`
	TemplateID      *uuid.UUID        `json:"templateId,omitempty"`
	SchemaVCT       string            `json:"schemaVct"`
	RecipientKind   string            `json:"recipientKind"`
	RecipientUserID *uuid.UUID        `json:"recipientUserId,omitempty"`
	RecipientRef    string            `json:"recipientRef"`
	Attributes      map[string]string `json:"attributes"`
	Qualified       bool              `json:"qualified"`
	Status          string            `json:"status"`
	Delivery        string            `json:"delivery"`
	IssuedByUserID  *uuid.UUID        `json:"issuedByUserId,omitempty"`
	ClaimedAt       *string           `json:"claimedAt,omitempty"`
	ExpiresAt       *string           `json:"expiresAt,omitempty"`
	RevokedAt       *string           `json:"revokedAt,omitempty"`
	CancelledAt     *string           `json:"cancelledAt,omitempty"`
	CreatedAt       string            `json:"createdAt"`
	UpdatedAt       string            `json:"updatedAt"`
}

func issuedRecordOf(i attestation.Issued) issuedRecord {
	return issuedRecord{
		ID:              i.ID,
		TemplateID:      i.TemplateID,
		SchemaVCT:       i.SchemaVCT,
		RecipientKind:   i.RecipientKind,
		RecipientUserID: i.RecipientUserID,
		RecipientRef:    i.RecipientRef,
		Attributes:      i.Attributes,
		Qualified:       i.Qualified,
		Status:          i.Status,
		Delivery:        i.Delivery,
		IssuedByUserID:  i.IssuedByUserID,
		ClaimedAt:       optionalTimestamp(i.ClaimedAt),
		ExpiresAt:       optionalTimestamp(i.ExpiresAt),
		RevokedAt:       optionalTimestamp(i.RevokedAt),
		CancelledAt:     optionalTimestamp(i.CancelledAt),
		CreatedAt:       timestamp(i.CreatedAt),
		UpdatedAt:       timestamp(i.UpdatedAt),
	}
}

// heldRecord indexes one credential the org holds. Format and Path are empty
// when the bundle carries no material for it — the manifest's omitted list then
// says why.
type heldRecord struct {
	ID              uuid.UUID  `json:"id"`
	CredentialRef   string     `json:"credentialRef"`
	VCT             string     `json:"vct"`
	Issuer          string     `json:"issuer"`
	Source          string     `json:"source"`
	SourceMessageID *uuid.UUID `json:"sourceMessageId,omitempty"`
	Format          string     `json:"format,omitempty"`
	Path            string     `json:"path,omitempty"`
	ReceivedAt      string     `json:"receivedAt"`
}

func heldRecordOf(h attestation.HeldAttestation) heldRecord {
	return heldRecord{
		ID:            h.ID,
		CredentialRef: h.CredentialRef,
		VCT:           h.VCT,
		Issuer:        h.Issuer,
		Source:        h.Source,
		// The cross-link to the QERDS message that delivered it, so a held
		// credential can be tied to its own delivery evidence.
		SourceMessageID: h.SourceMessageID,
		ReceivedAt:      timestamp(h.ReceivedAt),
	}
}

// schemaRecord carries a referenced credential definition so a receiver can
// interpret the ledger's attribute keys. LogoURI is left out: it is an API path
// into this deployment.
type schemaRecord struct {
	ID                 uuid.UUID                   `json:"id"`
	VCT                string                      `json:"vct"`
	DisplayName        string                      `json:"displayName"`
	CredentialConfigID string                      `json:"credentialConfigId"`
	SubjectType        string                      `json:"subjectType"`
	Attributes         []attestation.AttributeDef  `json:"attributes"`
	Display            []attestation.LocalizedName `json:"display,omitempty"`
	Qualified          bool                        `json:"qualified"`
	Status             string                      `json:"status"`
	CreatedAt          string                      `json:"createdAt"`
	UpdatedAt          string                      `json:"updatedAt"`
}

func schemaRecordOf(s attestation.Schema) schemaRecord {
	return schemaRecord{
		ID:                 s.ID,
		VCT:                s.VCT,
		DisplayName:        s.DisplayName,
		CredentialConfigID: s.CredentialConfigID,
		SubjectType:        s.SubjectType,
		Attributes:         s.Attributes,
		Display:            s.Display,
		Qualified:          s.Qualified,
		Status:             s.Status,
		CreatedAt:          timestamp(s.CreatedAt),
		UpdatedAt:          timestamp(s.UpdatedAt),
	}
}

// templateRecord carries a referenced issuance template. IssuedCount is a
// derived display figure and stays out; the ledger itself is the count.
type templateRecord struct {
	ID                uuid.UUID         `json:"id"`
	SchemaID          uuid.UUID         `json:"schemaId"`
	Name              string            `json:"name"`
	DefaultAttributes map[string]string `json:"defaultAttributes,omitempty"`
	AttributeSources  map[string]string `json:"attributeSources,omitempty"`
	ValiditySeconds   *int              `json:"validitySeconds,omitempty"`
	KeyMaterialID     *uuid.UUID        `json:"keyMaterialId,omitempty"`
	Status            string            `json:"status"`
	VCT               string            `json:"vct"`
	CreatedAt         string            `json:"createdAt"`
	UpdatedAt         string            `json:"updatedAt"`
}

func templateRecordOf(t attestation.Template) templateRecord {
	return templateRecord{
		ID:                t.ID,
		SchemaID:          t.SchemaID,
		Name:              t.Name,
		DefaultAttributes: t.DefaultAttributes,
		AttributeSources:  t.AttributeSources,
		ValiditySeconds:   t.ValiditySeconds,
		KeyMaterialID:     t.KeyMaterialID,
		Status:            t.Status,
		VCT:               t.VCT,
		CreatedAt:         timestamp(t.CreatedAt),
		UpdatedAt:         timestamp(t.UpdatedAt),
	}
}

// keyRecord names a signing key, never its material. ProviderRef is a handle
// into the hosted issuer or QTSP key store and is exported deliberately: a
// receiver needs it to correlate with the provider's own records. Private key
// material is not in the database at all and must never be fetched for an export.
type keyRecord struct {
	ID          uuid.UUID `json:"id"`
	Kind        string    `json:"kind"`
	Label       string    `json:"label"`
	ProviderRef string    `json:"providerRef,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   string    `json:"createdAt"`
	UpdatedAt   string    `json:"updatedAt"`
}

func keyRecordOf(k attestation.Key) keyRecord {
	return keyRecord{
		ID:          k.ID,
		Kind:        k.Kind,
		Label:       k.Label,
		ProviderRef: k.ProviderRef,
		Status:      k.Status,
		CreatedAt:   timestamp(k.CreatedAt),
		UpdatedAt:   timestamp(k.UpdatedAt),
	}
}
