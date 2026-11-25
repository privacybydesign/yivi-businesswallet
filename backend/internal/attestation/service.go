package attestation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation/rulebook"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vciissuer"
)

const claimTokenBytes = 24

// releaseClaimTimeout bounds reopening an offer whose redemption failed. It is a
// single row on a context of its own, so it needs no more than this; the point of
// the bound is that a detached context cannot be cancelled by the acceptance that
// just failed (see Service.releaseClaim).
const releaseClaimTimeout = 5 * time.Second

// listReconcileBudget bounds the issuer calls one Issued-tab load may spend, so a
// slow issuer cannot keep the whole ledger from loading.
const listReconcileBudget = 5 * time.Second

// issuer is the hosted-issuer seam the service orchestrates (see
// internal/openid4vciissuer). Accept the interface; the concrete client/stub is
// injected at boot.
type issuer interface {
	CreateOffer(ctx context.Context, req openid4vciissuer.OfferRequest) (openid4vciissuer.Offer, error)
	Status(ctx context.Context, instance, issuanceID string) (openid4vciissuer.IssuanceStatus, error)
	// RevokeCredential flips the credential's bit on the issuer's Token Status
	// List, so a verifier observes the revocation.
	RevokeCredential(ctx context.Context, instance, credentialUUID string) error
}

// issuerInstanceResolver resolves an organization's Veramo issuer instance name
// (the {instance} path segment) so offers route to that org's issuer. Backed by
// the issuersettings store; an empty result means "use the configured default".
type issuerInstanceResolver interface {
	InstanceFor(ctx context.Context, orgID uuid.UUID) (string, error)
}

// issuedStore is the ledger surface the service coordinates; reads for the API go
// through the store directly from the handler.
type issuedStore interface {
	GetTemplateDetail(ctx context.Context, orgID, id uuid.UUID) (TemplateDetail, error)
	CreateOffered(ctx context.Context, orgID uuid.UUID, in IssueInput, detail TemplateDetail, issuedBy *uuid.UUID, expiresAt *time.Time, claimToken, delivery string) (Issued, error)
	SetOffer(ctx context.Context, orgID, id uuid.UUID, issuanceID, offerURI, txCode string) error
	MarkFailed(ctx context.Context, orgID, id uuid.UUID) error
	MarkClaimed(ctx context.Context, orgID, id uuid.UUID, credentialUUID string) (Issued, error)
	GetIssued(ctx context.Context, orgID, id uuid.UUID) (Issued, error)
	ListIssued(ctx context.Context, orgID uuid.UUID) ([]Issued, error)
	Revoke(ctx context.Context, orgID, id uuid.UUID, reason string) (Issued, error)
	Cancel(ctx context.Context, orgID, id uuid.UUID) (Issued, error)
	GetClaim(ctx context.Context, token string) (claimRow, error)
}

// heldMutator is the held-index surface the service coordinates for the
// engine-backed delete flow (read the ref, then soft-delete the audited index
// row). Backed by the attestation store.
type heldMutator interface {
	ListHeld(ctx context.Context, orgID uuid.UUID) ([]HeldAttestation, error)
	GetHeld(ctx context.Context, orgID, id uuid.UUID) (HeldAttestation, error)
	SoftDeleteHeld(ctx context.Context, orgID, id uuid.UUID) error
	RecordHeldStatusChange(ctx context.Context, orgID, id uuid.UUID, vct string, status HeldStatus) (bool, error)
	HeldHistory(ctx context.Context, orgID, id uuid.UUID) ([]audit.Event, error)
	HolderOrgs(ctx context.Context) ([]uuid.UUID, error)
}

// offerStore is the pending-offer queue the service coordinates for the
// accept/decline flow. Backed by the attestation store.
type offerStore interface {
	ListPendingOffers(ctx context.Context, orgID uuid.UUID) ([]CredentialOffer, error)
	ClaimOffer(ctx context.Context, orgID, id uuid.UUID) (CredentialOffer, error)
	ReleaseOffer(ctx context.Context, orgID, id uuid.UUID) error
	AcceptOffer(ctx context.Context, orgID, id uuid.UUID, in HeldInput) (HeldAttestation, error)
	DeclineOffer(ctx context.Context, orgID, id uuid.UUID) (CredentialOffer, error)
}

// holderEngine is the organization's holder-wallet engine seam the service
// coordinates: redeeming an accepted credential offer, removing a credential
// (the delete flow) and reading a held credential's disclosed attributes (the
// detail view). Backed by internal/eudiholder; accept the interface so the
// service stays decoupled from the concrete engine (stub or irmago).
type holderEngine interface {
	Redeem(ctx context.Context, orgID uuid.UUID, offerURI string) (eudiholder.Redeemed, error)
	Delete(ctx context.Context, orgID uuid.UUID, ref string) error
	Claims(ctx context.Context, orgID uuid.UUID, ref, vct, lang string) (eudiholder.HeldCredential, error)
	Displays(ctx context.Context, orgID uuid.UUID, lang string) (map[string]eudiholder.HeldDisplay, error)
	Validities(ctx context.Context, orgID uuid.UUID) (map[string]eudiholder.HeldValidity, error)
	RefreshStatuses(ctx context.Context, orgID uuid.UUID) (int, error)
	RefreshStatus(ctx context.Context, orgID uuid.UUID, ref string) (int, error)
	Payload(ctx context.Context, orgID uuid.UUID, ref string) (map[string]any, error)
}

