// Package proofing is the org side of identity proofing: the org's flows, and
// the requests that ask a person to prove their identity on one of them. The
// proofing engine (internal/proofingengine) runs the sessions in-process, with
// the org as its tenant: nothing is provisioned, an org can use proofing
// straight away. Any member may send a request; only an org admin defines flows
// and chooses which members may send on.
//
// A request goes to a member of the org, or to an external person without a
// login, known by an e-mail address and an optional name. The org's API keys
// and its one webhook endpoint let its own software do the same.
//
// Sending a request creates the engine session (SessionTTL) and mails its Idem
// app deep link, or shows it on screen, or hands out a hosted link. The link's
// claim is single use. Nothing restarts a session: a new attempt is a new
// request. The engine notifies the wallet of every change, and the wallet then
// reads the outcome; the deadline job covers a missed notice.
//
// Data minimisation: the wallet keeps the outcome (status, assurance levels,
// error code) and, for an external person, the name read off an approved
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
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/ratelimit"
)

// hostedLinkTTL is how long a hosted request's link can be started from; the
// session it starts then runs its own SessionTTL.
const hostedLinkTTL = 72 * time.Hour

// SessionTTL is how long a mailed session runs, counted from the send, on a
// flow that sets no lifetime of its own: the org's, a fixed default
// (checkSettingsChange). The mail states it in minutes, so it is whole minutes.
const SessionTTL = 10 * time.Minute

// readReconcileEvery is how often a single-request read may re-check the
// engine: a fallback for a missed notice.
const readReconcileEvery = 10 * time.Second

// maxReconcilePerRound bounds the live requests the background reconciler
// re-checks per round, across every org.
const maxReconcilePerRound = 50

// maxListedRequests caps one list read, newest first.
const maxListedRequests = 200

// proofedNameRetention is how long the name read off an external person's
// document is kept before the pruner clears it: long enough for the member to
// act on the outcome, not a record of the person. A member's request is
// purged after it too, unless its flow sets a retention.
const proofedNameRetention = 30 * 24 * time.Hour

// hoursPerDay converts the org's data retention in days to a duration.
const hoursPerDay = 24

// defaultDataRetentionDays is how long an external person's session data is
// kept after it ends on a flow that sets no retention of its own: the org's,
// a fixed default (checkSettingsChange), a week, as Settings › Data and privacy
// says. Migration 20261021114000 gave each flow the org's own value, where it
// had another, when it became fixed. The column's default
// (identity_proofing_settings.data_retention_days) is the same.
const defaultDataRetentionDays = 7

// SessionTTLOptions are the session lifetimes a flow may set; SessionTTL is
// the default and the longest.
var SessionTTLOptions = []time.Duration{2 * time.Minute, 5 * time.Minute, SessionTTL}

// Branding is how the proofing mail to external people and the hosted pages
// look: DisplayName signs the mail ("" is the org's name), PrimaryColor colours
// it ("" is the org's), and SupportContact and PrivacyURL are added to it ("" leaves
// each out). HasLogo reports a stored logo, read with OrgSettingsStore.Logo.
// SecondaryColor (the Idem app's second colour), the other colours and
// FontFamily are the rest of its white-label, the same as the org theme's: any
// one set is a look of its own, and "" in it keeps the design's, not the org's
// (see AppBrandingResolver); TopbarColor is the app's header.
type Branding struct {
	DisplayName    string
	PrimaryColor   string
	SecondaryColor string
	TextColor      string
	SurfaceColor   string
	BorderColor    string
	LinkColor      string
	SuccessColor   string
	WarningColor   string
	ErrorColor     string
	TopbarColor    string
	FontFamily     string
	SupportContact string
	PrivacyURL     string
	HasLogo        bool
	// HidePoweredBy leaves the "powered by" line off the hosted pages.
	HidePoweredBy bool
}

// Logo is the org's logo image for its proofing mail and hosted pages, sniffed
// at upload.
type Logo struct {
	Bytes       []byte
	ContentType string
}

// LogoChange is what a branding save does to the logo: nothing, replace it
// with Logo, or (Replace with an empty Logo) remove it.
type LogoChange struct {
	Replace bool
	Logo    Logo
}

func (b Branding) auditFields() map[string]any {
	return map[string]any{
		"displayName": b.DisplayName, "primaryColor": b.PrimaryColor, "secondaryColor": b.SecondaryColor,
		"textColor": b.TextColor, "surfaceColor": b.SurfaceColor, "borderColor": b.BorderColor,
		"linkColor": b.LinkColor, "successColor": b.SuccessColor, "warningColor": b.WarningColor,
		"errorColor": b.ErrorColor, "topbarColor": b.TopbarColor, "fontFamily": b.FontFamily,
		"supportContact": b.SupportContact, "privacyUrl": b.PrivacyURL, "hasLogo": b.HasLogo,
		"hidePoweredBy": b.HidePoweredBy,
	}
}

