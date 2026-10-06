// Package proofing is the org side of identity proofing: the org's flows, its
// customers, and the requests that ask a person to prove their identity on one
// of them. The proofing engine (internal/proofingengine) runs the sessions
// in-process, with the org as its tenant: nothing is provisioned, an org can
// use proofing straight away. Any member may send a request; only an org admin
// defines flows and chooses which members may send on.
//
// An org's customers are its B2B clients, without a login: an admin assigns
// each a subset of the org's flows, and a request for a customer goes to that
// customer's subject, known by an e-mail address and an optional name.
//
// Sending a request creates the engine session (SessionTTL) and mails its Idem
// app deep link, or shows it on screen, or hands out a hosted link. The link's
// claim is single use. Nothing restarts a session: a new attempt is a new
// request. The engine notifies the wallet of every change, and the wallet then
// reads the outcome; the deadline job covers a missed notice.
//
// Data minimisation: the wallet keeps the outcome (status, assurance levels,
// error code) and, for a customer's subject, the name read off an approved
// document, sealed, for proofedNameRetention. The other document fields, the
// BSN and the images stay in the engine and are never stored, decoded or
// audited here.
package proofing

import (
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/ratelimit"
)

// hostedLinkTTL is how long a hosted request's link can be started from; the
// session it starts then runs its own SessionTTL.
const hostedLinkTTL = 72 * time.Hour

// SessionTTL is how long a mailed session runs, counted from the send. The mail
// states it in minutes, so it is whole minutes.
const SessionTTL = 10 * time.Minute

// readReconcileEvery is how often a single-request read may re-check the
// engine: a fallback for a missed notice.
const readReconcileEvery = 10 * time.Second

// maxReconcilePerRound bounds the live requests the background reconciler
// re-checks per round, across every org.
const maxReconcilePerRound = 50

// maxListedRequests caps one list read, newest first.
const maxListedRequests = 200

// proofedNameRetention is how long the name read off a customer's subject's
// document is kept before the pruner clears it: long enough for the member to
// act on the outcome, not a record of the person.
const proofedNameRetention = 30 * 24 * time.Hour

// hoursPerDay converts a customer's data retention in days to a duration.
const hoursPerDay = 24

// SessionTTLOptions are the session lifetimes an admin may pick for a
// customer; SessionTTL is the default and the longest.
var SessionTTLOptions = []time.Duration{2 * time.Minute, 5 * time.Minute, SessionTTL}

// DataRetentionDayOptions are the proofed-name retentions, in days, an admin
// may pick for a customer: up to a year; proofedNameRetention (30) is the default.
var DataRetentionDayOptions = []int{7, 30, 90, 180, 365}

// CustomerBranding is how the proofing mail to a customer's subjects looks:
// DisplayName signs it ("" is the customer's name), PrimaryColor colours it
// ("" is the org's), and SupportContact and PrivacyURL are added to it ("" leaves
// each out). HasLogo reports a stored logo, read with CustomerStore.Logo.
type CustomerBranding struct {
	DisplayName    string
	PrimaryColor   string
	SupportContact string
	PrivacyURL     string
	HasLogo        bool
	// HidePoweredBy leaves the "powered by" line off the customer's hosted pages.
	HidePoweredBy bool
}

// CustomerLogo is a customer's logo image, sniffed at upload.
type CustomerLogo struct {
	Bytes       []byte
	ContentType string
}

// LogoChange is what a branding save does to the logo: nothing, replace it
// with Logo, or (Replace with an empty Logo) remove it.
type LogoChange struct {
	Replace bool
	Logo    CustomerLogo
}

func (b CustomerBranding) auditFields() map[string]any {
	return map[string]any{
		"displayName": b.DisplayName, "primaryColor": b.PrimaryColor,
		"supportContact": b.SupportContact, "privacyUrl": b.PrivacyURL, "hasLogo": b.HasLogo,
		"hidePoweredBy": b.HidePoweredBy,
	}
}

// SignedAs is the name the customer's mail signs with.
func (c Customer) SignedAs() string {
	if c.Branding.DisplayName != "" {
		return c.Branding.DisplayName
	}
	return c.Name
}

// CustomerSettings are a customer's session settings.
type CustomerSettings struct {
	SessionTTL        time.Duration
	DataRetentionDays int
}