// emailNotifier delivers a person-facing "your credential is ready" e-mail.
type emailNotifier interface {
	SendCredentialOffer(ctx context.Context, orgID uuid.UUID, to, orgName, credentialName, claimURL, txCode string) error
	// SendCredentialRevoked tells a natural-person holder that a credential was
	// revoked, and why.
	SendCredentialRevoked(ctx context.Context, orgID uuid.UUID, to, orgName, credentialName, reason string) error
}

// qerdsNotifier delivers an organization-facing OpenID4VCI credential offer over
// QERDS to a digital address. offerURI is the self-contained
// openid-credential-offer:// deeplink the receiver redeems via its holder flow —
// not a claim link.
type qerdsNotifier interface {
	SendCredentialOffer(ctx context.Context, orgID uuid.UUID, toAddress, orgName, credentialName, offerURI string) error
	// SendOfferDeclined tells the issuer at toAddress that its offer was declined.
	SendOfferDeclined(ctx context.Context, orgID uuid.UUID, toAddress, credentialName, offerURI string) error
	// SendCredentialRevoked tells the holder at toAddress that a credential it
	// was issued has been revoked, and why.
	SendCredentialRevoked(ctx context.Context, orgID uuid.UUID, toAddress, credentialName, reason string) error
}

// Service coordinates issuance across the ledger store, the hosted issuer and the
// two recipient-delivery channels (e-mail for people, QERDS for organizations).
type Service struct {
	store       issuedStore
	issuer      issuer
	instances   issuerInstanceResolver
	email       emailNotifier
	qerds       qerdsNotifier
	held        heldMutator
	offers      offerStore
	holder      holderEngine
	appBaseURL  string
	now         func() time.Time
	statusLists StatusListPolicy
	heldFresh   heldFreshness
	ownerID     ownerIDRevoker
}

// ownerIDRevoker acts on the org's owner identification data (the EBWOID its
// wallet opened with) being revoked by its issuer.
type ownerIDRevoker interface {
	OwnerIDRevoked(ctx context.Context, orgID uuid.UUID) error
}

// SetOwnerIDRevoker sets who acts when a status re-check finds the org's EBWOID
// revoked. Without it nothing happens beyond the audit.
func (s *Service) SetOwnerIDRevoker(r ownerIDRevoker) { s.ownerID = r }

func NewService(store issuedStore, iss issuer, instances issuerInstanceResolver, email emailNotifier, qerds qerdsNotifier, held heldMutator, offers offerStore, holder holderEngine, appBaseURL string) *Service {
	return &Service{
		store:      store,
		issuer:     iss,
		instances:  instances,
		email:      email,
		qerds:      qerds,
		held:       held,
		offers:     offers,
		holder:     holder,
		appBaseURL: strings.TrimRight(appBaseURL, "/"),
		now:        time.Now,
		heldFresh: heldFreshness{
			checked: map[uuid.UUID]time.Time{},
			running: map[uuid.UUID]chan struct{}{},
		},
	}
}

// StatusListPolicy says which credentials this deployment's issuers can put on a
// Token Status List, which a credential valid over 24 hours needs (CS-10).
type StatusListPolicy struct {
	// Enforce refuses what needs a status list the issuer lacks. Off on a local stack.
	Enforce bool
	// Default is true when the default issuer puts every credential on a status list.
	Default bool
	// RulebookRouted is true when rulebook credentials go to their own issuer, which
	// ignores the template validity and issues for as long as its own configuration
	// says. Rulebook says whether that issuer puts them on a status list.
	RulebookRouted bool
	Rulebook       bool
}

// SetStatusListPolicy sets what CheckValidity refuses. Without it nothing is refused.
func (s *Service) SetStatusListPolicy(p StatusListPolicy) { s.statusLists = p }

// CheckValidity refuses a template this deployment may not issue: one whose
// credentials live over 24 hours without a status list to revoke them on.
func (s *Service) CheckValidity(validitySeconds *int, rulebook bool) error {
	p := s.statusLists
	if !p.Enforce {
		return nil
	}
	if rulebook && p.RulebookRouted {
		if !p.Rulebook {
			return ErrRulebookStatusListRequired
		}
		return nil
	}
	if !p.Default && needsStatusList(validitySeconds) {
		return ErrStatusListRequired
	}
	return nil
}

// instanceFor resolves the org's issuer instance so offers route to that org's
// issuer. A resolution error is non-fatal: it logs and returns "" so the client
// falls back to its configured default instance.
func (s *Service) instanceFor(ctx context.Context, orgID uuid.UUID) string {
	instance, err := s.instances.InstanceFor(ctx, orgID)
	if err != nil {
		slog.WarnContext(ctx, "attestation: resolve issuer instance failed; using default",
			slog.String("orgId", orgID.String()), slog.String("error", err.Error()))
		return ""
	}
	return instance
}