// SessionSettings are how long a session runs and how long its data is kept.
type SessionSettings struct {
	SessionTTL        time.Duration
	DataRetentionDays int
	ErasureHandling   ErasureHandling
}

// ErasureHandling is how the org's "delete my data" requests are decided
// once their person is proven. A match proven to be the person's is a sure
// one (MatchStrong, MatchProbable: read off a verified chip); one found only
// by its e-mail address or typed name (MatchEmail, MatchName) is not.
type ErasureHandling string

const (
	// ErasureReview leaves each request to a reviewer, the sure matches ticked.
	ErasureReview ErasureHandling = "review"
	// ErasureAutomatic erases the sure matches at once. A request without
	// unsure ones is then decided; one with them waits for a reviewer, who
	// decides only those.
	ErasureAutomatic ErasureHandling = "automatic"
)

// valid reports a handling an org may set. What the law makes an org keep is
// its legal retention, not a handling: the "keep" that refused every request
// is gone (migration 20261021110100).
func (h ErasureHandling) valid() bool {
	return h == ErasureReview || h == ErasureAutomatic
}

// SessionSettingsChange changes some of the org's session settings: a nil field
// keeps what is set.
type SessionSettingsChange struct {
	SessionTTL        *time.Duration
	DataRetentionDays *int
	ErasureHandling   *ErasureHandling
}

func (c SessionSettingsChange) empty() bool {
	return c.SessionTTL == nil && c.DataRetentionDays == nil && c.ErasureHandling == nil
}

// apply is settings with c's changes.
func (c SessionSettingsChange) apply(settings SessionSettings) SessionSettings {
	if c.SessionTTL != nil {
		settings.SessionTTL = *c.SessionTTL
	}
	if c.DataRetentionDays != nil {
		settings.DataRetentionDays = *c.DataRetentionDays
	}
	if c.ErasureHandling != nil {
		settings.ErasureHandling = *c.ErasureHandling
	}
	return settings
}

// DataRetention is DataRetentionDays as a duration.
func (c SessionSettings) DataRetention() time.Duration {
	return time.Duration(c.DataRetentionDays) * hoursPerDay * time.Hour
}

func (c SessionSettings) auditFields() map[string]any {
	return map[string]any{
		"sessionTtlSeconds": int(c.SessionTTL.Seconds()),
		"dataRetentionDays": c.DataRetentionDays,
		"erasureHandling":   string(c.ErasureHandling),
	}
}

// Settings is what an org sets once for all its flows. An org that set nothing
// reads as defaultSettings.
type Settings struct {
	OrganizationID uuid.UUID
	// OrgName is the org's own name, which the proofing mail signs with until
	// the branding names another.
	OrgName  string
	Session  SessionSettings
	Branding Branding
	// LegalRetention is the least the org keeps an identity check's data, when
	// the law says so.
	LegalRetention LegalRetention
	// RedirectOrigins are the origins (scheme://host[:port]) a hosted page may
	// send its subject back to and be embedded on.
	RedirectOrigins []string
	UpdatedAt       time.Time
}

// defaultSettings are an org's settings until it saves some.
func defaultSettings(orgID uuid.UUID) Settings {
	return Settings{
		OrganizationID: orgID,
		Session: SessionSettings{
			SessionTTL: SessionTTL, DataRetentionDays: defaultDataRetentionDays, ErasureHandling: ErasureReview,
		},
	}
}

// SignedAs is the name the proofing mail signs with: the branding's display
// name, else the org's.
func (s Settings) SignedAs() string {
	if s.Branding.DisplayName != "" {
		return s.Branding.DisplayName
	}
	return s.OrgName
}

// hexColor is the #rrggbb form the primary colour takes.
var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// statsWindow is how far back the proofing overview counts requests.
const statsWindow = 30 * 24 * time.Hour

// maxDisplayNameLength and maxSubjectNameLength bound the names an admin or a
// member types.
const (
	maxDisplayNameLength = 200
	maxSubjectNameLength = 200
	maxWebhookURLLength  = 2048
	maxSupportContactLen = 200
)

