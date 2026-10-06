// Package proofingprovider holds the value types between internal/proofing and
// the proofing engine (internal/proofingengine, in-process): flows, sessions,
// results. An org is the engine's tenant, under the org's own id.
//
// It imports no other internal package (a leaf, like signingprovider). Besides
// the types it has an in-memory Stub for tests; internal/proofing uses the
// engine through its own interface.
package proofingprovider

import (
	"errors"
	"fmt"
	"time"
)

// Tenant is which org a call is for.
type Tenant struct {
	ID string
}

// FlowSpec is everything an org admin sets on a flow: the body of both a new
// flow and a new version of one. The engine validates the combination (which steps need
// which checks, what an assurance level requires) and answers a *RejectedError.
// Empty optional fields fall back to the engine's defaults.
type FlowSpec struct {
	Name  string   `json:"name"`
	Steps []string `json:"steps"`
	// RequestedAttributes limits what a session result may carry (dg1, dg11, dg2,
	// selfie, chip_checks, biometrics, document_image). Empty releases everything
	// the steps collect, ["outcome_only"] the outcome only; a flow read back
	// always lists what it releases. The wallet never reads the data itself,
	// only the outcome.
	RequestedAttributes []string `json:"requestedAttributes,omitempty"`
	// SelfieLocation is which client captures the face: "native" (the vcmrtd
	// app) or "browser". The engine defaults an empty value to browser.
	SelfieLocation string `json:"selfieLocation,omitempty"`
	// FaceProvider verifies the face step: "regula", "engine", or empty for
	// the engine's default (Regula when configured).
	FaceProvider             string             `json:"faceProvider,omitempty"`
	AcceptedDocumentTypes    []string           `json:"acceptedDocumentTypes,omitempty"`
	AcceptedIssuingCountries []string           `json:"acceptedIssuingCountries,omitempty"`
	RequiredChecks           []string           `json:"requiredChecks,omitempty"`
	CheckThresholds          map[string]float64 `json:"checkThresholds,omitempty"`
	RequiredAssuranceLevel   string             `json:"requiredAssuranceLevel,omitempty"`
	// BSNPolicy is retrieve, mask or omit; empty inherits the tenant's.
	BSNPolicy string `json:"bsnPolicy,omitempty"`
	// BlurFace and BlurBSN are nil to inherit the tenant's redaction policy.
	BlurFace                 *bool           `json:"blurFace,omitempty"`
	BlurBSN                  *bool           `json:"blurBsn,omitempty"`
	RetentionOverrideSeconds int             `json:"retentionOverrideSeconds,omitempty"`
	AssuranceTiers           []AssuranceTier `json:"assuranceTiers,omitempty"`
	LegalBasis               string          `json:"legalBasis,omitempty"`
	ProcessingPurpose        string          `json:"processingPurpose,omitempty"`
}

// AssuranceTier maps a minimum score percentage to an assurance tier name.
type AssuranceTier struct {
	Level      string  `json:"level"`
	MinPercent float64 `json:"minPercent"`
}