// Issue validates the request, persists an offered ledger row (audited), asks the
// issuer to create the credential offer, then delivers it to the recipient by the
// channel the schema's subject type dictates: e-mail for a natural person, QERDS
// for an organization. Delivery failures are non-fatal — the offer still exists
// and the issuing UI shows its QR — but they are logged.
// issuedBy is the admin who issued the attestation, or nil for a
// system-initiated issuance (the onboarding auto-issue path, which has no admin
// actor); the ledger's issued_by_user_id is nullable to record that.
func (s *Service) Issue(ctx context.Context, orgID uuid.UUID, issuedBy *uuid.UUID, orgName string, in IssueInput) (IssueResult, error) {
	detail, err := s.store.GetTemplateDetail(ctx, orgID, in.TemplateID)
	if err != nil {
		return IssueResult{}, err
	}
	if err := checkRecipientKind(detail.SubjectType, in.Recipient.Kind); err != nil {
		return IssueResult{}, err
	}
	if err := validateAttributes(detail, in.Attributes); err != nil {
		return IssueResult{}, err
	}
	if detail.SchemaRulebook == "" && detail.SubjectType == SubjectOrganization {
		identifier, _ := normalizeIdentifier(in.Attributes[IdentifierClaim])
		in.Attributes = maps.Clone(in.Attributes)
		in.Attributes[IdentifierClaim] = identifier
	}
	// Also here: a template saved before the rule, or by another deployment.
	if err := s.CheckValidity(detail.ValiditySeconds, detail.SchemaRulebook != ""); err != nil {
		return IssueResult{}, err
	}

	expirationSeconds := expirationSecondsFor(detail.ValiditySeconds)
	var expiresAt *time.Time
	if expirationSeconds > 0 {
		t := s.now().Add(time.Duration(expirationSeconds) * time.Second)
		expiresAt = &t
	}

	claimToken, err := newClaimToken()
	if err != nil {
		return IssueResult{}, err
	}
	delivery := resolveDelivery(detail.SubjectType, in.DeliveryMethod)

	issued, err := s.store.CreateOffered(ctx, orgID, in, detail, issuedBy, expiresAt, claimToken, delivery)
	if err != nil {
		return IssueResult{}, err
	}

	offer, err := s.issuer.CreateOffer(ctx, openid4vciissuer.OfferRequest{
		Instance:           s.instanceFor(ctx, orgID),
		CredentialConfigID: detail.CredentialConfigID,
		VCT:                detail.SchemaVCT,
		DisplayName:        detail.Name,
		IssuerName:         orgName,
		Claims:             maps.Clone(in.Attributes),
		ExpirationSeconds:  expirationSeconds,
		// tx_code is a second factor only for the external-email path (a person
		// keys the PIN in). Members redeem while authenticated; an organization
		// redeems from its console over the authenticated QERDS channel — a
		// tx_code there has no one to enter it and would block the accept (see
		// .ai/features/oid4vci-over-qerds.md §4).
		UseTxCode:   in.Recipient.Kind == RecipientExternal,
		AwaitRedeem: in.Recipient.Kind == RecipientOrganization,
		Rulebook:    detail.SchemaRulebook != "",
	})
	if err != nil {
		if failErr := s.store.MarkFailed(ctx, orgID, issued.ID); failErr != nil {
			slog.ErrorContext(ctx, "attestation: mark failed after offer error",
				slog.String("id", issued.ID.String()), slog.String("error", failErr.Error()))
		}
		return IssueResult{}, fmt.Errorf("attestation: create offer: %w", err)
	}

	if err := s.store.SetOffer(ctx, orgID, issued.ID, offer.IssuanceID, offer.OfferURI, offer.TxCode); err != nil {
		return IssueResult{}, err
	}
	issued.IssuanceID = offer.IssuanceID

	s.deliver(ctx, orgID, orgName, detail.Name, offer, in.Recipient, claimToken, delivery)

	return IssueResult{Issued: issued, OfferURI: offer.OfferURI, TxCode: offer.TxCode}, nil
}

// deliver routes the offer over the resolved delivery channel. Errors are logged,
// never fatal. DeliveryNone (the "show QR directly" choice) sends nothing — the
// issuing UI shows the QR.
func (s *Service) deliver(ctx context.Context, orgID uuid.UUID, orgName, credentialName string, offer openid4vciissuer.Offer, recipient Recipient, claimToken, delivery string) {
	switch delivery {
	case DeliveryQerds:
		// Organizations receive the real OpenID4VCI offer (their wallet redeems it
		// automatically over the secure channel), not a human claim link.
		if err := s.qerds.SendCredentialOffer(ctx, orgID, recipient.Ref, orgName, credentialName, offer.OfferURI); err != nil {
			slog.ErrorContext(ctx, "attestation: qerds offer delivery failed",
				slog.String("recipient", recipient.Ref), slog.String("error", err.Error()))
		}
	case DeliveryEmail:
		claimURL := s.appBaseURL + "/claim/" + claimToken
		if err := s.email.SendCredentialOffer(ctx, orgID, recipient.Ref, orgName, credentialName, claimURL, offer.TxCode); err != nil {
			slog.ErrorContext(ctx, "attestation: email offer delivery failed",
				slog.String("recipient", recipient.Ref), slog.String("error", err.Error()))
		}
	case DeliveryNone:
		// "Show QR directly" — nothing to send; the issuing UI renders the QR.
	}
}

// Status returns the current ledger row, reconciling an offered attestation with
// the issuer: if the recipient has claimed the credential, it transitions to
// claimed (audited). Poll transitions are idempotent.
func (s *Service) Status(ctx context.Context, orgID, id uuid.UUID) (Issued, error) {
	issued, err := s.store.GetIssued(ctx, orgID, id)
	if err != nil {
		return Issued{}, err
	}
	if issued.Status != StatusOffered || issued.IssuanceID == "" {
		return issued, nil
	}
	if credentialUUID, ok := s.reconcile(ctx, s.instanceFor(ctx, orgID), issued.IssuanceID); ok {
		return s.store.MarkClaimed(ctx, orgID, id, credentialUUID)
	}
	return issued, nil
}