var (
	ErrNoEncryptionKey    = proofingprovider.ErrNoEncryptionKey
	ErrFlowNotFound       = errors.New("proofing: flow not found")
	ErrFlowNotCompletable = errors.New("proofing: flow needs a browser step the recipient cannot reach")
	ErrFlowNotAllowed     = errors.New("proofing: flow is not available to members")
	ErrMemberNotFound     = errors.New("proofing: not a member of this organization")
	// ErrNoDefaultFlow is a request that names no flow, for an org that has no
	// default flow to fall back on.
	ErrNoDefaultFlow = errors.New("proofing: no flow named and the organization has no default flow")
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
	ErrNoLogo          = errors.New("proofing: organization has no logo")
	ErrInvalidInput    = errors.New("proofing: invalid input")
	// ErrWrongMethod is a Yivi-only call on a request that runs in the Idem app.
	ErrWrongMethod = errors.New("proofing: the request does not run in the Yivi app")
	// ErrSessionOver is a call on a request whose session can no longer run.
	ErrSessionOver = errors.New("proofing: the request's session is over")
	// ErrReferencePhotoRequired is a request on a flow that matches the face
	// against the sender's own photo (flowNeedsReferencePhoto) sent without one:
	// only the API can carry it.
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
	// StatusCancelled is derived from Request.CancelledAt: an API caller ended it.
	StatusCancelled Status = "cancelled"
)

// known reports a status a request can read in (Request.EffectiveStatus),
// stored or derived.
func (s Status) known() bool {
	switch s {
	case StatusPending, StatusInProgress, StatusApproved, StatusRejected, StatusNeedsReview,
		StatusExpired, StatusCancelled:
		return true
	}
	return false
}

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
	// DataMatchesKept is how many of an approved erasure's matched sessions
	// were kept, not erased; nil for any other request.
	DataMatchesKept *int
	// DataHold is what a decided FlowDataErasure request left because the
	// org's legal retention still holds it; nil when it left nothing held.
	DataHold *DataHold
	// LegalNotice is what the org's legal retention kept of a FlowDataErasure
	// request's person, for their page: why, and until when, once it declined
	// the request or an approved one left sessions it holds; nil otherwise,
	// and set only on the hosted reads (withLegalNotice).
	LegalNotice *LegalNotice
	// CancelledAt is when an API caller cancelled it; PurgedAt when its data was
	// erased in the engine and here (the row stays for the audit trail).
	CancelledAt     *time.Time
	PurgedAt        *time.Time
	RequestedBy     *uuid.UUID
	RequestedByName string
	// APIKeyID is the key that created the request through the API, instead
	// of a member; APIKeyName its label, empty once the key is gone.
	APIKeyID   *uuid.UUID
	APIKeyName string
	// SubjectUserID is the member the request was sent to; nil for an external
	// person, a request from before requests went to members, or once the
	// member's user is gone.
	SubjectUserID *uuid.UUID
	// SubjectKind is whether the request went to a member or to an external
	// person, which SubjectUserID cannot tell once the member's user is gone.
	SubjectKind SubjectKind
	// SubjectName is empty for an external person sent without a name.
	SubjectName  string
	SubjectEmail string
	// ProofedName is the name read off an external person's approved document,
	// until proofedNameRetention clears it; empty otherwise.
	ProofedName string
	// PurgeAt is when the pruner purges the request (Purge): its retention
	// after it settled (setPurgeAt); nil while it runs.
	PurgeAt  *time.Time
	FlowID   string
	FlowName string
	// FlowVersion is the engine flow version the session pinned; 0 when unknown.
	FlowVersion int
	// Method is how the subject took part; "" while no device claimed it.
	Method proofingprovider.Method
	// NameRetention is how long a proofed name is kept: the org's data
	// retention, or proofedNameRetention for a member's request.
	NameRetention time.Duration
	// legalNameRetention is how long the org's legal retention keeps the
	// proofed name of an external identity check once it is approved
	// (legalHeld); 0 for any other request or without a legal retention.
	legalNameRetention time.Duration
	// RetentionOverride is the flow's own retention at send, when it set one:
	// it applies in place of the org's.
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
	// Address is whether the request asks for the subject's postal address
	// once the identity is approved: its flow's AddressMode when it was sent.
	// SubjectAddress is the address they entered, decrypted, at
	// AddressAddedAt; nil before, and once the request is purged.
	Address        AddressMode
	SubjectAddress *Address
	AddressAddedAt *time.Time
	// AddressReview is a self-declared address's review; empty for none.
	AddressReview AddressReview
	// Employment is whether the request asks for the subject's UWV
	// Verzekeringsbericht once the identity is approved: its flow's
	// EmploymentMode when it was sent. SubjectEmployment is what was kept of
	// it, decrypted, at EmploymentAddedAt; nil before, and once purged.
	Employment        EmploymentMode
	SubjectEmployment *Employment
	EmploymentAddedAt *time.Time
	// ReviewReason is why the wallet itself put the request in review after
	// the engine approved it (Employment.reviewReason); "" for a review the
	// engine opened, and for none. Such a review is decided in the wallet.
	ReviewReason string
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
	// TTL is the lifetime the session was created with; 0 for one created
	// before the wallet kept it.
	TTL time.Duration
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

