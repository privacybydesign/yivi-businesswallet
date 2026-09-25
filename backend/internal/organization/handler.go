package organization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

type repository interface {
	List(ctx context.Context) ([]Organization, error)
	GetByID(ctx context.Context, id uuid.UUID) (Organization, error)
	GetBySlug(ctx context.Context, slug string) (Organization, error)
	Update(ctx context.Context, id uuid.UUID, name string) (Organization, error)
	Delete(ctx context.Context, id uuid.UUID) error
	SetDataInstruction(ctx context.Context, orgID uuid.UUID, instruction string) (Organization, error)
	Terminate(ctx context.Context, orgID uuid.UUID, exports exportQueuer) (Organization, error)
	ListForUser(ctx context.Context, userID uuid.UUID) ([]Organization, error)
	GetMembership(ctx context.Context, userID, orgID uuid.UUID) (Membership, error)
	ResolveAuthority(ctx context.Context, orgID, userID uuid.UUID) (Authority, error)
	GetMember(ctx context.Context, orgID, userID uuid.UUID) (Member, error)
	GetMemberAvatar(ctx context.Context, orgID, userID uuid.UUID) (user.Avatar, error)
	ListMemberEntries(ctx context.Context, orgID uuid.UUID, p MemberListParams) ([]MemberEntry, int, error)
	RevokeInvitation(ctx context.Context, orgID, invitationID uuid.UUID) error
	// ResendInvitation rotates the invite token and extends the expiry, returning
	// the refreshed invitation (with its new raw Token) so a fresh link can be
	// e-mailed.
	ResendInvitation(ctx context.Context, orgID, invitationID uuid.UUID) (Invitation, error)
	UpdateMembership(ctx context.Context, orgID, userID uuid.UUID, role *string, jobTitle *string, departmentID *uuid.UUID) (Member, error)
	RemoveMembership(ctx context.Context, orgID, userID uuid.UUID) error
	ListDepartments(ctx context.Context, orgID uuid.UUID) ([]Department, error)
	CreateDepartment(ctx context.Context, orgID uuid.UUID, name string) (Department, error)
	UpdateDepartment(ctx context.Context, orgID, deptID uuid.UUID, name string) (Department, error)
	DeleteDepartment(ctx context.Context, orgID, deptID uuid.UUID) error
	ListMandates(ctx context.Context, orgID uuid.UUID) ([]Mandate, error)
	HasJointRepresentation(ctx context.Context, orgID, userID uuid.UUID) (bool, error)
	GrantMandate(ctx context.Context, orgID, grantorUserID uuid.UUID, req MandateGrant) (Mandate, error)
	RevokeMandate(ctx context.Context, orgID, mandateID, revokedBy uuid.UUID, effectiveAt *time.Time, reason string) ([]Mandate, error)

	UpdateMemberType(ctx context.Context, orgID, userID uuid.UUID, memberType string, externalOrganisation *string) (Member, error)
	RequestIdentification(ctx context.Context, orgID uuid.UUID, userIDs []uuid.UUID, requestedBy uuid.UUID, reason string) ([]RequestedMember, error)
	GetIdentitySettings(ctx context.Context, orgID uuid.UUID) (IdentitySettings, error)
	SaveIdentitySettings(ctx context.Context, orgID uuid.UUID, in IdentitySettingsInput) (IdentitySettings, error)
	ReverifyTokenLookup(ctx context.Context, rawToken string) (ReverifyContext, error)

	GetScreeningSettings(ctx context.Context, orgID uuid.UUID) (ScreeningSettings, error)
	SaveScreeningSettings(ctx context.Context, orgID uuid.UUID, in ScreeningSettingsInput) (ScreeningSettings, error)
	ListScreeningHistory(ctx context.Context, orgID, userID uuid.UUID) ([]ScreeningRecord, error)
	RequestVog(ctx context.Context, orgID uuid.UUID, userIDs []uuid.UUID, requestedBy uuid.UUID, reason string) ([]RequestedVogMember, error)
	ScreeningMatchContext(ctx context.Context, orgID, userID uuid.UUID) (ScreeningMatchContext, error)
	EnsureVogToken(ctx context.Context, orgID, userID uuid.UUID) (string, time.Time, error)
	VogTokenLookup(ctx context.Context, rawToken string) (VogTokenContext, error)
	MemberStatusSnapshots(ctx context.Context, orgID uuid.UUID) ([]MemberStatusSnapshot, error)
}

