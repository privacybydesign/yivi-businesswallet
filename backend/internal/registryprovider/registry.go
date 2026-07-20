package registryprovider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Reject reasons carried in the audit trail of a non-validated consult.
const (
	reasonUnknownKVK         = "unknown_kvk"
	reasonNotARepresentative = "not_a_representative"
)

// SeededRegistry is an in-process KVK stand-in for local dev and tests, backed by
// a seeded dataset of known registrations (the "fake API"). Unlike a blanket
// mock, it validates: it matches the requester's identification data against the
// register's authorised representatives, records every match/no-match decision on
// the KVK register's own audit log, and bounces anything it cannot validate. It
// proves the anti-fraud property of the design (KVK decides who may act for a
// company) end-to-end. A real KVK/BRIS driver would deliver the attestation over
// QERDS instead; see .ai/features/wallet-bootstrap.md §6.2.
type SeededRegistry struct {
	data     Dataset
	db       database.DB
	recorder audit.Recorder
}

// NewSeededRegistry returns a dataset-backed registry that audits its decisions
// against the seeded KVK register organisation (resolved by RegisterKVKNumber).
func NewSeededRegistry(db database.DB, recorder audit.Recorder) *SeededRegistry {
	return &SeededRegistry{data: DefaultDataset(), db: db, recorder: recorder}
}

// Ping is the boot readiness probe. The in-process registry is always ready.
func (*SeededRegistry) Ping(context.Context) error { return nil }

// Consult looks the company up by KVK number and matches the requester's
// identification data against its representatives. A validated match returns the
// attestation with RequesterIsRepresentative set; an unknown KVK number returns
// ErrUnknownKVK; a known company with non-matching identity returns the
// attestation with RequesterIsRepresentative false. Every outcome is audited.
func (s *SeededRegistry) Consult(ctx context.Context, req ConsultRequest) (RegistrationAttestation, error) {
	reg, known := s.data[req.KVKNumber]
	if !known {
		s.audit(ctx, req, "", false, reasonUnknownKVK)
		return RegistrationAttestation{}, ErrUnknownKVK
	}

	att := RegistrationAttestation{
		KVKNumber:       reg.KVKNumber,
		LegalName:       reg.LegalName,
		EUID:            reg.EUID,
		Representatives: reg.Representatives,
		IssuedAt:        s.now(),
	}
	idx, ok := reg.match(req)
	if !ok {
		s.audit(ctx, req, reg.LegalName, false, reasonNotARepresentative)
		return att, nil
	}
	att.RequesterIsRepresentative = true
	att.RequesterRepresentativeIndex = idx
	s.audit(ctx, req, reg.LegalName, true, "")
	return att, nil
}

// now returns the attestation issue time. It is a method so tests can keep the
// field deterministic without reaching for a clock abstraction the stub predates.
func (*SeededRegistry) now() time.Time { return time.Now().UTC() }

// audit records the KVK-side decision on the register organisation's audit log:
// the KVK number and identification data consulted, and the outcome (validated /
// not validated + reason). Best-effort — a failed audit write must not fail a
// consult (mirrors the wallet's best-effort attestation deposit) — but it is
// logged, since the decision trail is the point of the exercise.
func (s *SeededRegistry) audit(ctx context.Context, req ConsultRequest, legalName string, validated bool, reason string) {
	action := audit.KVKRegistrationNotValidated
	if validated {
		action = audit.KVKRegistrationValidated
	}
	after := map[string]any{
		"kvkNumber":   req.KVKNumber,
		"givenNames":  req.GivenNames,
		"familyName":  req.FamilyName,
		"dateOfBirth": req.DateOfBirth,
		"outcome":     outcome(validated),
	}
	if legalName != "" {
		after["legalName"] = legalName
	}
	if reason != "" {
		after["reason"] = reason
	}

	target := audit.Target{Type: audit.TargetKVKRegistration, ID: req.KVKNumber}
	if orgID, err := s.registerOrgID(ctx); err != nil {
		slog.WarnContext(ctx, "registryprovider: resolve kvk register org for audit failed",
			slog.String("kvkNumber", req.KVKNumber), slog.String("error", err.Error()))
	} else {
		target.OrgID = &orgID
	}
	if err := s.recorder.Record(ctx, s.db, action, target, audit.Created(after)); err != nil {
		slog.ErrorContext(ctx, "registryprovider: record kvk decision failed",
			slog.String("kvkNumber", req.KVKNumber), slog.String("error", err.Error()))
	}
}

// registerOrgID resolves the seeded KVK register organisation the decisions are
// audited against.
func (s *SeededRegistry) registerOrgID(ctx context.Context) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT id FROM organizations WHERE kvk_number = $1`, RegisterKVKNumber).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, fmt.Errorf("kvk register organisation (kvk %s) not seeded", RegisterKVKNumber)
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("query kvk register organisation: %w", err)
	}
	return id, nil
}

func outcome(validated bool) string {
	if validated {
		return "validated"
	}
	return "not_validated"
}
