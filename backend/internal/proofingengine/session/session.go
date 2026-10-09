// Package session defines the identity-proofing session: its status machine,
// evidence and result envelope, and its stores.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// Method identifies which proofing flow a session was opened for.
type Method string

const (
	MethodFace        Method = "face"         // selfie/liveness verification against an enrolled face
	MethodNFCPassport Method = "nfc_passport" // ICAO 9303 chip read via the vcmrtd app (issue #1)
	// MethodBiometricBoundLogin is the Yivi method: the live face is matched 1:1
	// against a disclosed credential's photo before the disclosure is released.
	// Nothing is enrolled; the reference lives only as long as the session.
	MethodBiometricBoundLogin Method = "biometric_bound_login"
)

// Status is a session's place in its lifecycle; what failed or was read
// belongs in Result.
//
//   - created: the session exists, the subject has not opened it.
//   - opened: the subject opened the link, so "did they start?" differs from
//     "they started and stalled".
//   - in_progress: evidence is coming in.
//   - needs_review: waits for a reviewer.
//   - approved, rejected, expired, cancelled: final.
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

// transitions are each status's direct next statuses. Final statuses have
// none: a finished session is never reopened.
var transitions = map[Status][]Status{
	StatusCreated:     {StatusOpened, StatusExpired, StatusCancelled},
	StatusOpened:      {StatusInProgress, StatusExpired, StatusCancelled},
	StatusInProgress:  {StatusNeedsReview, StatusApproved, StatusRejected, StatusExpired, StatusCancelled},
	StatusNeedsReview: {StatusApproved, StatusRejected, StatusExpired, StatusCancelled},
	StatusApproved:    nil,
	StatusRejected:    nil,
	StatusExpired:     nil,
	StatusCancelled:   nil,
}

// Valid reports whether s is one of the known statuses.
func (s Status) Valid() bool {
	_, ok := transitions[s]
	return ok
}

// Terminal reports whether s is an end state: no further transition is valid.
func (s Status) Terminal() bool {
	return s.Valid() && len(transitions[s]) == 0
}

func CanTransition(from, to Status) bool {
	return slices.Contains(transitions[from], to)
}

