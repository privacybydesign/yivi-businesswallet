// Package eudiholder is the organization-facing EUDI holder-wallet engine seam
// (the "store, select" side of the wallet, Art 5(1)(a)). It is the holder
// counterpart to internal/openid4vciissuer (the issue side): where the issuer
// seam orchestrates a hosted issuer, this seam owns the credential store the
// organization holds credentials in.
//
// The concrete engine is chosen by config, never code — mirroring the
// issuer/verifier/QERDS seams:
//
//   - StubHolder: an in-process, in-memory engine for local dev / CI
//     (ATTESTATION_HOLDER=stub, the default). No persistence, no irmago.
//   - Engine: the irmago EUDI holder engine backed by PostgreSQL
//     (ATTESTATION_HOLDER=irmago). Each organization gets its own isolated
//     Postgres schema (irmago's holder models carry no tenant column, so
//     isolation is per-schema, not per-row). See .ai/features/attestations.md §6.5.
//
// The credential material (the raw SD-JWT VC) lives in the holder engine; the
// attestation slice keeps only a thin, org-scoped index row (held_attestations)
// pointing at it via the engine's credential-instance id.
package eudiholder

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/common/clientmodels"
)

// ErrNotConfigured is returned by operations that require the real engine when
// the stub is selected and the operation has no meaningful in-memory behaviour.
var ErrNotConfigured = errors.New("eudiholder: engine not configured")

// Holder is the per-organization holder-wallet engine seam. Accept the
// interface; the concrete engine (stub or irmago-backed) is injected at boot.
type Holder interface {
	// Ping verifies the holder subsystem is usable at boot. For the irmago
	// engine it proves the whole path (schema creation + Postgres connect +
	// AutoMigrate) against a reserved probe schema; failure is fatal at startup,
	// mirroring the issuer/verifier probes.
	Ping(ctx context.Context) error

	// Store persists a received credential in the organization's holder engine
	// and returns the engine credential-instance id, which the caller records as
	// held_attestations.credential_ref. Used by the receive flows (§9.5) and the
	// dev seed.
	Store(ctx context.Context, orgID uuid.UUID, cred Credential) (string, error)

	// List returns the organization's held credentials as irmago's display model
	// (common/clientmodels.Credential — the same DTOs the irmamobile wallet
	// renders: localized name, issuer, attributes with display names, logos).
	// This is the holder read/display layer the frontend consumes. The engine
	// reads them straight from the org's storage; the stub synthesises them.
	List(ctx context.Context, orgID uuid.UUID) ([]*clientmodels.Credential, error)

	// Redeem runs the OpenID4VCI holder flow for offerURI — an
	// openid-credential-offer:// deeplink using the pre-authorized-code grant —
	// against the sending org's issuer, verifies and stores the received
	// credential in this org's holder engine, and returns the fields the caller
	// indexes in held_attestations. This is the receive counterpart to the issue
	// side: it is how an OpenID4VCI offer delivered over QERDS (§9.5,
	// .ai/features/oid4vci-over-qerds.md) becomes a held credential.
	Redeem(ctx context.Context, orgID uuid.UUID, offerURI string) (Redeemed, error)

	// Delete removes a credential instance from the organization's holder engine.
	// Deleting an absent ref is a no-op: the held_attestations index is the source
	// of truth for the audit trail (it soft-deletes), while the engine holds the
	// live credential material.
	Delete(ctx context.Context, orgID uuid.UUID, ref string) error

	// Close releases all per-organization engines.
	Close() error
}

// Credential is a credential to persist in the holder engine. The fields mirror
// the parts of irmago's CredentialBatch/IssuedCredentialInstance the engine
// needs; the receive flows (§9.5) populate them from the actual received
// SD-JWT VC, the dev seed synthesises a demo credential.
type Credential struct {
	// VCT is the verifiable-credential type (the vct claim), e.g. "nl.kvk.registration".
	VCT string
	// IssuerURL is the iss claim of the issuer-signed JWT (== credential_issuer).
	IssuerURL string
	// CredentialIssuer is the canonical issuer URL (credential_issuer claim).
	CredentialIssuer string
	// Hash is a deterministic dedup hash over the credential (irmago requires it
	// unique per batch); the caller supplies a stable value.
	Hash string
	// RawToken is the raw SD-JWT VC token (without the key-binding JWT).
	RawToken []byte
	// ProcessedPayload is the JSON-encoded, verified SD-JWT payload.
	ProcessedPayload []byte
	// IssuedAt is the iat claim.
	IssuedAt time.Time
	// ExpiresAt is the exp claim; nil if the credential does not expire.
	ExpiresAt *time.Time
}

// Redeemed is the outcome of redeeming a credential offer: the fields the caller
// records in held_attestations. The credential material itself is already stored
// in the org's holder engine by the time Redeem returns.
type Redeemed struct {
	// Ref is the engine credential-instance id → held_attestations.credential_ref.
	Ref string
	// VCT is the verifiable-credential type of the received credential.
	VCT string
	// Issuer is the credential issuer identifier (for the held index / display).
	Issuer string
}