// DataRetention is DataRetentionDays as a duration.
func (c CustomerSettings) DataRetention() time.Duration {
	return time.Duration(c.DataRetentionDays) * hoursPerDay * time.Hour
}

func (c CustomerSettings) auditFields() map[string]any {
	return map[string]any{
		"sessionTtlSeconds": int(c.SessionTTL.Seconds()),
		"dataRetentionDays": c.DataRetentionDays,
	}
}

// hexColor is the #rrggbb form a customer's primary colour takes.
var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// statsWindow is how far back the proofing overview counts requests.
const statsWindow = 30 * 24 * time.Hour

// maxCustomerNameLength and maxSubjectNameLength bound the names a member types.
const (
	maxCustomerNameLength = 200
	maxSubjectNameLength  = 200
	maxWebhookURLLength   = 2048
	maxSupportContactLen  = 200
)

var (
	ErrNoEncryptionKey    = proofingprovider.ErrNoEncryptionKey
	ErrFlowNotFound       = errors.New("proofing: flow not found")
	ErrFlowNotCompletable = errors.New("proofing: flow needs a browser step the recipient cannot reach")
	ErrFlowNotAllowed     = errors.New("proofing: flow is not available to members")
	ErrMemberNotFound     = errors.New("proofing: not a member of this organization")
	ErrCustomerNotFound   = errors.New("proofing: customer not found")
	ErrCustomerExists     = errors.New("proofing: a customer with this name already exists")
	ErrFlowNotAssigned    = errors.New("proofing: flow is not assigned to the customer")
	ErrCustomerPaused     = errors.New("proofing: proofing is paused for the customer")
	// ErrCustomerHasOpenReviews refuses pausing a customer while a session
	// of it waits for a review: decide those first.
	ErrCustomerHasOpenReviews = errors.New("proofing: the customer has sessions waiting for review")
	ErrCustomerNoAPIKey       = errors.New("proofing: the customer has no live api key")
	// ErrCustomerSessionsLeft refuses removing a customer while a request of
	// it still holds personal data: one sent while it was being removed.
	ErrCustomerSessionsLeft = errors.New("proofing: sessions were sent for the customer while it was being removed")
	// ErrDeviceActive is a new claim link refused while the Idem app holding
	// the session is still active.
	ErrDeviceActive = errors.New("proofing: the app holding the session is still active")
	// ErrResultNotReady is a result read before the session has an outcome.
	ErrResultNotReady = errors.New("proofing: the session has no outcome yet")
	// ErrNotHosted is a headless start of a request not created hosted.
	ErrNotHosted       = errors.New("proofing: the request was not created hosted")
	ErrAPIKeyNotFound  = errors.New("proofing: api key not found")
	ErrAPIKeyInvalid   = errors.New("proofing: invalid api key")
	ErrRequestNotFound = errors.New("proofing: request not found")
	ErrWebhookNotFound = errors.New("proofing: webhook not configured")
	ErrNoCustomerLogo  = errors.New("proofing: customer has no logo")
	ErrInvalidInput    = errors.New("proofing: invalid input")
	// ErrWrongMethod is a Yivi-only call on a request that runs in the Idem app.
	ErrWrongMethod = errors.New("proofing: the request does not run in the Yivi app")
	// ErrSessionOver is a call on a request whose session can no longer run.
	ErrSessionOver = errors.New("proofing: the request's session is over")
	// ErrReferencePhotoRequired is a request on a flow that matches the face
	// against the customer's own photo (flowNeedsReferencePhoto) sent without one:
	// only the customer API can carry it.
	ErrReferencePhotoRequired = errors.New("proofing: the flow matches the face against a reference photo the request must carry")
	// ErrDisclosurePending is a Yivi request whose subject has not finished the
	// OpenID4VP disclosure in the Yivi app yet.
	ErrDisclosurePending = errors.New("proofing: the Yivi disclosure is not finished")
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
	// StatusCancelled is derived from Request.CancelledAt: the customer ended it.
	StatusCancelled Status = "cancelled"
)

// Settled reports an outcome the engine has already decided at least once. needs_review
// is settled for the recipient (nothing left for them to do) but still reconciled.
func (s Status) Settled() bool {
	return s == StatusApproved || s == StatusRejected || s == StatusNeedsReview
}

