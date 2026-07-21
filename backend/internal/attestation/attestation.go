// Package attestation is the domain slice for issuing Electronic Attestations of
// Attributes (EAAs) — COM(2025) 838 Art 5(1)(a),(f),(g),(h), Art 6(1)(a). An
// organization defines credential schemas and issuance templates, then issues
// attestations to members or external parties. It orchestrates persistence
// (schemas, templates, keys, the issuance ledger), the hosted issuer seam
// (internal/openid4vciissuer) and auditing behind an org-scoped API.
// See .ai/features/attestations.md.
package attestation

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrSchemaNotFound        = errors.New("attestation: schema not found")
	ErrSchemaVctTaken        = errors.New("attestation: schema vct already exists")
	ErrTemplateNotFound      = errors.New("attestation: template not found")
	ErrKeyNotFound           = errors.New("attestation: key not found")
	ErrIssuedNotFound        = errors.New("attestation: issued attestation not found")
	ErrNotOfferable          = errors.New("attestation: attestation is not in an offerable state")
	ErrClaimNotFound         = errors.New("attestation: claim not found")
	ErrHeldNotFound          = errors.New("attestation: held attestation not found")
	ErrRecipientKindMismatch = errors.New("attestation: recipient kind does not match the schema subject type")

	// ErrUnknownAttribute and ErrMissingAttribute enforce data minimisation
	// (Art 5(1)(b)): the schema's attribute list is the allow-list.
	ErrUnknownAttribute = errors.New("attestation: attribute not declared by the schema")
	ErrMissingAttribute = errors.New("attestation: required attribute missing")
)

// LocalizedName and LocalizedLabel model the SD-JWT VC type metadata `display`
// arrays: a BCP-47 language tag paired with the text a wallet renders for that
// language. The credential display uses `name`; a claim's display uses `label`.
type LocalizedName struct {
	Lang string `json:"lang"`
	Name string `json:"name"`
}

type LocalizedLabel struct {
	Lang  string `json:"lang"`
	Label string `json:"label"`
}

// AttributeDef is one field in a schema's attribute allow-list. Display carries
// optional per-language labels for wallets showing the claim in that language.
type AttributeDef struct {
	Key      string           `json:"key"`
	Label    string           `json:"label"`
	Type     string           `json:"type"`
	Required bool             `json:"required"`
	Display  []LocalizedLabel `json:"display,omitempty"`
}

// Attribute value types the schema editor offers and the API accepts. Kept in
// sync with the frontend's SUPPORTED_ATTRIBUTE_TYPES: any other value is
// rejected on write so only valid SD-JWT VC claim types reach a credential.
const (
	AttributeTypeString  = "string"
	AttributeTypeInteger = "integer"
	AttributeTypeNumber  = "number"
	AttributeTypeBoolean = "boolean"
	AttributeTypeDate    = "date"
)

// SupportedAttributeTypes is the allow-list for AttributeDef.Type, in the order
// the editor's dropdown offers them.
var SupportedAttributeTypes = []string{
	AttributeTypeString,
	AttributeTypeInteger,
	AttributeTypeNumber,
	AttributeTypeBoolean,
	AttributeTypeDate,
}

func isSupportedAttributeType(t string) bool {
	for _, s := range SupportedAttributeTypes {
		if s == t {
			return true
		}
	}
	return false
}

// Schema is a credential-type definition an organization can issue.
type Schema struct {
	ID                 uuid.UUID       `json:"id"`
	OrganizationID     uuid.UUID       `json:"organizationId"`
	VCT                string          `json:"vct"`
	DisplayName        string          `json:"displayName"`
	CredentialConfigID string          `json:"credentialConfigId"`
	SubjectType        string          `json:"subjectType"`
	Attributes         []AttributeDef  `json:"attributes"`
	Display            []LocalizedName `json:"display,omitempty"`
	Qualified          bool            `json:"qualified"`
	Status             string          `json:"status"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`
}

