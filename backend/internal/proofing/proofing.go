// Package proofing is the org side of identity proofing: an org's link to its
// identity-proofing-service (IPS) tenant, the org's proofing flows, and the
// e-mailed requests that ask a person to prove their identity on one of them.
//
// One org is one IPS tenant, provisioned by the wallet through the IPS admin API
// the first time the org uses proofing: there is no enable step, every org gets
// its own tenant and API key on first use. Any member may send a request; only
// an org admin may define flows (the checks a request runs) and choose which of
// them members may send on.
//
// An org also has customers (its B2B clients, with no login of their own): an
// admin assigns each a subset of the org's flows, and any member can send a
// request for a customer to that customer's subject, an external person known
// only by an e-mail address and an optional name.
//
// A request goes to a member of the org or to a customer's subject. Sending it
// creates the IPS session (SessionTTL, IPS's own default lifetime) and mails its
// vcmrtd deep link, as a QR code and a button: there is no page in between, so
// the mail is the session. The link's claim is single use, and IPS keeps it
// claimable as long as the session (ClaimTokenTTL). Nothing restarts a session:
// once it ends, a new request means a new mail. Outcomes are reconciled on read
// (the request list re-checks live requests). The IPS webhook is not used:
// it goes to a per-session URL and carries the full personal-data result.
//
// Data minimisation: only the outcome is kept (status, achieved assurance levels,
// IPS error code). For a customer's subject, the name read off an approved
// document is kept too, sealed, for ProofedNameRetention: the sender may have
// known only an e-mail address. The other document fields, the BSN and images
// IPS returns are never decoded, stored or audited, and neither is the name.
package proofing

