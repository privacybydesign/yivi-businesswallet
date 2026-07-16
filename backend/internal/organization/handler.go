package organization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

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
	ListForUser(ctx context.Context, userID uuid.UUID) ([]Organization, error)
	GetMembership(ctx context.Context, userID, orgID uuid.UUID) (Membership, error)
	GetMember(ctx context.Context, orgID, userID uuid.UUID) (Member, error)
	ListMemberEntries(ctx context.Context, orgID uuid.UUID, p MemberListParams) ([]MemberEntry, int, error)
	RevokeInvitation(ctx context.Context, orgID, invitationID uuid.UUID) error
	// ResendInvitation rotates the invite token and extends the expiry, returning
	// the refreshed invitation (with its new raw Token) so a fresh link can be
	// e-mailed.
	ResendInvitation(ctx context.Context, orgID, invitationID uuid.UUID) (Invitation, error)
	UpdateMembership(ctx context.Context, orgID, userID uuid.UUID, role *string, jobTitle *string, departmentID *uuid.UUID) (Member, error)
	ListDepartments(ctx context.Context, orgID uuid.UUID) ([]Department, error)
	CreateDepartment(ctx context.Context, orgID uuid.UUID, name string) (Department, error)
	UpdateDepartment(ctx context.Context, orgID, deptID uuid.UUID, name string) (Department, error)
	DeleteDepartment(ctx context.Context, orgID, deptID uuid.UUID) error
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
}

type Handler struct {
	store       repository
	service     inviter
	reader      auditReader
	issuer      sessionIssuer
	mailer      inviteMailer
	appBaseURL  string
	requireUser func(http.Handler) http.Handler
	admins      auth.PlatformAdmins
}

func NewHandler(store repository, service inviter, reader auditReader, issuer sessionIssuer, mailer inviteMailer, appBaseURL string, requireUser func(http.Handler) http.Handler, admins auth.PlatformAdmins) *Handler {
	return &Handler{store: store, service: service, reader: reader, issuer: issuer, mailer: mailer, appBaseURL: strings.TrimRight(appBaseURL, "/"), requireUser: requireUser, admins: admins}
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

	mux.Handle("GET /orgs/{slug}", orgScoped(respond.HandlerFunc(h.details)))
	mux.Handle("PATCH /orgs/{slug}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.update))))
	mux.Handle("GET /orgs/{slug}/members", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.members))))
	mux.Handle("GET /orgs/{slug}/members/{userId}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.member))))
	mux.Handle("POST /orgs/{slug}/members", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.invite))))
	mux.Handle("PATCH /orgs/{slug}/members/{userId}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.updateMember))))
	mux.Handle("GET /orgs/{slug}/members/{userId}/audit-events", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.memberAuditEvents))))

	mux.Handle("POST /orgs/{slug}/invitations/{id}/resend", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.resendInvitation))))
	mux.Handle("DELETE /orgs/{slug}/invitations/{id}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.revokeInvitation))))

	mux.Handle("GET /orgs/{slug}/departments", orgScoped(respond.HandlerFunc(h.listDepartments)))
	mux.Handle("POST /orgs/{slug}/departments", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.createDepartment))))
	mux.Handle("PATCH /orgs/{slug}/departments/{id}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.updateDepartment))))
	mux.Handle("DELETE /orgs/{slug}/departments/{id}", orgScoped(RequireOrgAdmin(respond.HandlerFunc(h.deleteDepartment))))

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
}

func (h *Handler) details(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	respond.JSON(w, r, http.StatusOK, orgDetailResponse{
		Organization: OrgFromContext(ctx),
		Role:         roleFromContext(ctx),
	})
	return nil
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