// Key is a signing key-material reference (never the private key itself).
type Key struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organizationId"`
	Kind           string    `json:"kind"`
	Label          string    `json:"label"`
	ProviderRef    string    `json:"providerRef,omitempty"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// Template is a named issuance preset over a schema, enriched for the list view
// with the schema's identity + attribute chips and the count issued so far.
type Template struct {
	ID                uuid.UUID         `json:"id"`
	OrganizationID    uuid.UUID         `json:"organizationId"`
	SchemaID          uuid.UUID         `json:"schemaId"`
	Name              string            `json:"name"`
	DefaultAttributes map[string]string `json:"defaultAttributes,omitempty"`
	ValiditySeconds   *int              `json:"validitySeconds,omitempty"`
	KeyMaterialID     *uuid.UUID        `json:"keyMaterialId,omitempty"`
	Status            string            `json:"status"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`

	// Joined schema identity + issuance count, for the Templates tab cards.
	VCT         string         `json:"vct"`
	DisplayName string         `json:"displayName"`
	SubjectType string         `json:"subjectType"`
	Attributes  []AttributeDef `json:"attributes"`
	Qualified   bool           `json:"qualified"`
	IssuedCount int            `json:"issuedCount"`
}

// TemplateDetail is what the issue flow needs: the template plus the resolved
// schema fields required to build the credential offer.
type TemplateDetail struct {
	ID                 uuid.UUID
	OrganizationID     uuid.UUID
	Name               string
	DefaultAttributes  map[string]string
	ValiditySeconds    *int
	SchemaVCT          string
	CredentialConfigID string
	SubjectType        string
	SchemaAttributes   []AttributeDef
	Qualified          bool
}

// Issued is one row of the issuance ledger (the Issued tab / Art 5(1)(m) log).
type Issued struct {
	ID              uuid.UUID         `json:"id"`
	OrganizationID  uuid.UUID         `json:"organizationId"`
	TemplateID      *uuid.UUID        `json:"templateId,omitempty"`
	SchemaVCT       string            `json:"schemaVct"`
	RecipientKind   string            `json:"recipientKind"`
	RecipientUserID *uuid.UUID        `json:"recipientUserId,omitempty"`
	RecipientRef    string            `json:"recipientRef"`
	Attributes      map[string]string `json:"attributes"`
	Qualified       bool              `json:"qualified"`
	Status          string            `json:"status"`
	Delivery        string            `json:"delivery"`
	IssuanceID      string            `json:"-"`
	// CredentialUUID is the hosted issuer's handle for the minted credential,
	// captured on claim. It keys the issuer's status-list revocation and never
	// leaves the backend.
	CredentialUUID string     `json:"-"`
	IssuedByUserID *uuid.UUID `json:"issuedByUserId,omitempty"`
	ClaimedAt      *time.Time `json:"claimedAt,omitempty"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	RevokedAt      *time.Time `json:"revokedAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// Recipient identifies who an attestation is issued to.
type Recipient struct {
	Kind   string
	UserID *uuid.UUID
	Ref    string
}

// IssueInput is the validated request to issue one attestation.
type IssueInput struct {
	TemplateID uuid.UUID
	Recipient  Recipient
	Attributes map[string]string
}

// IssueResult is the ledger row plus the wallet offer to render immediately in
// the issuing UI (and, for remote recipients, delivered by e-mail / QERDS).
type IssueResult struct {
	Issued
	OfferURI string `json:"offerUri"`
	TxCode   string `json:"txCode,omitempty"`
}

// ClaimView is the public, recipient-facing view of an offer, resolved by its
// opaque claim token (never the row id). It carries what the claim page renders.
type ClaimView struct {
	Status           string `json:"status"`
	OfferURI         string `json:"offerUri"`
	TxCode           string `json:"txCode,omitempty"`
	OrganizationName string `json:"organizationName"`
	CredentialName   string `json:"credentialName"`
}

// Recipient kinds: a member or external e-mail are natural persons; organization
// is a business wallet addressed by its QERDS digital address.
const (
	RecipientMember       = "member"
	RecipientExternal     = "external"
	RecipientOrganization = "organization"
)

// Subject types (who the credential is about); drives the delivery route.
const (
	SubjectNaturalPerson = "natural_person"
	SubjectOrganization  = "organization"
)

// Delivery channels for a created offer.
const (
	DeliveryNone  = "none"
	DeliveryEmail = "email"
	DeliveryQerds = "qerds"
)
