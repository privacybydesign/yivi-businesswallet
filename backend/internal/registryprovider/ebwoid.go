package registryprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
)

// EBWOIDVCT is the WE BUILD EBW Owner Identification Data type (rb-ebwoid §5.1).
const EBWOIDVCT = "uri:eu.ebw.oid.1"

// EBWOID claim names (rb-ebwoid §5.1): id is the Art 9 unique identifier (EUID),
// name the official name from the register.
const (
	EBWOIDClaimID   = "id"
	EBWOIDClaimName = "name"
)

// EBWOIDHolder is the holder-engine surface an EBWOID issuer writes into.
type EBWOIDHolder interface {
	Store(ctx context.Context, orgID uuid.UUID, cred eudiholder.Credential) (string, error)
}

// HeldEBWOID is an EBWOID now stored in an org's holder engine.
type HeldEBWOID struct {
	Ref    string
	Issuer string
}

// stubEBWOIDIssuer is the issuer identifier the seeded register's EBWOID carries.
const stubEBWOIDIssuer = "https://kvk.seeded-register.localhost"

// stubEBWOIDLifetime matches the stub holder's received-credential lifetime.
const stubEBWOIDLifetime = 365 * 24 * time.Hour

// StubEBWOIDIssuer stores an EBWOID built from the seeded register's attestation,
// for dev / CI. It is unsigned: nothing outside this wallet should rely on it.
type StubEBWOIDIssuer struct{}

func (StubEBWOIDIssuer) Ping(context.Context) error { return nil }

func (StubEBWOIDIssuer) Obtain(ctx context.Context, h EBWOIDHolder, orgID uuid.UUID, att RegistrationAttestation) (HeldEBWOID, error) {
	payload, err := json.Marshal(map[string]string{
		"vct":                        EBWOIDVCT,
		"iss":                        stubEBWOIDIssuer,
		EBWOIDClaimID:                att.EUID,
		EBWOIDClaimName:              att.LegalName,
		"issuing_authority":          RegisterLegalName,
		"issuing_country":            "NL",
		"attestation_legal_category": "PuB-EAA",
	})
	if err != nil {
		return HeldEBWOID{}, fmt.Errorf("registryprovider: stub ebwoid payload: %w", err)
	}
	issuedAt := time.Now().UTC()
	expiresAt := issuedAt.Add(stubEBWOIDLifetime)
	ref, err := h.Store(ctx, orgID, eudiholder.Credential{
		VCT:              EBWOIDVCT,
		IssuerURL:        stubEBWOIDIssuer,
		CredentialIssuer: stubEBWOIDIssuer,
		Hash:             "ebwoid-" + orgID.String(),
		ProcessedPayload: payload,
		IssuedAt:         issuedAt,
		ExpiresAt:        &expiresAt,
	})
	if err != nil {
		return HeldEBWOID{}, fmt.Errorf("registryprovider: store stub ebwoid: %w", err)
	}
	return HeldEBWOID{Ref: ref, Issuer: stubEBWOIDIssuer}, nil
}