// screener is the VOG screening seam the handler needs, satisfied by
// *ScreeningService.
type screener interface {
	UploadVog(ctx context.Context, orgID, userID uuid.UUID, checkedBy string, checkedByUserID *uuid.UUID, pdf []byte) (ScreeningOutcome, error)
	StartVogCredentialSession(ctx context.Context, orgID uuid.UUID) (auth.Session, error)
	DiscloseVogCredential(ctx context.Context, orgID, userID uuid.UUID, checkedBy string, checkedByUserID *uuid.UUID, disclosureToken string) (ScreeningOutcome, error)
	StartIdentityVogCredentialSession(ctx context.Context, orgID uuid.UUID) (auth.Session, error)
	DiscloseIdentityAndVogCredential(ctx context.Context, orgID, userID uuid.UUID, checkedBy string, checkedByUserID *uuid.UUID, disclosureToken string) (ScreeningOutcome, error)
}

type inviter interface {
	InviteMember(ctx context.Context, orgID uuid.UUID, in Invite) (Invitation, error)
	PendingInvitation(ctx context.Context, rawToken string) (Invitation, error)
	StartAcceptSession(ctx context.Context, rawToken string) (auth.Session, error)
	StartIdentitySession(ctx context.Context) (auth.Session, error)
	AcceptInvitation(ctx context.Context, rawToken, disclosureToken string) (AcceptOutcome, error)
	DeclineInvitation(ctx context.Context, rawToken string) error
	MyInvitations(ctx context.Context, email user.Email) ([]Invitation, error)
	AcceptInvitationByID(ctx context.Context, invitationID uuid.UUID, disclosureToken string) (AcceptOutcome, error)
	DeclineInvitationForUser(ctx context.Context, invitationID uuid.UUID, email user.Email) error
	ListIdentityReviews(ctx context.Context) ([]IdentityReview, error)
	ResolveIdentityReview(ctx context.Context, reviewID, reviewerID uuid.UUID, approve bool) (ResolveOutcome, error)

	StartReverifySession(ctx context.Context, rawToken string) (auth.Session, error)
	MintOwnReverifyToken(ctx context.Context, orgID, userID uuid.UUID) (string, time.Time, error)
	CompleteReverification(ctx context.Context, rawToken, disclosureToken string) (ReverifyOutcome, error)
	CompleteOwnIdentification(ctx context.Context, orgID, userID uuid.UUID, disclosureToken string) error
}

type auditReader interface {
	ListForOrganization(ctx context.Context, orgID uuid.UUID, after *audit.Cursor, limit int) (audit.Page, error)
	ListForMember(ctx context.Context, orgID, userID uuid.UUID, after *audit.Cursor, limit int) (audit.Page, error)
}

// sessionIssuer logs a member in after they accept an invitation: the accept's
// identity disclosure already proves email ownership, so re-login is redundant.
type sessionIssuer interface {
	Issue(ctx context.Context, w http.ResponseWriter, userID uuid.UUID, idempotencyToken string) error
}

// inviteMailer delivers invitation e-mails. Best-effort: a delivery failure
// never blocks the invite, which is also discoverable in-app. Satisfied by
// *email.Service (kept as a local interface so this slice does not import it).
type inviteMailer interface {
	SendInvitation(ctx context.Context, orgID uuid.UUID, to, orgName, acceptURL string) error
	SendIdentityRequested(ctx context.Context, orgID uuid.UUID, to, orgName, reidentifyURL, reason string) error
	SendVogRequested(ctx context.Context, orgID uuid.UUID, to, orgName, vogURL, reason string) error
}