// Request is one proofing request, without its link token.
type Request struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	// Hosted is a request its subject opens from a link (ChannelHosted): its
	// engine session is created only when the subject starts.
	Hosted bool
	// FlowKind is what the session is for: an identity check, or a person
	// asking for their data (FlowDataAccess) or its erasure (FlowDataErasure),
	// which goes to review once the person is proven (see data_request.go).
	FlowKind FlowKind
	// DataExportUntil is until when an approved FlowDataAccess request's data
	// can be downloaded; nil otherwise.
	DataExportUntil *time.Time
	// CancelledAt is when the customer cancelled it; PurgedAt when its data was
	// erased in the engine and here (the row stays for the audit trail).
	CancelledAt     *time.Time
	PurgedAt        *time.Time
	RequestedBy     *uuid.UUID
	RequestedByName string
	// APIKeyID is the customer key that created the request through the API,
	// instead of a member; APIKeyName its label, empty once the key is gone.
	APIKeyID   *uuid.UUID
	APIKeyName string
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
	// until proofedNameRetention clears it; empty otherwise.
	ProofedName string
	// PurgeAt is when the pruner purges the request (Purge): its customer's
	// retention after it settled; nil while it runs, and for a member's.
	PurgeAt  *time.Time
	FlowID   string
	FlowName string
	// FlowVersion is the engine flow version the session pinned; 0 when unknown.
	FlowVersion int
	// Method is how the subject took part; "" while no device claimed it.
	Method proofingprovider.Method
	// NameRetention is how long a proofed name is kept: the customer's data
	// retention, or proofedNameRetention for a member's request.
	NameRetention time.Duration
	// RetentionOverride is the flow's own retention at send, when it set one:
	// it applies in place of the customer's.
	RetentionOverride time.Duration
	Status            Status
	// LinkExpiresAt is when the mailed session ends: its engine expiry at send.
	LinkExpiresAt  time.Time
	AssuranceLevel string
	EIDASLevel     string
	// RequiredAssuranceLevel is the eIDAS level the flow demanded at send; ""
	// for none. An approval below it is recorded as a rejection.
	RequiredAssuranceLevel string
	ErrorCode              string
	CreatedAt              time.Time
	UpdatedAt              time.Time
	CompletedAt            *time.Time
	// RedirectURL is where a hosted request's page sends its subject once it
	// settles; empty for none. Language is the page's language; empty leaves
	// the subject's browser to decide.
	RedirectURL string
	Language    email.Locale
	// Diplomas is whether the request asks for DUO diploma extracts once the
	// identity is approved: its flow's DiplomaMode when it was sent.
	Diplomas DiplomaMode
	// ExpectsSubject is a request for one known person: only an identity that
	// is SubjectName, born on expectedBirthDate, is approved (matchSubject).
	ExpectsSubject bool

	// expectedBirthDate is the birth date an ExpectsSubject request was sent
	// with, as YYYY-MM-DD; empty once the request is decided or purged.
	expectedBirthDate string
	// session is the live the engine session, decrypted; nil when none is attached.
	session *ipsSession
	// yiviTransactionID is the verifier's transaction of a Yivi request's
	// latest OpenID4VP disclosure; empty before one is started. Server-side
	// only: it reads the disclosed claims.
	yiviTransactionID string
}

type ipsSession struct {
	ID    string
	Token string
	// ExpiresAt is the engine's hard cap for the session: past it, it is certainly over.
	ExpiresAt time.Time
	// EndedAt is when the wallet saw the session end without an outcome (expired
	// or cancelled in the engine); nil while it may still run or decide.
	EndedAt *time.Time
}

// liveSession returns the request's engine session while it can still run, else nil.
func (r Request) liveSession(now time.Time) *ipsSession {
	if r.session == nil || r.session.EndedAt != nil || !now.Before(r.session.ExpiresAt) {
		return nil
	}
	return r.session
}

// EffectiveStatus is Status with an unfinished request whose session is over
// reading as expired: the mail was the session, so nothing can start again.
func (r Request) EffectiveStatus(now time.Time) Status {
	if r.CancelledAt != nil {
		return StatusCancelled
	}
	if r.awaitingStart(now) {
		return r.Status
	}
	if !r.Status.Settled() && r.liveSession(now) == nil {
		return StatusExpired
	}
	// A review the engine gave up on (it expired or was cancelled there) is over too.
	if r.Status == StatusNeedsReview && r.session != nil && r.session.EndedAt != nil {
		return StatusExpired
	}
	return r.Status
}

