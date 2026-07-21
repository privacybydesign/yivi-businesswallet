package attestation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vciissuer"
)

const claimTokenBytes = 24

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
	CreateOffered(ctx context.Context, orgID uuid.UUID, in IssueInput, detail TemplateDetail, issuedBy uuid.UUID, expiresAt *time.Time, claimToken, delivery string) (Issued, error)
	SetOffer(ctx context.Context, orgID, id uuid.UUID, issuanceID, offerURI, txCode string) error
	MarkFailed(ctx context.Context, orgID, id uuid.UUID) error
	MarkClaimed(ctx context.Context, orgID, id uuid.UUID, credentialUUID string) (Issued, error)
	GetIssued(ctx context.Context, orgID, id uuid.UUID) (Issued, error)
	Revoke(ctx context.Context, orgID, id uuid.UUID) (Issued, error)
	GetClaim(ctx context.Context, token string) (claimRow, error)
}

// heldMutator is the held-index surface the service coordinates for the
// engine-backed delete flow (read the ref, then soft-delete the audited index
// row). Backed by the attestation store.
type heldMutator interface {
	GetHeld(ctx context.Context, orgID, id uuid.UUID) (HeldAttestation, error)
	SoftDeleteHeld(ctx context.Context, orgID, id uuid.UUID) error
}

// credentialRemover removes a credential from the organization's holder engine.
// Backed by internal/eudiholder; accept the interface so the service stays
// decoupled from the concrete engine (stub or irmago).
type credentialRemover interface {
	Delete(ctx context.Context, orgID uuid.UUID, ref string) error
}

// emailNotifier delivers a person-facing "your credential is ready" e-mail.
type emailNotifier interface {
	SendCredentialOffer(ctx context.Context, orgID uuid.UUID, to, orgName, credentialName, claimURL, txCode string) error
}

// qerdsNotifier delivers an organization-facing OpenID4VCI credential offer over
// QERDS to a digital address. offerURI is the self-contained
// openid-credential-offer:// deeplink the receiver redeems via its holder flow —
// not a claim link.
type qerdsNotifier interface {
	SendCredentialOffer(ctx context.Context, orgID uuid.UUID, toAddress, orgName, credentialName, offerURI string) error
}

// Service coordinates issuance across the ledger store, the hosted issuer and the
// two recipient-delivery channels (e-mail for people, QERDS for organizations).
type Service struct {
	store      issuedStore
	issuer     issuer
	instances  issuerInstanceResolver
	email      emailNotifier
	qerds      qerdsNotifier
	held       heldMutator
	holder     credentialRemover
	appBaseURL string
	now        func() time.Time
}