// defaultAddressResolver resolves an organization's live default QERDS
// sending address (qerds_addresses.is_default), which the Wallet card shows in
// place of the registration-time snapshot on the organization row (#260). Kept
// as a local interface so this slice does not import qerds — which already
// imports organization for org-scoped auth, so the reverse import would
// cycle. Satisfied by an adapter over *qerds.Store, wired in cmd/api/main.go.
type defaultAddressResolver interface {
	// DefaultDigitalAddress returns the organization's default address. ok is
	// false when none is provisioned.
	DefaultDigitalAddress(ctx context.Context, orgID uuid.UUID) (address string, ok bool, err error)
}

// exports queues the bundle a termination owes. Nil disables the route: a
// deployment without the export slice cannot honour Art 7(6)(f), and refusing is
// better than terminating with no handover.
type Handler struct {
	store          repository
	service        inviter
	screening      screener
	reader         auditReader
	issuer         sessionIssuer
	mailer         inviteMailer
	defaultAddress defaultAddressResolver
	appBaseURL     string
	requireUser    func(http.Handler) http.Handler
	admins         auth.PlatformAdmins
	exports        exportQueuer
}

func NewHandler(store repository, service inviter, screening screener, reader auditReader, issuer sessionIssuer, mailer inviteMailer, appBaseURL string, requireUser func(http.Handler) http.Handler, admins auth.PlatformAdmins, defaultAddress defaultAddressResolver, exports exportQueuer) *Handler {
	return &Handler{store: store, service: service, screening: screening, reader: reader, issuer: issuer, mailer: mailer, defaultAddress: defaultAddress, appBaseURL: strings.TrimRight(appBaseURL, "/"), requireUser: requireUser, admins: admins, exports: exports}
}