// SessionExpiresAt is when the request's running the engine session ends, or nil when
// it is over.
func (r Request) SessionExpiresAt(now time.Time) *time.Time {
	sess := r.liveSession(now)
	if sess == nil {
		return nil
	}
	at := sess.ExpiresAt
	return &at
}

// awaitingStart reports a hosted request its subject has not started yet,
// whose link still can be.
func (r Request) awaitingStart(now time.Time) bool {
	return r.Hosted && r.session == nil && r.PurgedAt == nil && r.Status == StatusPending && now.Before(r.LinkExpiresAt)
}

// needsReconcile reports whether the engine may hold a newer state than the row: an
// attached session not yet seen to end (running, or past its cap with its end or
// a last-moment outcome still unread), or an outcome still under review.
func (r Request) needsReconcile() bool {
	switch {
	case r.session == nil:
		return false
	case r.Status == StatusNeedsReview:
		return r.session.EndedAt == nil
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

// Org is the organization a proofing call acts for.
type Org struct {
	ID   uuid.UUID
	Name string
}

// FlowSelection is the org admin's allow-list over the org's flows: the
// flows members may send a request on, and the one the request form preselects.
// DefaultFlowID is one of FlowIDs, or empty when FlowIDs is.
type FlowSelection struct {
	FlowIDs       []string
	DefaultFlowID string
}

// OrgFlow is one of the org's flows with the admin's selection applied.
type OrgFlow struct {
	proofingprovider.Flow
	Allowed bool
	Default bool
	// Diplomas is whether the flow asks for DUO diploma extracts.
	Diplomas DiplomaMode
	// Kind is what the flow's sessions are for.
	Kind FlowKind
}

// RequestFilter narrows a request list: to the requests one member sent, and/or
// to one customer's. A nil field does not narrow.
type RequestFilter struct {
	RequestedBy *uuid.UUID
	CustomerID  *uuid.UUID
	// SubjectUserID narrows to the requests proofing one member.
	SubjectUserID *uuid.UUID
}

// NewRequest is a member's ask to proof someone on a flow: another member
// (SubjectUserID), or, with a CustomerID, that customer's subject by e-mail
// address and an optional name.
type NewRequest struct {
	SubjectUserID uuid.UUID
	CustomerID    *uuid.UUID
	SubjectEmail  string
	SubjectName   string
	// ReferencePhoto is the customer's own photo of the subject's face,
	// standard base64: the live face is matched against it, for a flow that
	// flowNeedsReferencePhoto (required there, refused anywhere else).
	ReferencePhoto string
	// SubjectBirthDate (YYYY-MM-DD) makes the request one for exactly the
	// person SubjectName names, born then: any other identity is rejected
	// with errorIdentityMismatch. Empty proofs whoever takes part.
	SubjectBirthDate string
	FlowID           string
	// SkipMail leaves the mail out: an API caller that shows the deep link in
	// its own interface.
	SkipMail bool
	// Method is the app the subject proofs with: the Idem app (vcmrtd, the
	// default when empty) or the Yivi app.
	Method proofingprovider.Method
	// Channel is how the subject gets the session: mailed (the default when
	// empty), or shown on the sender's screen, where the subject scans it.
	Channel Channel
	// Language is the sender's wallet language: the mail's, and the one the engine
	// hands the Idem app. Empty leaves the deployment default and the phone's.
	Language email.Locale
	// RedirectURL is where a hosted page sends its subject once the session
	// settles; its origin must be one of the customer's RedirectOrigins. Empty
	// shows the page's own done screen.
	RedirectURL string
}

// Channel is how a request reaches its subject.
type Channel string

const (
	// ChannelEmail mails the session's QR code and link: the mail is the session.
	ChannelEmail Channel = "email"
	// ChannelHosted hands the caller a link to the public page, where the
	// subject, on their own device, reads what is collected, picks the app and
	// starts: the engine session is created then. Nothing is mailed.
	ChannelHosted Channel = "hosted"
	// ChannelOnScreen shows the session on the sender's screen once the subject
	// accepted what will be collected: nothing is mailed, and a customer's
	// subject needs no address. The Yivi app's face check runs on that screen,
	// so a Yivi session is only ever delivered this way.
	ChannelOnScreen Channel = "on_screen"
)

// Subject is who a stored request went to: a member (UserID) or a customer's
// subject (CustomerID), never both.
type Subject struct {
	UserID     *uuid.UUID
	CustomerID *uuid.UUID
	Name       string
	Email      string
	// BirthDate (YYYY-MM-DD) is set for a request for one known person: Name,
	// born then. Empty for anyone.
	BirthDate string
}

// Customer is one of the org's B2B customers, with the org flows assigned to it.
type Customer struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Name           string
	Flows          FlowSelection
	// PausedAt is when an admin paused proofing for the customer; nil while it
	// is active.
	PausedAt *time.Time
	Settings CustomerSettings
	Branding CustomerBranding
	// RedirectOrigins are the origins (scheme://host[:port]) a hosted page of
	// the customer may send its subject back to and be embedded on.
	RedirectOrigins []string
	// HasAPIKey is whether the customer holds an unrevoked API key: requests
	// for it need one.
	HasAPIKey bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CustomerStatus is whether new requests can be sent for a customer.
type CustomerStatus string

// FlowView is which of the org's flows a read lists.
type FlowView int

const (
	// FlowsAllowed is the flows a member may send on.
	FlowsAllowed FlowView = iota
	// FlowsAll is every flow: an admin's view, to configure and assign from.
	FlowsAll
)

// PauseState is what SetProofingPaused sets a pause level to.
type PauseState string

const (
	PauseOn  PauseState = "paused"
	PauseOff PauseState = "resumed"
)

const (
	CustomerActive CustomerStatus = "active"
	CustomerPaused CustomerStatus = "paused"
)

// Paused reports whether proofing is paused for the customer.
func (c Customer) Paused() bool { return c.PausedAt != nil }

// Status is the customer's CustomerStatus.
func (c Customer) Status() CustomerStatus {
	if c.Paused() {
		return CustomerPaused
	}
	return CustomerActive
}

// StatsRow counts one customer's requests on one flow within statsWindow, by
// outcome. Sessions is every request sent; what the outcome counts leave is
// still pending or in progress.
type StatsRow struct {
	CustomerID  uuid.UUID
	FlowID      string
	Sessions    int
	Approved    int
	Rejected    int
	NeedsReview int
	Expired     int
	// Cancelled are the requests cancelled before an outcome, apart from
	// Expired.
	Cancelled int
}

// CustomerFlow is one of the org's flows with a customer's assignment applied.
type CustomerFlow struct {
	proofingprovider.Flow
	Assigned bool
	Default  bool
	Diplomas DiplomaMode
	Kind     FlowKind
	// RetentionDays is what a subject on this flow for the customer is told
	// about keeping their data: subjectRetentionDays.
	RetentionDays int
}

// faceSteps are the flow steps that capture the face. A wallet flow runs them
// in the Idem app (selfieLocationNative); a face step without the chip read
// needs a reference photo (flowCompletable).
var faceSteps = []string{"face_verification", "selfie", "liveness", "face_match"}

const (
	selfieLocationBrowser = "browser"
	selfieLocationNative  = "native"
)

const (
	faceProviderRegula = "regula"
	// faceProviderEngine names a built-in face engine, which this engine does not
	// have: a flow cannot pick it.
	faceProviderEngine = "engine"
)

// faceProviders are the values the wallet sets on a flow's face provider. It
// always names one, so a flow never depends on the engine's default; the
// engine verifies faces with Regula only.
var faceProviders = []string{faceProviderRegula}

// yiviAppAvailable reports whether flow f can run in the Yivi app: unless it
// photographs the document, which only the Idem app does, or matches the face
// against a customer's reference photo (the Yivi app matches against its
// credential's photo).
func yiviAppAvailable(f proofingprovider.Flow) bool {
	return !slices.Contains(f.Steps, stepDocumentPhoto) && !flowNeedsReferencePhoto(f)
}

// Public API rate limits, per customer and per API replica: apiCallLimit for
// every call, APISessionLimit also for creating a session, so one customer
// cannot use up the deployment's capacity.
var (
	apiCallLimit    = ratelimit.Limit{Burst: 120, Per: time.Minute}
	APISessionLimit = ratelimit.Limit{Burst: 10, Per: time.Minute}
)

// eIDAS levels of assurance, lowest first: a flow's required level and the
// level the engine reports a session achieved.
const (
	eidasLow         = "low"
	eidasSubstantial = "substantial"
	eidasHigh        = "high"
)

var eidasLevels = []string{eidasLow, eidasSubstantial, eidasHigh}

// yiviEIDASLevel is an approved Yivi session's level when the engine reports
// none: the face matched the credential's photo without liveness, which is
// low.
const yiviEIDASLevel = eidasLow

// errorAssuranceNotMet: the engine approved below the flow's required level,
// so the request is rejected, never recorded as a pass at the lower level.
const errorAssuranceNotMet = "ASSURANCE_NOT_MET"

// ErrorOrgPaused is the rejection every open review gets when its org pauses
// identity proofing: nothing can be decided while it is paused.
const ErrorOrgPaused = "ORG_PAUSED"

// orgPausedReason is the review decision's reason for ErrorOrgPaused.
const orgPausedReason = "The organisation paused identity proofing; try again later."

// orgPausedReviewer is who the automatic ErrorOrgPaused decisions name.
const orgPausedReviewer = "system: organisation paused"

// errorIdentityMismatch is the error code the wallet records when a request
// for one known person (ExpectsSubject) is approved for someone else: the
// identity read off the document is not that name and birth date.
const errorIdentityMismatch = "IDENTITY_MISMATCH"

// birthDateLayout is how a request's expected birth date is written.
const birthDateLayout = time.DateOnly

// meetsAssurance reports whether an achieved eIDAS level satisfies a required
// one. No requirement is always met; an unknown or absent achieved level
// meets none, and an unknown requirement is never met (fail closed).
func meetsAssurance(achieved, required string) bool {
	if required == "" {
		return true
	}
	want := slices.Index(eidasLevels, required)
	got := slices.Index(eidasLevels, achieved)
	return want >= 0 && got >= want
}

// stepNFCRead is the chip read. A face step compares the selfie against the
// chip's photo; without the chip, the engine needs a reference photo supplied per
// session, which the wallet does not have.
const stepNFCRead = "nfc_read"

// attributeDocument is the document data (dg1): the holder's name and date of
// birth, read by the document scan.
const attributeDocument = "dg1"

// stepDocumentPhoto is the photos of the document's front and back, taken in
// the Idem app.
const stepDocumentPhoto = "document_photo"

// flowCompletable reports whether a recipient can finish flow f with only the
// vcmrtd app and the wallet's session: a face step must run in the app and
// have the chip photo to compare against.
func flowCompletable(f proofingprovider.Flow) bool {
	if !hasFaceStep(f.Steps) {
		return true
	}
	return f.SelfieLocation != selfieLocationBrowser && slices.Contains(f.Steps, stepNFCRead)
}

// flowNeedsReferencePhoto reports whether flow f matches the face without
// reading the chip: there is no chip photo, so each session carries the
// customer's own photo of the person (NewRequest.ReferencePhoto). Only a
// customer can send one, through its API; a member cannot be sent such a flow.
func flowNeedsReferencePhoto(f proofingprovider.Flow) bool {
	return hasFaceStep(f.Steps) && !slices.Contains(f.Steps, stepNFCRead) && f.SelfieLocation != selfieLocationBrowser
}

// customerCompletable is flowCompletable for a customer's subject, whose session
// may carry a reference photo: a flow that needs one can be assigned and sent.
func customerCompletable(f proofingprovider.Flow) bool {
	return flowCompletable(f) || flowNeedsReferencePhoto(f)
}

// maxReferencePhotoBytes bounds a reference photo: a face to match, not a
// document scan.
const maxReferencePhotoBytes = 512 << 10

// referencePhotoTypes are the image types a reference photo may be, sniffed
// from its bytes: the ones Regula and a browser both read.
var referencePhotoTypes = []string{"image/jpeg", "image/png", "image/webp"}

func hasFaceStep(steps []string) bool {
	return slices.ContainsFunc(steps, func(step string) bool { return slices.Contains(faceSteps, step) })
}