// ListIssued returns the issuance ledger, first reconciling every open offer to an
// organization with the issuer. An organization accepts an offer in its own
// wallet, so only the issuer can tell this side that it was claimed.
func (s *Service) ListIssued(ctx context.Context, orgID uuid.UUID) ([]Issued, error) {
	issued, err := s.store.ListIssued(ctx, orgID)
	if err != nil {
		return nil, err
	}
	reconcileCtx, cancel := context.WithTimeout(ctx, listReconcileBudget)
	defer cancel()
	instance, resolved := "", false
	for i, row := range issued {
		if row.Status != StatusOffered || row.IssuanceID == "" || row.RecipientKind != RecipientOrganization {
			continue
		}
		if reconcileCtx.Err() != nil {
			break
		}
		if !resolved {
			instance, resolved = s.instanceFor(ctx, orgID), true
		}
		credentialUUID, ok := s.reconcile(reconcileCtx, instance, row.IssuanceID)
		if !ok {
			continue
		}
		claimed, err := s.store.MarkClaimed(ctx, orgID, row.ID, credentialUUID)
		if errors.Is(err, ErrIssuedNotFound) {
			// Claimed, cancelled or revoked by a concurrent request since the read.
			continue
		}
		if err != nil {
			return nil, err
		}
		issued[i] = claimed
	}
	return issued, nil
}

// ClaimStatus is the public, token-keyed view of an offer for the recipient's
// claim page, reconciling with the issuer so the page can poll to claimed.
func (s *Service) ClaimStatus(ctx context.Context, token string) (ClaimView, error) {
	c, err := s.store.GetClaim(ctx, token)
	if err != nil {
		return ClaimView{}, err
	}
	status := c.status
	if status == StatusOffered && c.issuanceID != "" {
		if credentialUUID, ok := s.reconcile(ctx, s.instanceFor(ctx, c.orgID), c.issuanceID); ok {
			if _, err := s.store.MarkClaimed(ctx, c.orgID, c.id, credentialUUID); err != nil {
				return ClaimView{}, err
			}
			status = StatusClaimed
		}
	}
	return ClaimView{
		Status:           status,
		OfferURI:         c.offerURI,
		TxCode:           c.txCode,
		OrganizationName: c.orgName,
		CredentialName:   c.credentialName,
	}, nil
}

// Revoke revokes an already-claimed attestation (Art 6(2)). Offers that were
// never claimed are withdrawn via Cancel instead. It flips the credential's bit
// on the issuer's Token Status List first, then the local ledger — so a local
// "revoked" flag is never set without the published status list also marking it
// revoked (the two must not drift). If the issuer reports no status-list bit for
// the credential (a deployment issuing without a Token Status List), the revoke
// degrades to a local-only flip — there is nothing published that could drift.
// Any other issuer failure leaves the local state untouched and surfaces the
// error (the same fail-safe, external-first ordering as DeleteHeld).
func (s *Service) Revoke(ctx context.Context, orgID, id uuid.UUID, in RevokeInput) (Issued, error) {
	issued, err := s.store.GetIssued(ctx, orgID, id)
	if err != nil {
		return Issued{}, err
	}
	switch {
	case issued.CredentialUUID != "" && isRevocable(issued.Status):
		switch err := s.issuer.RevokeCredential(ctx, s.instanceFor(ctx, orgID), issued.CredentialUUID); {
		case errors.Is(err, openid4vciissuer.ErrNoStatusListBit):
			// The deployment issues without a Token Status List (enableStatusLists
			// off / no statusLists block), so the issuer reserved no bit to flip.
			// Degrade to a local-only revocation: the local ledger records it, and
			// there is nothing published that could drift. Matches StubIssuer,
			// whose RevokeCredential is a graceful no-op success.
			slog.WarnContext(ctx, "attestation: issuer has no status-list bit for the credential; revoking locally only",
				slog.String("id", id.String()))
		case err != nil:
			return Issued{}, fmt.Errorf("attestation: revoke on issuer status list: %w", err)
		}
	case issued.CredentialUUID == "" && issued.Status == StatusClaimed:
		// A claimed credential with no captured uuid predates status-list backing
		// (issued before this column existed): the local ledger can still record
		// the revocation, but the status list cannot be updated.
		slog.WarnContext(ctx, "attestation: revoking claimed attestation with no issuer credential uuid; status list not updated",
			slog.String("id", id.String()))
	}
	revoked, err := s.store.Revoke(ctx, orgID, id, in.Reason)
	if err != nil {
		return Issued{}, err
	}
	s.notifyRevoked(ctx, orgID, in.IssuerName, revoked)
	return revoked, nil
}

// notifyRevoked tells the holder that its credential was revoked and why
// (CS-10 ISS-RV-09): an organization over QERDS, whose wallet then re-reads the
// status list, a person by e-mail. Failures are logged: the status list already
// says revoked.
func (s *Service) notifyRevoked(ctx context.Context, orgID uuid.UUID, issuerName string, issued Issued) {
	if issued.RecipientRef == "" {
		return
	}
	name := issued.SchemaVCT
	if issued.TemplateID != nil {
		if detail, err := s.store.GetTemplateDetail(ctx, orgID, *issued.TemplateID); err == nil {
			name = detail.Name
		}
	}
	var err error
	switch {
	case issued.RecipientKind == RecipientOrganization && s.qerds != nil:
		err = s.qerds.SendCredentialRevoked(ctx, orgID, issued.RecipientRef, name, issued.RevocationReason)
	case issued.RecipientKind != RecipientOrganization && s.email != nil:
		err = s.email.SendCredentialRevoked(ctx, orgID, issued.RecipientRef, issuerName, name, issued.RevocationReason)
	}
	if err != nil {
		slog.ErrorContext(ctx, "attestation: revocation notice failed",
			slog.String("id", issued.ID.String()), slog.String("recipient", issued.RecipientRef), slog.String("error", err.Error()))
	}
}

// isRevocable reports whether an attestation in this status can still be revoked
// (mirrors the store's transition guard: only a claimed credential is revocable;
// an unclaimed offer is withdrawn via Cancel).
func isRevocable(status string) bool {
	return status == StatusClaimed
}