func (h *Handler) Register(mux *http.ServeMux) {
	platform := func(next http.Handler) http.Handler {
		return h.requireUser(auth.RequirePlatformAdmin(h.admins)(next))
	}
	orgScoped := func(next http.Handler) http.Handler {
		return h.requireUser(h.Authorize(next))
	}

	mux.Handle("GET /organizations", platform(respond.HandlerFunc(h.list)))
	mux.Handle("GET /organizations/{id}", platform(respond.HandlerFunc(h.get)))
	mux.Handle("DELETE /organizations/{id}", platform(respond.HandlerFunc(h.delete)))
	mux.Handle("POST /organizations/{id}/terminate", platform(respond.HandlerFunc(h.terminate)))

	mux.Handle("GET /admin/identity-reviews", platform(respond.HandlerFunc(h.listIdentityReviews)))
	mux.Handle("POST /admin/identity-reviews/{id}/approve", platform(respond.HandlerFunc(h.approveIdentityReview)))
	mux.Handle("POST /admin/identity-reviews/{id}/reject", platform(respond.HandlerFunc(h.rejectIdentityReview)))

	mux.Handle("GET /me/organizations", h.requireUser(respond.HandlerFunc(h.listForUser)))
	mux.Handle("GET /me/invitations", h.requireUser(respond.HandlerFunc(h.myInvitations)))
	mux.Handle("POST /me/invitations/{id}/decline", h.requireUser(respond.HandlerFunc(h.declineMyInvitation)))

	mux.Handle("POST /invitations/session", respond.HandlerFunc(h.startInvitationSession))
	mux.Handle("POST /invitations/{id}/accept", respond.HandlerFunc(h.acceptInvitationByID))

	mux.Handle("GET /invite/{token}", respond.HandlerFunc(h.invitePreview))
	mux.Handle("POST /invite/{token}/session", respond.HandlerFunc(h.startAccept))
	mux.Handle("POST /invite/{token}/accept", respond.HandlerFunc(h.acceptInvite))
	mux.Handle("POST /invite/{token}/decline", respond.HandlerFunc(h.declineInvite))

	// Re-identification (#240): a bearer token, resolved the same way an invite
	// token is, reached either from a reminder/request e-mail or minted for the
	// caller by the in-app banner (POST .../me/reidentify-token below).
	mux.Handle("GET /reidentify/{token}", respond.HandlerFunc(h.reidentifyPreview))
	mux.Handle("POST /reidentify/{token}/session", respond.HandlerFunc(h.startReidentify))
	mux.Handle("POST /reidentify/{token}/complete", respond.HandlerFunc(h.completeReidentify))

	// VOG submission (#242): a bearer token like re-identification's, reached
	// from a request/reminder e-mail or minted for the caller by the dashboard
	// banner (POST .../me/vog-token below), so a member submits without
	// signing in. The pbdf.vog credential paths are gated on the org's opt-in.
	mux.Handle("GET /vog/{token}", respond.HandlerFunc(h.vogPreview))
	mux.Handle("POST /vog/{token}/upload", respond.HandlerFunc(h.uploadSelfVog))
	mux.Handle("POST /vog/{token}/identity-session", respond.HandlerFunc(h.startVogIdentitySession))
	mux.Handle("POST /vog/{token}/identity-complete", respond.HandlerFunc(h.completeVogIdentification))
	mux.Handle("POST /vog/{token}/credential-session", respond.HandlerFunc(h.startVogCredentialSession))
	mux.Handle("POST /vog/{token}/credential-complete", respond.HandlerFunc(h.completeVogCredential))
	mux.Handle("POST /vog/{token}/identity-credential-session", respond.HandlerFunc(h.startIdentityVogCredentialSession))
	mux.Handle("POST /vog/{token}/identity-credential-complete", respond.HandlerFunc(h.completeIdentityVogCredential))

	mux.Handle("GET /orgs/{slug}", orgScoped(respond.HandlerFunc(h.details)))
	mux.Handle("PATCH /orgs/{slug}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.update))))
	mux.Handle("PUT /orgs/{slug}/data-instruction", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.setDataInstruction))))
	mux.Handle("GET /orgs/{slug}/members", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.members))))
	mux.Handle("GET /orgs/{slug}/member-insights", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.memberInsights))))
	mux.Handle("GET /orgs/{slug}/members/{userId}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.member))))
	mux.Handle("GET /orgs/{slug}/members/{userId}/avatar", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.memberAvatar))))
	mux.Handle("POST /orgs/{slug}/members", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.invite))))
	mux.Handle("PATCH /orgs/{slug}/members/{userId}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.updateMember))))
	mux.Handle("DELETE /orgs/{slug}/members/{userId}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.offboardMember))))
	mux.Handle("GET /orgs/{slug}/members/{userId}/audit-events", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.memberAuditEvents))))
	mux.Handle("PATCH /orgs/{slug}/members/{userId}/type", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.updateMemberType))))
	mux.Handle("POST /orgs/{slug}/members/{userId}/request-identification", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.requestIdentification))))
	mux.Handle("POST /orgs/{slug}/members/request-identification", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.requestIdentificationBulk))))
	// Any member may mint their own re-identification link (the in-app banner);
	// it is scoped to the caller's own membership, so no admin gate is needed.
	mux.Handle("POST /orgs/{slug}/me/reidentify-token", orgScoped(respond.HandlerFunc(h.mintOwnReverifyToken)))

	mux.Handle("GET /orgs/{slug}/identity-settings", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.getIdentitySettings))))
	mux.Handle("PUT /orgs/{slug}/identity-settings", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.putIdentitySettings))))

	// Member screening / VOG (#242): the admin policy, upload on a member's
	// behalf, on-demand request, and history. The member's own submission is
	// the public /vog/{token} page above; any member may mint their own link.
	mux.Handle("GET /orgs/{slug}/screening-settings", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.getScreeningSettings))))
	mux.Handle("PUT /orgs/{slug}/screening-settings", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.putScreeningSettings))))
	mux.Handle("POST /orgs/{slug}/me/vog-token", orgScoped(respond.HandlerFunc(h.mintOwnVogToken)))
	mux.Handle("POST /orgs/{slug}/members/{userId}/vog", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.uploadMemberVog))))
	mux.Handle("GET /orgs/{slug}/members/{userId}/vog/history", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.screeningHistory))))
	mux.Handle("POST /orgs/{slug}/members/{userId}/request-vog", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.requestVog))))
	mux.Handle("POST /orgs/{slug}/members/request-vog", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.requestVogBulk))))

	mux.Handle("POST /orgs/{slug}/invitations/{id}/resend", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.resendInvitation))))
	mux.Handle("DELETE /orgs/{slug}/invitations/{id}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.revokeInvitation))))

	mux.Handle("GET /orgs/{slug}/departments", orgScoped(respond.HandlerFunc(h.listDepartments)))
	mux.Handle("POST /orgs/{slug}/departments", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.createDepartment))))
	mux.Handle("PATCH /orgs/{slug}/departments/{id}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.updateDepartment))))
	mux.Handle("DELETE /orgs/{slug}/departments/{id}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.deleteDepartment))))

	// The mandate register is readable by the administrative surface, but granting
	// and revoking are gated on Axis A instead of on a role — see
	// RequireMandateAuthority.
	mux.Handle("GET /orgs/{slug}/mandates", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.listMandates))))
	mux.Handle("GET /orgs/{slug}/mandates/authority", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.mandateAuthority))))
	mux.Handle("POST /orgs/{slug}/mandates", orgScoped(RequireMandateAuthority(respond.HandlerFunc(h.grantMandate))))
	mux.Handle("POST /orgs/{slug}/mandates/{id}/revoke", orgScoped(RequireMandateAuthority(respond.HandlerFunc(h.revokeMandate))))

	mux.Handle("GET /orgs/{slug}/audit-events", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.auditEvents))))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	orgs, err := h.store.List(r.Context())
	if err != nil {
		return fmt.Errorf("listing organizations: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, orgs)
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid id")
	}

	org, err := h.store.GetByID(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "org_not_found", Message: "organization not found"}
	}
	if err != nil {
		return fmt.Errorf("getting organization %s: %w", id, err)
	}

	respond.JSON(w, r, http.StatusOK, org)
	return nil
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid id")
	}

	if err := h.store.Delete(r.Context(), id); errors.Is(err, ErrNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "org_not_found", Message: "organization not found"}
	} else if err != nil {
		return fmt.Errorf("deleting organization %s: %w", id, err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) listForUser(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFromContext(r.Context())
	orgs, err := h.store.ListForUser(r.Context(), u.ID)
	if err != nil {
		return fmt.Errorf("listing organizations for user: %w", err)
	}
	respond.JSON(w, r, http.StatusOK, orgs)
	return nil
}

type orgDetailResponse struct {
	Organization
	Role string `json:"role"`
	// Identity is the caller's own re-identification state in this organisation
	// (#240), so any member — not just an admin, who alone may read the member
	// list — can be shown the banner when their identification is due, overdue
	// or has been requested. Empty for a platform admin who is not a member.
	Identity *ownIdentityState `json:"identity,omitempty"`
	// Vog is the caller's own VOG screening state (#242), the same reasoning as
	// Identity: a member who isn't an admin still needs to see their own status
	// to know a VOG is required, expiring or was requested.
	Vog *ownVogState `json:"vog,omitempty"`
}

// ownIdentityState is the caller's own identity status and deadline. It carries
// no other member's data and nothing the caller cannot already see about
// themselves.
type ownIdentityState struct {
	Status string     `json:"status"`
	DueAt  *time.Time `json:"dueAt"`
}

// ownVogState is the caller's own VOG screening status and expiry, plus
// whether the org accepts the pbdf.vog credential - a member needs that to
// know whether the wallet-disclosure option applies to them, and screening
// settings are otherwise admin-only. It carries no other member's data and
// nothing the caller cannot already see about themselves.
type ownVogState struct {
	Status           string     `json:"status"`
	ValidUntil       *time.Time `json:"validUntil"`
	AcceptCredential bool       `json:"acceptCredential"`
	// NeedsIdentity is true when the caller has no date of birth on file, so a
	// VOG cannot be matched yet: the page offers identification first (or the
	// combined identity+VOG disclosure) instead of an upload that would fail.
	NeedsIdentity bool `json:"needsIdentity"`
}

func (h *Handler) details(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	org := OrgFromContext(ctx)

	// h.defaultAddress is nil only in test setups that don't wire QERDS, mirroring
	// sendInviteEmail's h.mailer guard; a real deployment always wires it, so a
	// resolver error here is unexpected and fails the request rather than
	// falling back to the stale snapshot this replaces.
	if h.defaultAddress != nil {
		address, ok, err := h.defaultAddress.DefaultDigitalAddress(ctx, org.ID)
		if err != nil {
			return fmt.Errorf("resolving default digital address for org %s: %w", org.ID, err)
		}
		org.DigitalAddress = ""
		if ok {
			org.DigitalAddress = address
		}
	}

	respond.JSON(w, r, http.StatusOK, orgDetailResponse{
		Organization: org,
		Role:         roleFromContext(ctx),
		Identity:     h.ownIdentity(ctx, org.ID, auth.UserFromContext(ctx).ID),
		Vog:          h.ownVog(ctx, org.ID, auth.UserFromContext(ctx).ID),
	})
	return nil
}

// ownIdentity resolves the caller's own identity state, or nil when they have no
// membership (a platform admin reading someone else's org) or when it cannot be
// read — the banner is informational, so a failure here logs and disappears
// rather than failing the whole org detail the app needs to render.
func (h *Handler) ownIdentity(ctx context.Context, orgID, userID uuid.UUID) *ownIdentityState {
	member, err := h.store.GetMember(ctx, orgID, userID)
	if errors.Is(err, ErrNotMember) {
		return nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "resolving own identity state", slog.String("error", err.Error()))
		return nil
	}
	lookahead, err := h.identityLookaheadDays(ctx, orgID)
	if err != nil {
		slog.ErrorContext(ctx, "resolving identity lookahead", slog.String("error", err.Error()))
		return nil
	}
	member = member.withIdentityStatus(time.Now(), lookahead)
	return &ownIdentityState{Status: member.IdentityStatus, DueAt: member.IdentityDueAt}
}