// linkLive reports whether a hosted link still has its subject at work:
// waiting to start, its session running or in review, or an address,
// diploma, employment or data export window open. A link past all of that only shows its outcome.
func (r Request) linkLive(now time.Time) bool {
	if r.PurgedAt != nil || r.CancelledAt != nil {
		return false
	}
	if r.awaitingStart(now) || r.liveSession(now) != nil || r.EffectiveStatus(now) == StatusNeedsReview {
		return true
	}
	if until := addressUntil(r); until != nil && now.Before(*until) {
		return true
	}
	if until := employmentUntil(r); until != nil && now.Before(*until) {
		return true
	}
	if until := diplomasUntil(r); until != nil && now.Before(*until) {
		return true
	}
	return r.DataExportUntil != nil && now.Before(*r.DataExportUntil)
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

// FlowSelection is the org admin's choices over the org's flows: the flows
// members may send a request on, and the org's default flow, the one a request
// form preselects. DefaultFlowID is any identity flow, whether or not members
// may use it, or empty for none.
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
	// Address is whether the flow asks for the subject's postal address.
	Address AddressMode
	// Employment is whether the flow asks for the UWV Verzekeringsbericht.
	Employment EmploymentMode
	// Kind is what the flow's sessions are for.
	Kind FlowKind
	// HostedEnabled is the flow's "Allow sending this flow as a link" switch
	// (FlowHosted.Enabled): off, a member only shows it on their screen.
	HostedEnabled bool
	// HostedReturnURL is the page the flow's links send their subject back to
	// (FlowHosted.ReturnURL); empty for none.
	HostedReturnURL string
	// RetentionDays is what an external person on this flow is told about
	// keeping their data: subjectRetentionDays.
	RetentionDays int
}

// RequestFilter narrows a request list: to the requests one member sent, to the
// ones proofing one member, and/or to those sent to one kind of subject. A nil
// field does not narrow.
type RequestFilter struct {
	RequestedBy *uuid.UUID
	// SubjectUserID narrows to the requests proofing one member.
	SubjectUserID *uuid.UUID
	// SubjectKind narrows to the requests sent to members or to external people;
	// the list's cap then counts only those.
	SubjectKind *SubjectKind
	// FlowID narrows to the requests sent on one flow, any version; the list's
	// cap then counts only those.
	FlowID *string
	// Status narrows to the requests that read in that status
	// (Request.EffectiveStatus), expired and cancelled included: a pending
	// request whose link lapsed matches expired, not pending. The list's cap
	// then counts only those. Those in review are listed oldest in review
	// first, so the cap leaves out the newest, not the most overdue.
	Status *Status
	// DataRequests narrows to data requests (FlowDataAccess, FlowDataErasure);
	// the list's cap then counts only those.
	DataRequests bool
	// AddressReview narrows to the requests whose address stands so with a
	// reviewer; pending ones are listed the longest waiting first.
	AddressReview *AddressReview
}

// NewRequest is a sender's ask to proof someone on a flow: another member
// (SubjectUserID), or, without one, an external person by e-mail address and an
// optional name.
type NewRequest struct {
	SubjectUserID uuid.UUID
	SubjectEmail  string
	SubjectName   string
	// ReferencePhoto is the sender's own photo of the subject's face,
	// standard base64: the live face is matched against it, for a flow that
	// flowNeedsReferencePhoto (required there, refused anywhere else).
	ReferencePhoto string
	// SubjectBirthDate (YYYY-MM-DD) makes the request one for exactly the
	// person SubjectName names, born then: any other identity is rejected
	// with errorIdentityMismatch. Empty proofs whoever takes part.
	SubjectBirthDate string
	FlowID           string
	// FlowKind is what the request is for: an identity check on FlowID (empty
	// is the flow's own kind), or a data request (FlowDataAccess,
	// FlowDataErasure), which runs on the org's standard flow of that kind and
	// names no flow of another kind.
	FlowKind FlowKind
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
	// settles; its origin must be one of the org's RedirectOrigins. Empty
	// takes the flow's return page, if it has one.
	RedirectURL string
	// NoRedirect ends a hosted page on its own done screen even when its flow
	// has a return page.
	NoRedirect bool
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
	// accepted what will be collected: nothing is mailed, and an external
	// subject needs no address. The Yivi app's session starts from that
	// screen's QR code, so a Yivi session is only ever delivered this way.
	ChannelOnScreen Channel = "on_screen"
)

