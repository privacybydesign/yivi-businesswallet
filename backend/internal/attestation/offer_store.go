package attestation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Credential-offer lifecycle: an inbound QERDS offer waits as pending until an
// admin accepts it (redeemed into the org's holder engine) or declines it. Both
// decisions are terminal — a declined offer is not re-queued when the message is
// re-delivered, because the org already said no.
const (
	OfferPending  = "pending"
	OfferAccepted = "accepted"
	OfferDeclined = "declined"
)

// CredentialOffer is an OpenID4VCI credential offer that arrived over QERDS and
// is waiting for an organization to accept or decline it.
//
// The offer deeplink itself is NOT part of the JSON: it is a bearer token, so
// serving it to the console would hand any member a credential the org has not
// accepted. Accepting replays it server-side instead.
type CredentialOffer struct {
	ID              uuid.UUID `json:"id"`
	OrganizationID  uuid.UUID `json:"organizationId"`
	SourceMessageID uuid.UUID `json:"sourceMessageId"`
	SenderOrgName   string    `json:"senderOrgName"`
	SenderAddress   string    `json:"senderAddress"`
	FromParty       string    `json:"fromParty"`
	CredentialName  string    `json:"credentialName"`
	// Offer is the openid-credential-offer:// deeplink. See the type comment for
	// why it stays out of the API response.
	Offer      string     `json:"-"`
	Status     string     `json:"status"`
	DecidedAt  *time.Time `json:"decidedAt,omitempty"`
	ReceivedAt time.Time  `json:"receivedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// OfferInput records a newly received credential offer in the queue.
type OfferInput struct {
	SourceMessageID uuid.UUID
	SenderOrgName   string
	SenderAddress   string
	FromParty       string
	CredentialName  string
	Offer           string
}

const offerColumns = `id, organization_id, source_message_id, sender_org_name, sender_address,
	from_party, credential_name, credential_offer, status, decided_at, received_at, created_at`

func scanOffer(row rowScanner) (CredentialOffer, error) {
	var o CredentialOffer
	if err := row.Scan(&o.ID, &o.OrganizationID, &o.SourceMessageID, &o.SenderOrgName, &o.SenderAddress,
		&o.FromParty, &o.CredentialName, &o.Offer, &o.Status, &o.DecidedAt, &o.ReceivedAt, &o.CreatedAt); err != nil {
		return CredentialOffer{}, err
	}
	return o, nil
}

// RecordOffer queues an inbound credential offer for a decision. It is idempotent
// on the source QERDS message: a re-delivered message resolves to the row already
// queued — recorded is false and the offer comes back in whatever state the
// organization left it, so a decision already made is never resurrected as
// pending.
func (s *Store) RecordOffer(ctx context.Context, orgID uuid.UUID, in OfferInput) (CredentialOffer, bool, error) {
	const insert = `INSERT INTO credential_offers
		(organization_id, source_message_id, sender_org_name, sender_address, from_party,
		 credential_name, credential_offer, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (organization_id, source_message_id) DO NOTHING
		RETURNING ` + offerColumns
	o, err := scanOffer(s.db.QueryRow(ctx, insert, orgID, in.SourceMessageID, in.SenderOrgName,
		in.SenderAddress, in.FromParty, in.CredentialName, in.Offer, OfferPending))
	if err == nil {
		return o, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CredentialOffer{}, false, fmt.Errorf("attestation: record offer org %s: %w", orgID, err)
	}

	// DO NOTHING returns no row, so the already-queued one is read back here
	// rather than handed to the caller as a zero value it could mistake for an
	// offer (the same contract qerds.Store.CreateInbound honours on a re-delivery).
	const existing = `SELECT ` + offerColumns + ` FROM credential_offers
		WHERE organization_id = $1 AND source_message_id = $2`
	o, err = scanOffer(s.db.QueryRow(ctx, existing, orgID, in.SourceMessageID))
	if err != nil {
		return CredentialOffer{}, false, fmt.Errorf("attestation: read queued offer for message %s org %s: %w",
			in.SourceMessageID, orgID, err)
	}
	return o, false, nil
}

// ListPendingOffers returns the offers an organization still has to decide on,
// newest first. Decided offers are not listed: an accepted one is visible as a
// held credential, a declined one only in the audit log.
func (s *Store) ListPendingOffers(ctx context.Context, orgID uuid.UUID) ([]CredentialOffer, error) {
	const query = `SELECT ` + offerColumns + ` FROM credential_offers
		WHERE organization_id = $1 AND status = $2
		ORDER BY received_at DESC`
	rows, err := s.db.Query(ctx, query, orgID, OfferPending)
	if err != nil {
		return nil, fmt.Errorf("attestation: list offers org %s: %w", orgID, err)
	}
	defer rows.Close()

	offers := []CredentialOffer{}
	for rows.Next() {
		o, err := scanOffer(rows)
		if err != nil {
			return nil, fmt.Errorf("attestation: list offers scan: %w", err)
		}
		offers = append(offers, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("attestation: list offers rows: %w", err)
	}
	return offers, nil
}

// GetPendingOffer returns one offer still awaiting a decision. An offer that is
// absent or already decided is ErrOfferNotFound: to the console both mean "there
// is nothing here to act on".
func (s *Store) GetPendingOffer(ctx context.Context, orgID, id uuid.UUID) (CredentialOffer, error) {
	const query = `SELECT ` + offerColumns + ` FROM credential_offers
		WHERE id = $1 AND organization_id = $2 AND status = $3`
	o, err := scanOffer(s.db.QueryRow(ctx, query, id, orgID, OfferPending))
	if errors.Is(err, pgx.ErrNoRows) {
		return CredentialOffer{}, ErrOfferNotFound
	}
	if err != nil {
		return CredentialOffer{}, fmt.Errorf("attestation: get offer %s org %s: %w", id, orgID, err)
	}
	return o, nil
}

// AcceptOffer marks a pending offer accepted and indexes the credential the
// caller redeemed from it, in one tx — so an accepted offer and the held row it
// produced can never disagree. The status guard is what makes a double accept
// safe: the second one finds no pending row and returns ErrOfferNotFound.
func (s *Store) AcceptOffer(ctx context.Context, orgID, id uuid.UUID, in HeldInput) (HeldAttestation, error) {
	var out HeldAttestation
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE credential_offers SET status = $3, decided_at = now(), updated_at = now()
			WHERE id = $1 AND organization_id = $2 AND status = $4
			RETURNING ` + offerColumns
		offer, err := scanOffer(q.QueryRow(ctx, update, id, orgID, OfferAccepted, OfferPending))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOfferNotFound
		}
		if err != nil {
			return fmt.Errorf("attestation: accept offer %s org %s: %w", id, orgID, err)
		}

		const insert = `INSERT INTO held_attestations
			(organization_id, credential_ref, vct, issuer, source, source_message_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING ` + heldColumns
		out, err = scanHeld(q.QueryRow(ctx, insert,
			orgID, in.CredentialRef, in.VCT, in.Issuer, in.Source, in.SourceMessageID))
		if err != nil {
			return fmt.Errorf("attestation: record held for offer %s org %s: %w", id, orgID, err)
		}

		return s.audit.Record(ctx, q, audit.AttestationOfferAccepted,
			audit.Target{Type: audit.TargetCredentialOffer, ID: offer.ID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{
				"status": offer.Status, "sender": offer.SenderOrgName,
				"credentialName": offer.CredentialName, "vct": out.VCT,
			}))
	})
	return out, err
}

// DeclineOffer marks a pending offer declined and audits, in one tx. Nothing is
// redeemed, so the credential never enters the wallet. Returns ErrOfferNotFound
// when the offer is absent or already decided.
func (s *Store) DeclineOffer(ctx context.Context, orgID, id uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE credential_offers SET status = $3, decided_at = now(), updated_at = now()
			WHERE id = $1 AND organization_id = $2 AND status = $4
			RETURNING ` + offerColumns
		offer, err := scanOffer(q.QueryRow(ctx, update, id, orgID, OfferDeclined, OfferPending))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOfferNotFound
		}
		if err != nil {
			return fmt.Errorf("attestation: decline offer %s org %s: %w", id, orgID, err)
		}
		return s.audit.Record(ctx, q, audit.AttestationOfferDeclined,
			audit.Target{Type: audit.TargetCredentialOffer, ID: offer.ID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{
				"status": offer.Status, "sender": offer.SenderOrgName,
				"credentialName": offer.CredentialName,
			}))
	})
}