// Cancel withdraws an unclaimed offer. Nothing was ever held, so this is a
// cancellation, not a revocation, and never publishes to the Token Status List.
func (s *Service) Cancel(ctx context.Context, orgID, id uuid.UUID) (Issued, error) {
	return s.store.Cancel(ctx, orgID, id)
}

// DeleteHeld removes a held credential the organization no longer wants to keep
// (Art 5(1)(a) "store, select"): it deletes the live credential from the holder
// engine first, then soft-deletes the audited index row. Engine-first ordering
// means a failed engine delete aborts before the index is touched, so a
// "removed" credential is never left presentable in the wallet. Returns
// ErrHeldNotFound when the row is absent or already deleted.
func (s *Service) DeleteHeld(ctx context.Context, orgID, id uuid.UUID) error {
	held, err := s.held.GetHeld(ctx, orgID, id)
	if err != nil {
		return err
	}
	if err := s.holder.Delete(ctx, orgID, held.CredentialRef); err != nil {
		return fmt.Errorf("attestation: delete held %s from engine: %w", id, err)
	}
	return s.held.SoftDeleteHeld(ctx, orgID, id)
}

// ListOffers returns the inbound credential offers the organization still has to
// decide on. The offer deeplinks stay server-side (see CredentialOffer).
func (s *Service) ListOffers(ctx context.Context, orgID uuid.UUID) ([]CredentialOffer, error) {
	return s.offers.ListPendingOffers(ctx, orgID)
}

// AcceptOffer redeems a pending credential offer into the organization's holder
// engine and indexes it as held (source=qerds, linked to the QERDS message that
// carried the offer). This is the only path that puts a QERDS-delivered
// credential in the wallet: receiving the offer merely queues it.
//
// Claim, redeem, settle. The claim (ClaimOffer, pending → accepting) comes first
// because redeeming is a call to the issuer with a side effect no later guard can
// undo: were the offer merely read here, two admins pressing Accept at the same
// moment would both redeem, and the one that lost the settle would leave a
// credential in the org's holder engine that the wallet can neither show nor
// delete. One caller wins the claim; the rest get ErrOfferNotFound.
//
// Engine first, index second — the same fail-safe ordering as DeleteHeld. A
// redemption that fails releases the claim, so the offer is pending again and the
// admin can accept once the cause is gone (the issuer was down, the org's wallet
// was not yet activated) rather than losing the offer to a terminal state.
// Settling is the point of no return: a second accept finds nothing pending and
// returns ErrOfferNotFound.
func (s *Service) AcceptOffer(ctx context.Context, orgID, id uuid.UUID) (HeldAttestation, error) {
	offer, err := s.offers.ClaimOffer(ctx, orgID, id)
	if err != nil {
		return HeldAttestation{}, err
	}
	redeemed, err := s.holder.Redeem(ctx, orgID, offer.Offer)
	if err != nil {
		s.releaseClaim(ctx, orgID, id)
		return HeldAttestation{}, fmt.Errorf("attestation: redeem accepted offer %s org %s: %w", id, orgID, err)
	}
	messageID := offer.SourceMessageID
	held, err := s.offers.AcceptOffer(ctx, orgID, id, HeldInput{
		CredentialRef:   redeemed.Ref,
		VCT:             redeemed.VCT,
		Issuer:          redeemed.Issuer,
		Source:          HeldSourceQERDS,
		SourceMessageID: &messageID,
	})
	if err != nil {
		return HeldAttestation{}, err
	}
	slog.InfoContext(ctx, "attestation: accepted QERDS credential offer",
		slog.String("orgId", orgID.String()),
		slog.String("offerId", id.String()),
		slog.String("vct", redeemed.VCT),
		slog.String("messageId", messageID.String()))
	return held, nil
}

// releaseClaim reopens an offer whose redemption produced no credential. It is
// called on that path only: after a redemption that did succeed, releasing would
// invite a retry that redeems a second credential, so an offer whose indexing
// failed stays claimed and out of the queue.
//
// The release runs on a context detached from the request's. The redemption most
// likely to fail is the one whose context was cancelled or timed out, and on that
// context the write that reopens the offer would fail too — stranding the offer
// as accepting: gone from the Wallet tab and undecidable for good. A release that
// fails anyway is logged rather than returned, because the redemption error is
// what the admin needs to see.
func (s *Service) releaseClaim(ctx context.Context, orgID, id uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseClaimTimeout)
	defer cancel()
	if err := s.offers.ReleaseOffer(ctx, orgID, id); err != nil {
		slog.ErrorContext(ctx, "attestation: reopening a credential offer after a failed redemption",
			slog.String("orgId", orgID.String()),
			slog.String("offerId", id.String()),
			slog.String("error", err.Error()))
	}
}

// DeclineOffer refuses a pending credential offer. Nothing is redeemed, so the
// credential never reaches the wallet; the QERDS message and its evidence stay in
// the inbox. Returns ErrOfferNotFound when the offer is absent or already decided.
// The decline is told back to the issuer over QERDS; the issuer cannot learn it
// any other way, since nothing is redeemed. A failed send is logged, not
// returned: the decline itself is recorded and cannot be repeated.
func (s *Service) DeclineOffer(ctx context.Context, orgID, id uuid.UUID) error {
	offer, err := s.offers.DeclineOffer(ctx, orgID, id)
	if err != nil {
		return err
	}
	if offer.SenderAddress == "" {
		return nil
	}
	if err := s.qerds.SendOfferDeclined(ctx, orgID, offer.SenderAddress, offer.CredentialName, offer.Offer); err != nil {
		slog.ErrorContext(ctx, "attestation: tell issuer of declined offer",
			slog.String("offerId", offer.ID.String()), slog.String("to", offer.SenderAddress),
			slog.String("error", err.Error()))
	}
	return nil
}