// external reports a request for an external person, not a member.
func (n NewRequest) external() bool { return n.SubjectUserID == uuid.Nil }

// SubjectKind is whether a request went to a member of the org or to an
// external person, who has no login.
type SubjectKind string

const (
	SubjectMember   SubjectKind = "member"
	SubjectExternal SubjectKind = "external"
)

// External reports a request that went to an external person.
func (r Request) External() bool { return r.SubjectKind == SubjectExternal }

// Subject is who a stored request went to: a member (UserID) or an external
// person (Kind SubjectExternal, no UserID).
type Subject struct {
	Kind   SubjectKind
	UserID *uuid.UUID
	Name   string
	Email  string
	// BirthDate (YYYY-MM-DD) is set for a request for one known person: Name,
	// born then. Empty for anyone.
	BirthDate string
}

// FlowView is which of the org's flows a read lists.
type FlowView int

const (
	// FlowsAllowed is the flows a member may send on.
	FlowsAllowed FlowView = iota
	// FlowsAll is every flow: an admin's view, to configure and send from.
	FlowsAll
)

// PauseState is what SetProofingPaused sets a pause level to.
type PauseState string

const (
	PauseOn  PauseState = "paused"
	PauseOff PauseState = "resumed"
)

// StatsRow counts the external requests on one flow within statsWindow, by
// outcome. Sessions is every request sent; what the outcome counts leave is
// still pending or in progress.
type StatsRow struct {
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
// photographs the document, which only the Idem app does, matches the face
// against the sender's reference photo (the Yivi app runs no face check),
// checks an age the Yivi credentials carry no claim for
// (openid4vpverifier.AgeClaim), or requires a level above what a Yivi session
// reaches (yiviEIDASLevel), which enforceAssurance would only reject at the
// end.
func yiviAppAvailable(f proofingprovider.Flow) bool {
	return !slices.Contains(f.Steps, stepDocumentPhoto) && !flowNeedsReferencePhoto(f) &&
		yiviAnswersAge(f) && meetsAssurance(yiviEIDASLevel, f.RequiredAssuranceLevel)
}

// keepsFacePhoto reports whether f releases the holder's photo: it keeps the
// face photos and asks for one (the engine's buildResult rule).
func keepsFacePhoto(f proofingprovider.Flow) bool {
	return f.KeepFacePhotos != nil && *f.KeepFacePhotos &&
		(slices.Contains(f.RequestedAttributes, attributeFacePhoto) || slices.Contains(f.RequestedAttributes, attributeFaceImage))
}

// yiviAnswersAge reports whether a Yivi disclosure can answer f's age check:
// always when f has none.
func yiviAnswersAge(f proofingprovider.Flow) bool {
	if !slices.Contains(f.RequestedAttributes, attributeAgeOver) {
		return true
	}
	_, ok := openid4vpverifier.AgeClaim(f.AgeOver)
	return ok
}

// Public API rate limits, per org and per API replica: apiCallLimit for every
// call, APISessionLimit also for creating a session, so one org cannot use up
// the deployment's capacity.
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

// errorAddressMismatch is the error code the wallet records when the address
// the subject entered after approval is not the registered (BRP) address
// their Yivi app disclosed.
const errorAddressMismatch = "ADDRESS_MISMATCH"

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

// attributeFacePhoto and attributeFaceImage are the holder's photo, by its two
// names.
const (
	attributeFacePhoto = "dg2"
	attributeFaceImage = "face_image"
)

// attributeAgeOver is the age check, which withholds the date of birth from
// the document data next to it.
const attributeAgeOver = "age_over"

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
// sender's own photo of the person (NewRequest.ReferencePhoto). Only the API
// can send one; a member cannot be sent such a flow.
func flowNeedsReferencePhoto(f proofingprovider.Flow) bool {
	return hasFaceStep(f.Steps) && !slices.Contains(f.Steps, stepNFCRead) && f.SelfieLocation != selfieLocationBrowser
}

// externalCompletable is flowCompletable for an external person, whose session
// may carry a reference photo: a flow that needs one can be sent.
func externalCompletable(f proofingprovider.Flow) bool {
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