func NewService(store issuedStore, iss issuer, instances issuerInstanceResolver, email emailNotifier, qerds qerdsNotifier, held heldMutator, holder credentialRemover, appBaseURL string) *Service {
	return &Service{
		store:      store,
		issuer:     iss,
		instances:  instances,
		email:      email,
		qerds:      qerds,
		held:       held,
		holder:     holder,
		appBaseURL: strings.TrimRight(appBaseURL, "/"),
		now:        time.Now,
	}
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
func (s *Service) Issue(ctx context.Context, orgID, issuedBy uuid.UUID, orgName string, in IssueInput) (IssueResult, error) {
	detail, err := s.store.GetTemplateDetail(ctx, orgID, in.TemplateID)
	if err != nil {
		return IssueResult{}, err
	}
	if err := checkRecipientKind(detail.SubjectType, in.Recipient.Kind); err != nil {
		return IssueResult{}, err
	}
	if err := validateAttributes(detail.SchemaAttributes, in.Attributes); err != nil {
		return IssueResult{}, err
	}

	expirationSeconds := defaultExpirationSeconds
	if detail.ValiditySeconds != nil {
		expirationSeconds = *detail.ValiditySeconds
	}
	var expiresAt *time.Time
	if expirationSeconds > 0 {
		t := s.now().Add(time.Duration(expirationSeconds) * time.Second)
		expiresAt = &t
	}

	claimToken, err := newClaimToken()
	if err != nil {
		return IssueResult{}, err
	}
	delivery := deliveryFor(detail.SubjectType)

	issued, err := s.store.CreateOffered(ctx, orgID, in, detail, issuedBy, expiresAt, claimToken, delivery)
	if err != nil {
		return IssueResult{}, err
	}

	offer, err := s.issuer.CreateOffer(ctx, openid4vciissuer.OfferRequest{
		Instance:           s.instanceFor(ctx, orgID),
		CredentialConfigID: detail.CredentialConfigID,
		Claims:             toClaims(in.Attributes),
		ExpirationSeconds:  expirationSeconds,
		// tx_code is a second factor only for the external-email path (a person
		// keys the PIN in). Members redeem while authenticated; organizations
		// auto-redeem over the authenticated QERDS channel — a tx_code there has
		// no one to enter it and would block automated issuance (see
		// .ai/features/oid4vci-over-qerds.md §4).
		UseTxCode: in.Recipient.Kind == RecipientExternal,
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

	s.deliver(ctx, orgID, orgName, detail.Name, offer, in.Recipient, claimToken)

	return IssueResult{Issued: issued, OfferURI: offer.OfferURI, TxCode: offer.TxCode}, nil
}

// deliver routes the offer to the recipient. Errors are logged, never fatal.
func (s *Service) deliver(ctx context.Context, orgID uuid.UUID, orgName, credentialName string, offer openid4vciissuer.Offer, recipient Recipient, claimToken string) {
	claimURL := s.appBaseURL + "/claim/" + claimToken
	switch recipient.Kind {
	case RecipientOrganization:
		// Organizations receive the real OpenID4VCI offer (their wallet redeems it
		// automatically over the secure channel), not a human claim link.
		if err := s.qerds.SendCredentialOffer(ctx, orgID, recipient.Ref, orgName, credentialName, offer.OfferURI); err != nil {
			slog.ErrorContext(ctx, "attestation: qerds offer delivery failed",
				slog.String("recipient", recipient.Ref), slog.String("error", err.Error()))
		}
	default: // member / external → natural person → e-mail
		if err := s.email.SendCredentialOffer(ctx, orgID, recipient.Ref, orgName, credentialName, claimURL, offer.TxCode); err != nil {
			slog.ErrorContext(ctx, "attestation: email offer delivery failed",
				slog.String("recipient", recipient.Ref), slog.String("error", err.Error()))
		}
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

// Revoke revokes an issued attestation (Art 6(2)). It flips the credential's bit
// on the issuer's Token Status List first, then the local ledger — so a local
// "revoked" flag is never set without the published status list also marking it
// revoked (the two must not drift). Only a claimed credential has a published
// uuid to revoke; an offered/failed row has nothing published, so the local flip
// stands alone. If the issuer reports no status-list bit for the credential (a
// deployment issuing without a Token Status List), the revoke degrades to a
// local-only flip — there is nothing published that could drift. Any other
// issuer failure leaves the local state untouched and surfaces the error (the
// same fail-safe, external-first ordering as DeleteHeld).
func (s *Service) Revoke(ctx context.Context, orgID, id uuid.UUID) (Issued, error) {
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
	return s.store.Revoke(ctx, orgID, id)
}

// isRevocable reports whether an attestation in this status can still be revoked
// (mirrors the store's transition guard).
func isRevocable(status string) bool {
	return status == StatusOffered || status == StatusClaimed
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

func deliveryFor(subjectType string) string {
	if subjectType == SubjectOrganization {
		return DeliveryQerds
	}
	return DeliveryEmail
}

// validateAttributes enforces the schema allow-list: every provided key must be
// declared, and every required attribute must be present and non-empty.
func validateAttributes(allowed []AttributeDef, provided map[string]string) error {
	byKey := make(map[string]AttributeDef, len(allowed))
	for _, a := range allowed {
		byKey[a.Key] = a
	}
	for key := range provided {
		if _, ok := byKey[key]; !ok {
			return fmt.Errorf("%w: %q", ErrUnknownAttribute, key)
		}
	}
	for _, a := range allowed {
		if a.Required && provided[a.Key] == "" {
			return fmt.Errorf("%w: %q", ErrMissingAttribute, a.Key)
		}
	}
	return nil
}

func toClaims(attrs map[string]string) map[string]any {
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

func newClaimToken() (string, error) {
	b := make([]byte, claimTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("attestation: claim token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