// Session is one proofing attempt of one tenant, moved through Status with
// Store.Update.
type Session struct {
	ID       string `json:"id"`
	TenantID string `json:"tenantId"`
	Method   Method `json:"method"`
	Status   Status `json:"status"`

	// Token names the session in the app's paths. It is handed out only at
	// creation, never in Get or List.
	Token string `json:"-"`

	// ClientReference is the relying party's own reference, echoed back as
	// given.
	ClientReference string `json:"clientReference,omitempty"`

	// TenantReference is a stable correlation id set once at creation.
	TenantReference string `json:"tenantReference"`

	// Flow names the session's flow (flow.FlowDefinition). One that matches no
	// stored flow leaves the session on RequestedAttributes alone, without
	// document, country or check rules.
	Flow string `json:"flow,omitempty"`
	// FlowVersion is the flow version the session was created on, never changed
	// afterwards. Zero when Flow matched no stored flow.
	FlowVersion int `json:"flowVersion,omitempty"`

	// RetentionOverride, when set, is how long the session is kept after it
	// finished, in place of the retention Purge is called with. Stamped at
	// creation from the flow.
	RetentionOverride time.Duration `json:"retentionOverride,omitempty"`

	// Language is the session's language as a BCP 47 tag, stored as given;
	// internal/i18n resolves what a client gets.
	Language string `json:"language,omitempty"`

	// RequestedAttributes are the result attributes released (the engine's
	// attrRequested).
	RequestedAttributes []string `json:"requestedAttributes,omitempty"`

	// ErrorCode is set on rejection/expiry to a machine-readable reason
	// (e.g. FACE_NO_MATCH, DOC_EXPIRED). Empty while not in a failed state.
	ErrorCode string `json:"errorCode,omitempty"`

	// Result is the released result, built once when the session finishes.
	Result map[string]any `json:"result,omitempty"`

	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	OpenedAt    *time.Time `json:"openedAt,omitempty"`
	SubmittedAt *time.Time `json:"submittedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	ExpiresAt   time.Time  `json:"expiresAt"`

	// AAChallenge is the session's Active Authentication challenge (RND.IFD),
	// hex, made at creation. The app sends it to the chip; evidence over another
	// nonce never proves the chip was present for this session, which closes the
	// relay window gmrtd's activeauth.VerifyEvidence warns about.
	AAChallenge string `json:"aaChallenge,omitempty"`

	// ResetCount is how many times the session was reset to opened (POST
	// .../reset). The app view carries it, and the Idem app watches it.
	ResetCount int `json:"resetCount,omitempty"`

	// Yivi is a Yivi-method session's face check while it runs: the
	// reference photo of the disclosure and the frames scored so far. It is
	// cleared when the session settles, so no biometric outlives it.
	Yivi *YiviState `json:"yivi,omitempty"`

	// ReferencePhoto is a face photo the relying party supplied at creation,
	// plain base64 of displayable type ReferencePhotoMime: what the face
	// step compares the live face against in a flow without nfc_read, in
	// place of the chip's DG2. It is dropped once the face step can no
	// longer run (ForStorage); the result keeps its own copy beside the
	// selfie when the flow requests the selfie (buildResult).
	ReferencePhoto     string `json:"referencePhoto,omitempty"`
	ReferencePhotoMime string `json:"referencePhotoMime,omitempty"`

	// Access is which device holds each client slot, plus the pending handover
	// grants.
	Access Access `json:"access,omitempty"`

	// Steps is the evidence collected per step, so a reload or a handover resumes
	// where the subject left off. A nil field is a step not submitted yet.
	Steps StepEvidence `json:"steps,omitempty"`
}

// ForStorage is sess as it is persisted: a session whose face step can no
// longer run (decided, under review, expired or cancelled) keeps no
// relying-party reference photo and no Yivi face check. One that ended
// without an outcome (expired or cancelled) keeps none of its step evidence
// either: no result is built from it, and the chip's data groups, the
// selfie, document photos and the chip access key would otherwise wait,
// unredacted, for the retention purge.
func (sess Session) ForStorage() Session {
	if sess.Status.Terminal() || sess.Status == StatusNeedsReview {
		sess.ReferencePhoto, sess.ReferencePhotoMime = "", ""
		sess.Yivi = nil
	}
	if sess.Status == StatusExpired || sess.Status == StatusCancelled {
		sess.Steps = StepEvidence{}
	}
	return sess
}

// YiviState is a Yivi-method session's face check: the disclosed photo the
// live frames are matched against, and the run of matches so far. Kept in
// the session (sealed) rather than in memory, so any API replica can score
// the next frame.
type YiviState struct {
	// Reference is the disclosed photo, plain base64, displayable type
	// ReferenceMime; Disclosure the provenance and claims, as the engine
	// releases them (JSON, its own shape).
	Reference     string          `json:"reference"`
	ReferenceMime string          `json:"referenceMime"`
	Disclosure    json.RawMessage `json:"disclosure"`
	KeepPhoto     bool            `json:"keepPhoto,omitempty"`

	Attempts    int     `json:"attempts"`
	Consecutive int     `json:"consecutive"`
	BestScore   float64 `json:"bestScore"`
	LastScore   float64 `json:"lastScore"`
	// FrameHashes are the SHA-256 (hex) of the frames scored, so a replayed
	// frame is told apart; Duplicates counts those.
	FrameHashes []string `json:"frameHashes,omitempty"`
	Duplicates  int      `json:"duplicates"`
	// RegulaCalls counts every frame sent to Regula, a faceless one
	// included, so the session's Face API calls stay bounded.
	RegulaCalls int `json:"regulaCalls,omitempty"`
	// LastFrame is the latest matching frame, released as the selfie when
	// requested.
	LastFrame   string `json:"lastFrame,omitempty"`
	FaceStarted bool   `json:"faceStarted,omitempty"`
}

// StepEvidence is the evidence per step; nil fields are steps not done yet.
type StepEvidence struct {
	Document      *DocumentStepEvidence      `json:"document,omitempty"`
	Selfie        *SelfieStepEvidence        `json:"selfie,omitempty"`
	NFC           *NFCStepEvidence           `json:"nfc,omitempty"`
	DocumentPhoto *DocumentPhotoStepEvidence `json:"documentPhoto,omitempty"`
	// Started are the steps begun, so each start is logged once.
	Started []string `json:"started,omitempty"`
}

// StepTiming is when a step started and finished, in server time: never the
// client's clock.
type StepTiming struct {
	StartedAt   time.Time `json:"startedAt"`
	SubmittedAt time.Time `json:"submittedAt"`
}

// DocumentPhotoStepEvidence is the document_photo step's evidence: photos of
// the document's front and back, taken by the app. Back is nil for a
// document without one worth taking (a passport's data page).
type DocumentPhotoStepEvidence struct {
	Front  DocumentPhotoSide  `json:"front"`
	Back   *DocumentPhotoSide `json:"back,omitempty"`
	Timing StepTiming         `json:"timing"`
}

// DocumentPhotoSide is one side's photo, plain base64. BSNRegion, when the app
// found the printed BSN, is where a BlurBSN policy covers it.
type DocumentPhotoSide struct {
	Image     string       `json:"image"`
	MimeType  string       `json:"mimeType"`
	BSNRegion *ImageRegion `json:"bsnRegion,omitempty"`
}

// ImageRegion is a box in an image, normalised to [0,1] from the top left.
type ImageRegion struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// DocumentStepEvidence is document_capture's evidence: the MRZ identity the
// Idem app read, or the chip's reading once nfc_read lands. There is no OCR
// here.
type DocumentStepEvidence struct {
	// Parsed is the document identity (ParsedMRZ).
	Parsed ParsedMRZ  `json:"parsed"`
	Timing StepTiming `json:"timing"`
	// Source is where Parsed came from: DocumentSourceMRZ (the
	// document_capture step on its own) or DocumentSourceChip (read off the
	// chip with nfc_read). A chip-sourced document is never replaced.
	Source string `json:"source,omitempty"`
	// ChipAccess is the MRZ-derived key needed to open the chip, kept only
	// until nfc_read lands so another device can resume straight at the chip
	// read after a handover.
	ChipAccess *ChipAccessKey `json:"chipAccess,omitempty"`
}

// DocumentStepEvidence.Source values.
const (
	DocumentSourceMRZ  = "mrz"
	DocumentSourceChip = "chip"
)

// ChipAccessKey is what the Idem app derives from the MRZ to open the chip
// (BAC/PACE, or BAP for a driving licence), in vcmrtd's vocabulary.
type ChipAccessKey struct {
	DocumentType   string `json:"documentType"`
	DocumentNumber string `json:"documentNumber"`
	CountryCode    string `json:"countryCode,omitempty"`
	DateOfBirth    string `json:"dateOfBirth,omitempty"`
	DateOfExpiry   string `json:"dateOfExpiry,omitempty"`
	Version        string `json:"version,omitempty"`
	RandomData     string `json:"randomData,omitempty"`
	Configuration  string `json:"configuration,omitempty"`
}

// ParsedMRZ is a document's MRZ identity fields, flattened for storage.
type ParsedMRZ struct {
	DocumentType   string `json:"documentType,omitempty"`
	IssuingState   string `json:"issuingState,omitempty"`
	Number         string `json:"number,omitempty"`
	Nationality    string `json:"nationality,omitempty"`
	Surname        string `json:"surname,omitempty"`
	GivenNames     string `json:"givenNames,omitempty"`
	Sex            string `json:"sex,omitempty"`
	DateOfBirth    string `json:"dateOfBirth,omitempty"`
	DateOfExpiry   string `json:"dateOfExpiry,omitempty"`
	PersonalNumber string `json:"personalNumber,omitempty"`
	AllChecksValid bool   `json:"allChecksValid"`
}

// SelfieStepEvidence is the face steps' evidence, one live capture for all.
type SelfieStepEvidence struct {
	Image    string `json:"image,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	// LivenessPassed reports the face provider's liveness verdict as this
	// server read it (Regula's transaction), never an app self-report.
	LivenessPassed bool `json:"livenessPassed"`
	// LivenessScore is a liveness confidence (0..1) when the provider gives
	// one; nil for Regula, which reports passed or not.
	LivenessScore *float64 `json:"livenessScore,omitempty"`
	// FaceMatchScore and FaceVerified are the server's own match against the
	// reference (the chip portrait or the customer's photo), never the app's. Nil
	// when the flow has no face match.
	FaceMatchScore *float64 `json:"faceMatchScore,omitempty"`
	FaceVerified   *bool    `json:"faceVerified,omitempty"`
	// Provider computed the verdicts: "regula" (no image kept) or "".
	Provider string `json:"provider,omitempty"`
	// Attempts is how many times the face step was submitted: a failed
	// attempt may be replaced by the next, up to the engine's bound. 0 on
	// evidence stored before attempts were counted, which is one.
	Attempts int        `json:"attempts,omitempty"`
	Timing   StepTiming `json:"timing"`
}