// Flow is one flow version. The id is stable across versions; exactly one
// version of a flow is active, and a session pins the version active when it
// is created.
type Flow struct {
	FlowSpec
	ID        string    `json:"id"`
	Version   int       `json:"version"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
}

// SessionInput starts a proofing session. ClientReference is echoed back by the engine
// on every read; the wallet sets it to its own request id. TTL is
// how long the session may live; the engine caps it at its own maximum (15 minutes).
type SessionInput struct {
	FlowID          string
	ClientReference string
	Language        string
	TTL             time.Duration
	// Method is the app the subject proofs with: MethodIdem (the default when
	// empty) or MethodYivi. MethodBrowser cannot be asked for.
	Method Method
	// Retention is how long the engine keeps the session (its evidence) once it
	// ended; 0 is the flow's own retention, else the engine's default.
	Retention time.Duration
	// ReferencePhoto is the relying party's own photo of the subject's face,
	// for a flow whose face step runs without reading the chip: the live
	// face is matched against it instead of the chip's DG2. Required exactly
	// for such a flow, refused for any other.
	ReferencePhoto *Image
}

// Session is a created proofing session. Token is the relying-party bearer token every
// later read needs; the engine returns it only here.
type Session struct {
	ID        string
	Token     string
	ExpiresAt time.Time
	// FlowVersion is the flow version the engine pinned the session to; 0 when unknown.
	FlowVersion int
	// Claim is the vcmrtd link for the subject's phone, nil when the engine offered none
	// (always for MethodYivi, whose page starts the Yivi disclosure instead).
	Claim *Claim
}

// Claim is a single-use link that binds the subject's phone to a session. It is
// short-lived (minutes), so the page showing it asks for a fresh one when it lapses.
type Claim struct {
	DeepLink  string
	ExpiresAt time.Time
	// Handover is set when a phone already held the session, so the link moves
	// it to another phone; unset, it is a fresh claim nobody has scanned yet.
	Handover bool
}

// Status is a proofing session status.
type Status string

const (
	StatusCreated     Status = "created"
	StatusOpened      Status = "opened"
	StatusInProgress  Status = "in_progress"
	StatusNeedsReview Status = "needs_review"
	StatusApproved    Status = "approved"
	StatusRejected    Status = "rejected"
	StatusExpired     Status = "expired"
	StatusCancelled   Status = "cancelled"
)

// Result is a session's outcome, reduced to the assurance summary and the
// subject's name. The engine's result also carries the other document fields, the BSN
// and images; they are never decoded.
type Result struct {
	// Method is how the subject took part: MethodIdem, MethodYivi or
	// MethodBrowser; "" while no device has claimed the session.
	Method         Method
	Status         Status
	ErrorCode      string
	AssuranceLevel string
	EIDASLevel     string
	CompletedAt    *time.Time
	// Name is the holder's name as read off the document: its DG11 display name
	// when the chip has one, else the MRZ first and last name. Empty when the flow did
	// not request the document data (dg1) or the session has no result yet.
	Name string
	// App is where the Idem app's phone is; SessionStatus only.
	App App
}

// App is where an Idem session's phone is: none scanned yet, holding the
// session, or away (closed or silent), when a claim link hands it over.
type App string

const (
	AppWaiting   App = "waiting"
	AppConnected App = "connected"
	AppAway      App = "away"
)

// Identity is a session's outcome with who was proofed and on what evidence,
// for a customer's result read. Only these fields are read from the engine: never
// the document number, personal number or place of birth.
type Identity struct {
	Result
	GivenName   string
	FamilyName  string
	BirthDate   string
	Nationality string
	// Evidence is what the identity rests on; nil while there is no result.
	Evidence *Evidence
	// Photo is the document's portrait (the chip's DG2, or the disclosed
	// credential's photo) and Selfie the live face matched against it; nil
	// when the flow did not request them or the engine released none a browser shows.
	Photo  *Image
	Selfie *Image
	// ReferencePhoto is the relying party's own photo the live face was
	// matched against, for a flow without the chip read; nil otherwise.
	ReferencePhoto *Image
	// DocumentImage and DocumentImageBack are the photos of the document's
	// front and back (the document_photo step; no back for a passport), the
	// printed BSN blurred under the tenant's policy.
	DocumentImage     *Image
	DocumentImageBack *Image
}

// Image is a face image as the engine releases it: already converted to a format a
// browser renders, and blurred when the tenant's policy says so. Base64 is
// the standard-encoded bytes.
type Image struct {
	MimeType string
	Base64   string
}

// Evidence is the checks behind an Identity. Type is EvidenceEMRTD (the chip,
// read by the Idem app), EvidenceYivi (a Yivi disclosure) or
// EvidenceReferencePhoto (a live face against the relying party's photo). PassiveAuth and
// ActiveAuth are CheckValid, CheckInvalid or CheckNotPerformed.
type Evidence struct {
	Type         string
	DocumentType string
	IssuingState string
	ExpiryDate   string
	PassiveAuth  string
	ActiveAuth   string
	FaceMatch    *float64
	// Liveness is passed, failed or not_performed.
	Liveness string
}

const (
	EvidenceEMRTD = "emrtd"
	EvidenceYivi  = "yivi_disclosure"
	// EvidenceReferencePhoto is a live face matched against the relying
	// party's own photo: no document was read.
	EvidenceReferencePhoto = "reference_photo"

	CheckValid        = "valid"
	CheckInvalid      = "invalid"
	CheckNotPerformed = "not_performed"
)

// ReviewDecision is a reviewer's decision on a session in needs_review:
// Approve, or a rejection with ErrorCode (MANUAL_REVIEW_REJECTED when
// empty). Reason and Reviewer are checked but not kept by the engine.
type ReviewDecision struct {
	Approve   bool
	ErrorCode string
	Reason    string
	Reviewer  string
}

// Method is the app a subject proofed with, as the wallet shows it.
type Method string

const (
	// MethodIdem is the Idem app (vcmrtd), the native device: it reads the
	// document's chip over NFC.
	MethodIdem Method = "idem_app"
	// MethodYivi is a disclosure of existing identity credentials from the Yivi
	// app, over OpenID4VP at the wallet's verifier, whose photo the engine then checks
	// the live face against (biometric_bound_login).
	MethodYivi Method = "yivi_app"
	// MethodBrowser is the web device alone, with no app involved.
	MethodBrowser Method = "browser"
)

// Reference is what the subject's verified OpenID4VP disclosure gives a
// MethodYivi session to check the face against. Credential is the credential's
// vct (e.g. pbdf-staging.pbdf.passport), Photo its photo claim as base64,
// Attributes the identity claims disclosed with it, by claim name.
type Reference struct {
	Credential string
	Photo      string
	Attributes map[string]string
}

// YiviDisclosure is the engine taking a Reference. OK false ended the session
// (Code says why: photo_missing, reference_no_face); OK true moves on to the face check, which approves after
// StableFrames matching frames and rejects after MaxAttempts without them.
type YiviDisclosure struct {
	OK           bool
	Code         string
	StableFrames int
	MaxAttempts  int
}

// FaceDecision is where a MethodYivi session's face check stands.
type FaceDecision string

const (
	FaceDecisionPending  FaceDecision = "pending"
	FaceDecisionApproved FaceDecision = "approved"
	FaceDecisionRejected FaceDecision = "rejected"
)

// FaceVerdict is one live camera frame scored against the disclosed photo. No
// score, face box or image is kept: only the progress the page shows.
type FaceVerdict struct {
	FaceDetected bool
	Matched      bool
	Consecutive  int
	StableFrames int
	Attempts     int
	MaxAttempts  int
	Decision     FaceDecision
}

// ErrMethodUnavailable is the engine refusing a session for a method it cannot
// run: MethodYivi without Regula, which scores its face check.
var ErrMethodUnavailable = errors.New("proofingprovider: method unavailable")

// ErrNoEncryptionKey is a deployment without IDENTITY_PROOFING_ENCRYPTION_KEY:
// no session (whose evidence is sealed under it) can be stored.
var ErrNoEncryptionKey = errors.New("proofing: no identity proofing encryption key configured")

// ErrNotFound is an unknown session or flow.
var ErrNotFound = errors.New("proofingprovider: not found")

// RejectedError is the engine refusing a request as invalid (400/409/422), or a
// subject-facing call on a session that is over (410). Message is
// the engine's own explanation (e.g. which check a flow step requires) and is safe to
// show to the org admin who made the request.
type RejectedError struct {
	Status  int
	Message string
	// Code is the machine-readable reason, when it sent one.
	Code string
}

// CodeDeviceActive is the engine refusing a handover while the app holding the
// session is still active: the subject carries on there.
const CodeDeviceActive = "device_active"

func (e *RejectedError) Error() string {
	return fmt.Sprintf("proofingprovider: rejected (status %d): %s", e.Status, e.Message)
}