// HeldClaimsView is a held credential's index metadata plus its disclosed
// attributes, assembled for the detail view. The index row carries the
// routing/identity fields (vct, issuer identifier, source, receivedAt); the
// attributes and the issuer's human display name are read from the holder engine,
// where the credential material and its metadata live (§6.5). Issuer is the issuer
// identifier (a URL); IssuerName is its display name, falling back to the
// identifier when the credential carried no issuer display metadata. DisplayName /
// LogoURI are the credential's own type-metadata title and logo resolved for the
// request's language (both empty when the credential carried no credential-level
// display metadata, so the frontend falls back to the VCT-derived name and shows
// no logo).
// ExpiresAt / Revoked carry the credential's validity as the holder engine has it
// stored (absent expiry means the credential does not expire); see
// eudiholder.HeldValidity for what Revoked does and does not observe.
type HeldClaimsView struct {
	ID          uuid.UUID  `json:"id"`
	VCT         string     `json:"vct"`
	Issuer      string     `json:"issuer"`
	IssuerName  string     `json:"issuerName"`
	DisplayName string     `json:"displayName"`
	LogoURI     string     `json:"logoUri"`
	Source      string     `json:"source"`
	ReceivedAt  time.Time  `json:"receivedAt"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	Revoked     bool       `json:"revoked"`
	HeldChecks
	Attributes []eudiholder.HeldAttribute `json:"attributes"`
	// Rulebook is the WE BUILD rulebook the credential's vct belongs to, and
	// RulebookViolations where its claims do not match that rulebook's schema.
	Rulebook           string   `json:"rulebook,omitempty"`
	RulebookViolations []string `json:"rulebookViolations,omitempty"`
}

// HeldChecks are what the wallet knows about a held credential's integrity:
// its iat and format, whether it carries a status list (so it can be revoked at
// all) and when that was last read. The signature and the issuer's trust chain
// were verified when it was received; a credential failing either is never
// stored.
type HeldChecks struct {
	IssuedAt        *time.Time `json:"issuedAt,omitempty"`
	Format          string     `json:"format,omitempty"`
	HasStatusList   bool       `json:"hasStatusList"`
	StatusCheckedAt *time.Time `json:"statusCheckedAt,omitempty"`
}

func heldChecks(v eudiholder.HeldValidity) HeldChecks {
	return HeldChecks{IssuedAt: v.IssuedAt, Format: v.Format, HasStatusList: v.HasStatusList, StatusCheckedAt: v.StatusCheckedAt}
}

// HeldListView is one held-credential index row enriched with the credential's
// localized type-metadata title, logo and issuer name (resolved from the holder
// engine for the request's language), so the held-list table can show a friendly,
// localized name, a logo and a translated issuer without a per-row claims fetch.
// DisplayName / LogoURI are empty when the credential carried no credential-level
// display metadata (the frontend then falls back to the VCT-derived name and shows
// no logo). IssuerName is the issuer's display name, falling back to the raw issuer
// identifier (Issuer, a URL) when the credential carried no issuer display metadata —
// the same rule the detail view follows. ExpiresAt / Revoked carry the credential's
// validity as the holder engine has it stored, so the view can badge each
// credential; an absent ExpiresAt means the credential does not expire (or the
// engine does not know this ref). See eudiholder.HeldValidity for what Revoked does
// and does not observe.
type HeldListView struct {
	HeldAttestation
	DisplayName string     `json:"displayName"`
	LogoURI     string     `json:"logoUri"`
	IssuerName  string     `json:"issuerName"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	Revoked     bool       `json:"revoked"`
	HeldChecks
}

// HeldClaims returns a held credential's disclosed attributes for the detail view,
// after confirming the organization holds it (the index row is the source of truth
// for existence). lang is the request's active language (a BCP-47 tag); the
// credential title, attribute labels and issuer name are resolved in it. Returns
// ErrHeldNotFound when the row is absent or already deleted.
func (s *Service) HeldClaims(ctx context.Context, orgID, id uuid.UUID, lang string) (HeldClaimsView, error) {
	held, err := s.held.GetHeld(ctx, orgID, id)
	if err != nil {
		return HeldClaimsView{}, err
	}
	s.refreshHeldIfStale(ctx, orgID)
	cred, err := s.holder.Claims(ctx, orgID, held.CredentialRef, held.VCT, lang)
	if err != nil {
		return HeldClaimsView{}, fmt.Errorf("attestation: held claims %s from engine: %w", id, err)
	}
	issuerName := cred.IssuerName
	if issuerName == "" {
		issuerName = held.Issuer
	}
	// The detail view badges the credential like the list does, so it needs the same
	// validity. The engine answers per org rather than per credential; an org holds
	// few credentials, so the whole map is cheaper than a second engine seam.
	validities, err := s.holder.Validities(ctx, orgID)
	if err != nil {
		return HeldClaimsView{}, fmt.Errorf("attestation: held validity %s from engine: %w", id, err)
	}
	validity := validities[held.CredentialRef]
	payload, err := s.holder.Payload(ctx, orgID, held.CredentialRef)
	if err != nil {
		return HeldClaimsView{}, fmt.Errorf("attestation: held claims %s payload from engine: %w", id, err)
	}
	rulebookID, violations, err := checkRulebook(held.VCT, cred.Attributes, payload)
	if err != nil {
		return HeldClaimsView{}, fmt.Errorf("attestation: held claims %s rulebook check: %w", id, err)
	}
	return HeldClaimsView{
		Rulebook:           rulebookID,
		RulebookViolations: violations,
		ID:                 held.ID,
		VCT:                held.VCT,
		Issuer:             held.Issuer,
		IssuerName:         issuerName,
		DisplayName:        cred.DisplayName,
		LogoURI:            cred.LogoURI,
		Source:             held.Source,
		ReceivedAt:         held.ReceivedAt,
		ExpiresAt:          validity.ExpiresAt,
		Revoked:            validity.Revoked,
		HeldChecks:         heldChecks(validity),
		Attributes:         cred.Attributes,
	}, nil
}

