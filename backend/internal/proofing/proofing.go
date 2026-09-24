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
// A request goes to a member of the org. Sending it creates the IPS session, and
// the e-mail carries a QR code and a button for one wallet link that lives
// exactly as long as that session (SessionTTL, IPS's own cap). The link's page
// shows IPS's vcmrtd/idem QR code and deep link, one payload in two forms, and
// re-mints it while the session lives: IPS's claim inside it is single-use and
// shorter-lived than the session. Outcomes are reconciled on read (the
// recipient's page polls, the request list re-checks live requests). The IPS webhook is not used: it goes to a
// per-session URL and carries the full personal-data result.
//
// Data minimisation: only the outcome is kept (status, achieved assurance levels,
// IPS error code). The document fields, BSN and images IPS returns are never
// decoded, stored or audited.
package proofing

import (
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// SessionTTL is how long a sent request can be completed: its IPS session's
// lifetime and so its mailed link's. It is IPS's own maximum session lifetime;
// IPS clamps anything longer to it.
const SessionTTL = 15 * time.Minute

// maxReconcilePerList bounds how many live requests one list read re-checks at
// IPS, so a list stays one bounded round of calls however many are open.
const maxReconcilePerList = 10

// maxListedRequests caps one list read, newest first.
const maxListedRequests = 200

var (
	ErrNotProvisioned     = errors.New("proofing: organization has no identity proofing tenant")
	ErrNoEncryptionKey    = errors.New("proofing: no identity proofing encryption key configured")
	ErrLinkNotFound       = errors.New("proofing: proofing link not found or expired")
	ErrFlowNotFound       = errors.New("proofing: flow not found")
	ErrFlowNotCompletable = errors.New("proofing: flow needs a browser step the recipient cannot reach")
	ErrFlowNotAllowed     = errors.New("proofing: flow is not available to members")
	ErrMemberNotFound     = errors.New("proofing: not a member of this organization")
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
	// SubjectUserID is the member the request was sent to; nil for a request
	// from before requests went to members, or once the member's user is gone.
	SubjectUserID *uuid.UUID
	SubjectName   string
	SubjectEmail  string
	FlowID        string
	FlowName      string
	// FlowVersion is the IPS flow version the session pinned; 0 when unknown.
	FlowVersion    int
	Status         Status
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
	ID        string
	Token     string
	ExpiresAt time.Time
}

// EffectiveStatus is Status with an unfinished request past its link reading as
// expired: nobody can start or finish it any more.
func (r Request) EffectiveStatus(now time.Time) Status {
	if !r.Status.Settled() && now.After(r.LinkExpiresAt) {
		return StatusExpired
	}
	return r.Status
}

// needsReconcile reports whether IPS may hold a newer state than the row.
func (r Request) needsReconcile() bool {
	return r.session != nil && (r.Status == StatusPending || r.Status == StatusInProgress || r.Status == StatusNeedsReview)
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

// Link is what a proofing link resolves to on the public page.
type Link struct {
	Request          Request
	OrganizationName string
}

// NewRequest is a member's ask to proof another member on a flow.
type NewRequest struct {
	SubjectUserID uuid.UUID
	FlowID        string
}

// StartResult is the recipient's next step: a vcmrtd link to show as a QR, or
// none when the phone is already in the flow or the request is settled.
type StartResult struct {
	Status         Status
	DeepLink       string
	ClaimExpiresAt *time.Time
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