// NFCStepEvidence is the nfc_read step's raw chip evidence.
type NFCStepEvidence struct {
	// Raw is the engine's mrtdEvidenceRequest as JSON: untyped here, since this
	// package cannot import the engine.
	Raw    map[string]any `json:"raw"`
	Timing StepTiming     `json:"timing"`
}

// CompletedSteps are the steps with evidence, as plain strings so this package
// need not import flow.
func (s Session) CompletedSteps() []string {
	var out []string
	if s.Steps.Document != nil {
		out = append(out, "document_capture")
	}
	if s.Steps.Selfie != nil {
		out = append(out, "face_verification", "selfie", "liveness", "face_match")
	}
	if s.Steps.NFC != nil {
		out = append(out, "nfc_read")
	}
	if s.Steps.DocumentPhoto != nil {
		out = append(out, "document_photo")
	}
	return out
}

// IsExpired reports a session past its expiry without a final status. One in
// needs_review never expires: it waits for a decision.
func (s Session) IsExpired(now time.Time) bool {
	return !s.Status.Terminal() && s.Status != StatusNeedsReview && !s.ExpiresAt.IsZero() && now.After(s.ExpiresAt)
}

// SetStatus applies a status transition and stamps its time; the only way to
// change Status.
func (s *Session) SetStatus(to Status, now time.Time) error {
	if !to.Valid() {
		return fmt.Errorf("session: unknown status %q", to)
	}
	if s.Status == to {
		return nil
	}
	if !CanTransition(s.Status, to) {
		return fmt.Errorf("session: invalid transition %q -> %q", s.Status, to)
	}
	s.Status = to
	s.UpdatedAt = now
	if to == StatusOpened && s.OpenedAt == nil {
		t := now
		s.OpenedAt = &t
	}
	if to == StatusInProgress && s.SubmittedAt == nil {
		t := now
		s.SubmittedAt = &t
	}
	if to.Terminal() {
		t := now
		s.CompletedAt = &t
	}
	return nil
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// newToken is the session token: longer than, and independent of, the id,
// which may appear in paths and logs.
func newToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// newAAChallenge is an 8-byte RND.IFD for INTERNAL AUTHENTICATE.
func newAAChallenge() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// newTenantReference is a correlation id, not a credential: an id's entropy
// is enough.
func newTenantReference() string {
	return "tref_" + newID()
}