// checkRulebook validates a held credential against the WE BUILD rulebook its vct
// belongs to: the whole signed payload (vct, cnf, status and every claim) when the
// engine holds one, else the disclosed claims. It returns no rulebook for any other vct.
func checkRulebook(vct string, attrs []eudiholder.HeldAttribute, payload map[string]any) (string, []string, error) {
	rb, ok := rulebook.ForVCT(vct)
	if !ok {
		return "", nil, nil
	}
	var err error
	if payload != nil {
		err = rb.ValidatePayload(payload)
	} else {
		claims := make(map[string]any, len(attrs))
		for _, a := range attrs {
			claims[a.Key] = a.Value
		}
		err = rb.ValidateClaims(claims)
	}
	var ve *rulebook.ValidationError
	if errors.As(err, &ve) {
		return rb.ID, ve.Violations, nil
	}
	return rb.ID, nil, err
}

// ListHeld returns an organization's active held credentials enriched with each
// credential's localized type-metadata title and logo (resolved from the holder
// engine for lang, the request's active language) and its stored validity, so the
// held view shows a friendly, localized name, a logo and a status badge without a
// per-row claims fetch. A row keeps empty DisplayName / LogoURI when its credential
// carried no display metadata, and no expiry when the credential does not expire or
// the engine does not know its ref (a row received before refs were captured).
func (s *Service) ListHeld(ctx context.Context, orgID uuid.UUID, lang string) ([]HeldListView, error) {
	held, err := s.held.ListHeld(ctx, orgID)
	if err != nil {
		return nil, err
	}
	s.refreshHeldIfStale(ctx, orgID)
	displays, err := s.holder.Displays(ctx, orgID, lang)
	if err != nil {
		return nil, fmt.Errorf("attestation: held displays org %s from engine: %w", orgID, err)
	}
	validities, err := s.holder.Validities(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("attestation: held validities org %s from engine: %w", orgID, err)
	}
	views := make([]HeldListView, len(held))
	for i, h := range held {
		d := displays[h.VCT]
		v := validities[h.CredentialRef]
		issuerName := d.IssuerName
		if issuerName == "" {
			issuerName = h.Issuer
		}
		views[i] = HeldListView{
			HeldAttestation: h,
			DisplayName:     d.DisplayName,
			LogoURI:         d.LogoURI,
			IssuerName:      issuerName,
			ExpiresAt:       v.ExpiresAt,
			Revoked:         v.Revoked,
			HeldChecks:      heldChecks(v),
		}
	}
	return views, nil
}

// reconcile reports whether the issuer says the credential has been issued, and
// if so the issuer's credential uuid (to store for later revocation). A transient
// issuer error is treated as "not yet" (never fails the poll).
func (s *Service) reconcile(ctx context.Context, instance, issuanceID string) (string, bool) {
	st, err := s.issuer.Status(ctx, instance, issuanceID)
	if err != nil {
		slog.WarnContext(ctx, "attestation: issuer status check failed",
			slog.String("error", err.Error()))
		return "", false
	}
	if st.Status != openid4vciissuer.StatusIssued {
		return "", false
	}
	return st.CredentialUUID, true
}

// checkRecipientKind enforces that the recipient matches the schema subject type.
func checkRecipientKind(subjectType, kind string) error {
	switch subjectType {
	case SubjectOrganization:
		if kind != RecipientOrganization {
			return ErrRecipientKindMismatch
		}
	default: // natural_person
		if kind != RecipientMember && kind != RecipientExternal {
			return ErrRecipientKindMismatch
		}
	}
	return nil
}

// resolveDelivery picks the delivery channel: organizations always go over QERDS;
// a natural person is e-mailed a claim link unless the caller chose the QR method
// (DeliveryNone — shown directly in the UI, no e-mail sent).
func resolveDelivery(subjectType, method string) string {
	if subjectType == SubjectOrganization {
		return DeliveryQerds
	}
	if method == DeliveryMethodQR {
		return DeliveryNone
	}
	return DeliveryEmail
}

// validateAttributes checks the values a template issues: a rulebook schema's
// against the rulebook's JSON schema (rulebook.ErrInvalidClaims), an
// organization-defined schema's against its allow-list, where every declared key
// may be given as a string and every required one must be non-empty.
func validateAttributes(detail TemplateDetail, provided map[string]any) error {
	if detail.SchemaRulebook != "" {
		rb, ok := rulebook.Get(detail.SchemaRulebook)
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownRulebook, detail.SchemaRulebook)
		}
		return rb.ValidateClaims(provided)
	}
	if detail.SubjectType == SubjectOrganization {
		if err := validateIdentifier(provided[IdentifierClaim]); err != nil {
			return err
		}
	}
	return validateOwnAttributes(detail, provided)
}

func validateIdentifier(value any) error {
	if value == nil {
		return fmt.Errorf("%w: %q", ErrMissingAttribute, IdentifierClaim)
	}
	_, err := normalizeIdentifier(value)
	return err
}