import (
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// SessionTTL is how long a mailed session runs, counted from the send: IPS's own
// default session lifetime (SessionCreateTTL), within its hard cap. The mail
// states it in minutes, so it is whole minutes.
const SessionTTL = 10 * time.Minute

// maxReconcilePerList bounds how many live requests one list read re-checks at
// IPS, so a list stays one bounded round of calls however many are open.
const maxReconcilePerList = 10

// maxListedRequests caps one list read, newest first.
const maxListedRequests = 200

// ProofedNameRetention is how long the name read off a customer's subject's
// document is kept before the pruner clears it: long enough for the member to
// act on the outcome, not a record of the person.
const ProofedNameRetention = 30 * 24 * time.Hour

// maxCustomerNameLength and maxSubjectNameLength bound the names a member types.
const (
	maxCustomerNameLength = 200
	maxSubjectNameLength  = 200
)

var (
	ErrNotProvisioned     = errors.New("proofing: organization has no identity proofing tenant")
	ErrNoEncryptionKey    = errors.New("proofing: no identity proofing encryption key configured")
	ErrFlowNotFound       = errors.New("proofing: flow not found")
	ErrFlowNotCompletable = errors.New("proofing: flow needs a browser step the recipient cannot reach")
	ErrFlowNotAllowed     = errors.New("proofing: flow is not available to members")
	ErrMemberNotFound     = errors.New("proofing: not a member of this organization")
	ErrCustomerNotFound   = errors.New("proofing: customer not found")
	ErrCustomerExists     = errors.New("proofing: a customer with this name already exists")
	ErrFlowNotAssigned    = errors.New("proofing: flow is not assigned to the customer")
	ErrInvalidInput       = errors.New("proofing: invalid input")
)

// Status is a request's lifecycle state. Expired is derived (see Request.EffectiveStatus).
type Status string

const (
	StatusPending     Status = "pending"
	StatusInProgress  Status = "in_progress"
	StatusApproved    Status = "approved"
	StatusRejected    Status = "rejected"
	StatusNeedsReview Status = "needs_review"
	StatusExpired     Status = "expired"
)

// Settled reports an outcome IPS has already decided at least once. needs_review
// is settled for the recipient (nothing left for them to do) but still reconciled.
func (s Status) Settled() bool {
	return s == StatusApproved || s == StatusRejected || s == StatusNeedsReview
}

// Request is one proofing request, without its link token.
type Request struct {
	ID              uuid.UUID
	OrganizationID  uuid.UUID
	RequestedBy     *uuid.UUID
	RequestedByName string
	// SubjectUserID is the member the request was sent to; nil for a customer's
	// subject, a request from before requests went to members, or once the
	// member's user is gone.
	SubjectUserID *uuid.UUID
	// CustomerID is the customer a request was sent for; nil for a member.
	CustomerID   *uuid.UUID
	CustomerName string
	// SubjectName is empty for a customer's subject sent without a name.
	SubjectName  string
	SubjectEmail string
	// ProofedName is the name read off a customer's subject's approved document,
	// until ProofedNameRetention clears it; empty otherwise.
	ProofedName string
	FlowID      string
	FlowName    string
	// FlowVersion is the IPS flow version the session pinned; 0 when unknown.
	FlowVersion int
	Status      Status
	// LinkExpiresAt is when the mailed session ends: its IPS expiry at send.
	LinkExpiresAt  time.Time
	AssuranceLevel string
	EIDASLevel     string
	ErrorCode      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time

	// session is the live IPS session, decrypted; nil when none is attached.
	session *ipsSession
}

type ipsSession struct {
	ID    string
	Token string
	// ExpiresAt is IPS's hard cap for the session: past it, it is certainly over.
	ExpiresAt time.Time
	// EndedAt is when the wallet saw the session end without an outcome (expired
	// or cancelled at IPS); nil while it may still run or decide.
	EndedAt *time.Time
}

// liveSession returns the request's IPS session while it can still run, else nil.
func (r Request) liveSession(now time.Time) *ipsSession {
	if r.session == nil || r.session.EndedAt != nil || !now.Before(r.session.ExpiresAt) {
		return nil
	}
	return r.session
}

// EffectiveStatus is Status with an unfinished request whose session is over
// reading as expired: the mail was the session, so nothing can start again.
func (r Request) EffectiveStatus(now time.Time) Status {
	if !r.Status.Settled() && r.liveSession(now) == nil {
		return StatusExpired
	}
	return r.Status
}

// SessionExpiresAt is when the request's running IPS session ends, or nil when
// it is over.
func (r Request) SessionExpiresAt(now time.Time) *time.Time {
	sess := r.liveSession(now)
	if sess == nil {
		return nil
	}
	at := sess.ExpiresAt
	return &at
}

// needsReconcile reports whether IPS may hold a newer state than the row: an
// attached session not yet seen to end (running, or past its cap with its end or
// a last-moment outcome still unread), or an outcome still under review.
func (r Request) needsReconcile() bool {
	switch {
	case r.session == nil:
		return false
	case r.Status == StatusNeedsReview:
		return true
	default:
		return (r.Status == StatusPending || r.Status == StatusInProgress) && r.session.EndedAt == nil
	}
}

// Member is a person a request can be sent to: any member of the org, admin or
// not, employee or external.
type Member struct {
	UserID               uuid.UUID
	Name                 string
	Email                string
	Role                 string
	MemberType           string
	ExternalOrganisation string
}

// Org is the organization a proofing call acts for. Its name names the org's
// IPS tenant when the call is the org's first use of proofing.
type Org struct {
	ID   uuid.UUID
	Name string
}

// FlowSelection is the org admin's allow-list over the org's IPS flows: the
// flows members may send a request on, and the one the request form preselects.
// DefaultFlowID is one of FlowIDs, or empty when FlowIDs is.
type FlowSelection struct {
	FlowIDs       []string
	DefaultFlowID string
}

// OrgFlow is one of the org's IPS flows with the admin's selection applied.
type OrgFlow struct {
	proofingprovider.Flow
	Allowed bool
	Default bool
}

// RequestFilter narrows a request list: to the requests one member sent, and/or
// to one customer's. A nil field does not narrow.
type RequestFilter struct {
	RequestedBy *uuid.UUID
	CustomerID  *uuid.UUID
}

// NewRequest is a member's ask to proof someone on a flow: another member
// (SubjectUserID), or, with a CustomerID, that customer's subject by e-mail
// address and an optional name.
type NewRequest struct {
	SubjectUserID uuid.UUID
	CustomerID    *uuid.UUID
	SubjectEmail  string
	SubjectName   string
	FlowID        string
}

// Subject is who a stored request went to: a member (UserID) or a customer's
// subject (CustomerID), never both.
type Subject struct {
	UserID     *uuid.UUID
	CustomerID *uuid.UUID
	Name       string
	Email      string
}

// Customer is one of the org's B2B customers, with the org flows assigned to it.
type Customer struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Name           string
	Flows          FlowSelection
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CustomerFlow is one of the org's IPS flows with a customer's assignment applied.
type CustomerFlow struct {
	proofingprovider.Flow
	Assigned bool
	Default  bool
}

// faceSteps are the IPS flow steps that capture the subject's face. With the
// "browser" selfie location they need the IPS web page, which has no end-user
// version yet, so a flow using them that way cannot be finished by a recipient.
// A face step without the chip read cannot either (see Completable).
var faceSteps = []string{"face_verification", "selfie", "liveness", "face_match"}

const (
	selfieLocationBrowser = "browser"
	selfieLocationNative  = "native"
)

// stepNFCRead is the chip read. A face step compares the selfie against the
// chip's photo; without the chip, IPS needs a reference photo supplied per
// session, which the wallet does not have.
const stepNFCRead = "nfc_read"

// Completable reports whether a recipient can finish flow f with only the
// vcmrtd app and the wallet's session: a face step must run in the app and
// have the chip photo to compare against.
func Completable(f proofingprovider.Flow) bool {
	if !hasFaceStep(f.Steps) {
		return true
	}
	return f.SelfieLocation != selfieLocationBrowser && slices.Contains(f.Steps, stepNFCRead)
}

func hasFaceStep(steps []string) bool {
	return slices.ContainsFunc(steps, func(step string) bool { return slices.Contains(faceSteps, step) })
}