// ownVog resolves the caller's own VOG screening state, mirroring
// ownIdentity: nil when they have no membership or it cannot be read, since
// the banner is informational.
func (h *Handler) ownVog(ctx context.Context, orgID, userID uuid.UUID) *ownVogState {
	state, err := h.vogState(ctx, orgID, userID)
	if errors.Is(err, ErrNotMember) {
		return nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "resolving own vog state", slog.String("error", err.Error()))
		return nil
	}
	return &state
}

// vogState is a member's VOG screening status and what their submission page
// needs to know, shared by the org detail's own state and the VOG link's
// preview. ErrNotMember when userID is not a member of orgID.
func (h *Handler) vogState(ctx context.Context, orgID, userID uuid.UUID) (ownVogState, error) {
	member, err := h.store.GetMember(ctx, orgID, userID)
	if err != nil {
		return ownVogState{}, err
	}
	settings, err := h.store.GetScreeningSettings(ctx, orgID)
	if err != nil {
		return ownVogState{}, fmt.Errorf("resolving screening settings: %w", err)
	}
	matchCtx, err := h.store.ScreeningMatchContext(ctx, orgID, userID)
	if err != nil {
		return ownVogState{}, fmt.Errorf("resolving screening match context: %w", err)
	}
	member = member.withVogStatus(settings, time.Now())
	return ownVogState{
		Status:           member.VogStatus,
		ValidUntil:       member.VogValidUntil,
		AcceptCredential: settings.AcceptYiviCredential,
		NeedsIdentity:    matchCtx.DateOfBirth == nil,
	}, nil
}

type updateRequest struct {
	Name string `json:"name"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return badRequest("invalid_body", "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return badRequest("invalid_input", "name is required")
	}

	org := OrgFromContext(r.Context())
	if _, err := h.store.Update(r.Context(), org.ID, req.Name); errors.Is(err, ErrNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "org_not_found", Message: "organization not found"}
	} else if err != nil {
		return fmt.Errorf("updating organization: %w", err)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

func badRequest(code, msg string) error {
	return &respond.APIError{Status: http.StatusBadRequest, Code: code, Message: msg}
}