func validateOwnAttributes(detail TemplateDetail, provided map[string]any) error {
	byKey := make(map[string]AttributeDef, len(detail.SchemaAttributes))
	for _, a := range detail.SchemaAttributes {
		byKey[a.Key] = a
	}
	for key, value := range provided {
		if key == IdentifierClaim && detail.SubjectType == SubjectOrganization {
			continue
		}
		if _, ok := byKey[key]; !ok {
			return fmt.Errorf("%w: %q", ErrUnknownAttribute, key)
		}
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%w: %q", ErrInvalidAttribute, key)
		}
	}
	for _, a := range detail.SchemaAttributes {
		if v, _ := provided[a.Key].(string); a.Required && v == "" {
			return fmt.Errorf("%w: %q", ErrMissingAttribute, a.Key)
		}
	}
	return nil
}

func newClaimToken() (string, error) {
	b := make([]byte, claimTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("attestation: claim token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// HeldHistory is a held credential's trail, oldest first, after confirming the
// organization holds it.
func (s *Service) HeldHistory(ctx context.Context, orgID, id uuid.UUID) ([]audit.Event, error) {
	if _, err := s.held.GetHeld(ctx, orgID, id); err != nil {
		return nil, err
	}
	return s.held.HeldHistory(ctx, orgID, id)
}

// heldRecheckTimeout bounds a status re-check, which outlives the request
// that started it: see recheckHeld.
const heldRecheckTimeout = 2 * time.Minute

// heldAuditTimeout bounds auditing what a re-check left behind, given its own
// budget so a re-check that ran out of time is still audited.
const heldAuditTimeout = 30 * time.Second

// RecheckHeld re-reads the issuer status list of every credential the
// organization holds and audits each one whose state moved between valid and
// revoked. It reports how many changed.
func (s *Service) RecheckHeld(ctx context.Context, orgID uuid.UUID) (int, error) {
	changed, err := s.recheckHeld(ctx, orgID, func(ctx context.Context) error {
		_, err := s.holder.RefreshStatuses(ctx, orgID)
		return err
	})
	return len(changed), err
}

// RecheckHeldOne re-reads the issuer status list of one held credential, the
// re-check a credential's own page asks for, and reports whether it changed
// state.
func (s *Service) RecheckHeldOne(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	held, err := s.held.GetHeld(ctx, orgID, id)
	if err != nil {
		return false, err
	}
	changed, err := s.recheckHeld(ctx, orgID, func(ctx context.Context) error {
		_, err := s.holder.RefreshStatus(ctx, orgID, held.CredentialRef)
		return err
	})
	_, ok := changed[id]
	return ok, err
}

// recheckHeld runs refresh and then audits every held credential whose status
// differs from its last audited one, returning the ids it audited. It runs
// detached from ctx's cancellation and actor: the holder keeps each status it
// reads as it goes, so a re-check cut off (a closed tab, a timeout) must still
// audit what was kept, and the issuer, not the caller, moved the status.
func (s *Service) recheckHeld(ctx context.Context, orgID uuid.UUID, refresh func(context.Context) error) (map[uuid.UUID]struct{}, error) {
	ctx = audit.WithoutActor(context.WithoutCancel(ctx))

	refreshCtx, cancel := context.WithTimeout(ctx, heldRecheckTimeout)
	refreshErr := refresh(refreshCtx)
	cancel()
	if refreshErr != nil {
		refreshErr = fmt.Errorf("attestation: recheck org %s: %w", orgID, refreshErr)
	}

	auditCtx, cancel := context.WithTimeout(ctx, heldAuditTimeout)
	defer cancel()
	changed, err := s.auditHeldStatuses(auditCtx, orgID)
	return changed, errors.Join(refreshErr, err)
}

// auditHeldStatuses records each held credential with a status list whose
// status in the holder differs from its last audited one. A credential that
// fails is skipped, not the ones after it.
func (s *Service) auditHeldStatuses(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	changed := map[uuid.UUID]struct{}{}
	validities, err := s.holder.Validities(ctx, orgID)
	if err != nil {
		return changed, fmt.Errorf("attestation: validities org %s: %w", orgID, err)
	}
	held, err := s.held.ListHeld(ctx, orgID)
	if err != nil {
		return changed, err
	}

	var errs []error
	for _, h := range held {
		v := validities[h.CredentialRef]
		if !v.HasStatusList {
			continue
		}
		recorded, err := s.held.RecordHeldStatusChange(ctx, orgID, h.ID, h.VCT, heldStatusOf(v.Revoked))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if recorded {
			changed[h.ID] = struct{}{}
		}
		// On every re-check, not only on the change, so a failed revoke is retried.
		if v.Revoked && h.Source == HeldSourceBootstrap && s.ownerID != nil {
			if err := s.ownerID.OwnerIDRevoked(ctx, orgID); err != nil {
				errs = append(errs, fmt.Errorf("attestation: owner id revoked org %s: %w", orgID, err))
			}
		}
	}
	return changed, errors.Join(errs...)
}

// RecheckAllHeld re-checks every holder organization's credentials, for the
// scheduled sweep. An org that fails is logged and skipped. It reports how many
// credentials changed state.
func (s *Service) RecheckAllHeld(ctx context.Context) (int64, error) {
	orgs, err := s.held.HolderOrgs(ctx)
	if err != nil {
		return 0, err
	}
	var changed int64
	for _, orgID := range orgs {
		// Each org's re-check ignores cancellation (see recheckHeld), so a
		// shutdown is honoured here, between orgs.
		if err := ctx.Err(); err != nil {
			return changed, err
		}
		n, err := s.RecheckHeld(ctx, orgID)
		changed += int64(n)
		if err != nil {
			slog.WarnContext(ctx, "attestation: held status recheck failed", slog.String("org_id", orgID.String()), slog.Any("error", err))
		}
	}
	return changed, nil
}
