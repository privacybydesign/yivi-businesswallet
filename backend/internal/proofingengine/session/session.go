// Package session defines the identity-proofing session: the object every
// proofing method (face, nfc_passport, ...) creates, updates and completes
// through. It is the shared status machine and result envelope referenced by
// issue #2 (restructure) and issue #1 (NFC/passport proofing via vcmrtd),
// so that every method produces the same shape instead of inventing its own.
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
	// MethodBiometricBoundLogin (issue #18): the user discloses a photo
	// credential (passport/ID-card photo from the Yivi app) and the live face
	// in front of the camera is verified 1:1 against that disclosed photo
	// before the disclosed attributes are released. No prior enrolment; the
	// reference embedding lives only for the session's lifetime.
	MethodBiometricBoundLogin Method = "biometric_bound_login"
)

// Status is a session's place in the proofing lifecycle. It is the single
// status vocabulary every method uses; method-specific detail (which check
// failed, which document was read) belongs in Session.Result, not in Status.
//
// Reconciles the two status lists proposed independently in issue #2
// (created, in_progress, needs_review, approved, rejected, expired,
// cancelled) and issue #1 (pending, opened, submitted, verified, rejected,
// expired):
//
//   - created  (#2 "created" == #1 "pending"): session exists, relying party
//     has a token, the user has not opened it yet.
//   - opened   (#1 "opened", kept as its own state rather than folded into
//     in_progress): the user opened the link/QR/deep link. Relying parties
//     need this distinction for UX and timeout handling ("did they even
//     start?" vs "they started and stalled").
//   - in_progress (#2 "in_progress", covers #1's "submitted": evidence has
//     been submitted and the system or a reviewer is processing it).
//   - needs_review (#2 only; #1 has no equivalent yet but can use it once a
//     method needs manual review).
//   - approved (#2 "approved" == #1 "verified").
//   - rejected, expired (shared by both lists as-is).
//   - cancelled (#2 only; #1 has no equivalent yet but can use it, e.g. the
//     user aborts in the vcmrtd app).
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

// transitions lists, for each status, the statuses it may move to directly.
// Terminal statuses (approved, rejected, expired, cancelled) have none: a
// finished session is not reopened, a new one is created instead.
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

// CanTransition reports whether moving from `from` directly to `to` is a
// valid step in the status machine.
func CanTransition(from, to Status) bool {
	return slices.Contains(transitions[from], to)
}

// Session is one identity-proofing attempt, owned by exactly one tenant and
// progressing through Status. Method implementations (face, nfc_passport,
// future ones) create a Session, drive it through the status machine via
// Store.Update, and write their outcome into Result.
type Session struct {
	ID       string `json:"id"`
	TenantID string `json:"tenantId"`
	Method   Method `json:"method"`
	Status   Status `json:"status"`

	// Token authorises polling the session and posting a result back to it
	// (e.g. from the vcmrtd app). It is generated once at creation, handed
	// to the caller only in the create response, and never echoed back by
	// Get/List — hence json:"-" rather than an omitempty field.
	Token string `json:"-"`

	// ClientReference is the relying party's own case/reference id, passed in
	// at creation and echoed back unchanged (see requirements.md §7).
	ClientReference string `json:"clientReference,omitempty"`

	// TenantReference is a server-generated, stable correlation id, assigned
	// once at creation and never changed afterward (see newTenantReference
	// and Store.Create/PostgresStore.Create, the only places it's set) —
	// requirements.md §7's "tenant_reference echoed in every response and
	// webhook", distinct from ClientReference which the caller supplies.
	TenantReference string `json:"tenantReference"`

	// Flow names which flow definition (internal/flow.FlowDefinition) this
	// session should follow. Defaults to "default" when empty (see
	// Store.Create); resolved against a tenant's flow.Store, if one is
	// configured (api.Config.Flows) - a Flow that doesn't match any stored
	// flow definition (e.g. the "default" default, or any deployment
	// without flow definitions configured at all) falls back to the
	// pre-flow-engine behavior: whatever RequestedAttributes was set to
	// directly, no document-type/country/check enforcement.
	Flow string `json:"flow,omitempty"`
	// FlowVersion is the version of Flow this session was actually created
	// against (requirements.md §3: "a session records the flow version it
	// ran"), resolved and stamped once at creation and never changed
	// afterward - even if the flow definition is later edited or a
	// different version activated, this session is still judged against
	// the exact version it started with. Zero when Flow didn't resolve to a
	// stored flow definition at creation time (see Flow's doc comment).
	FlowVersion int `json:"flowVersion,omitempty"`

	// RetentionOverride, when non-zero, is how long this session is kept
	// after completion, overriding the deployment's global
	// api.Config.SessionRetention (requirements.md §3: flow definition
	// "retention override"). Resolved from the flow it was created against
	// (flow.FlowDefinition.RetentionOverride) and stamped once at creation,
	// same convention as FlowVersion - a later edit to the flow's retention
	// setting never changes how long an already-created session is kept.
	// Zero means no override: Store.Purge/PostgresStore.Purge fall back to
	// the retention duration they're called with.
	RetentionOverride time.Duration `json:"retentionOverride,omitempty"`

	// Language is the relying party's preferred language for the user-facing
	// flow, as a BCP 47 tag (e.g. "en", "nl-NL"), stored as given. What a
	// client actually gets is resolved per request by internal/i18n: this
	// when it names a shipped language (nl, en), else the caller's
	// Accept-Language, else English.
	Language string `json:"language,omitempty"`

	// RequestedAttributes lists what the relying party wants collected
	// (e.g. passport data groups, "face_image"). Not yet validated against a
	// per-method capability list — that's the documents catalogue in
	// requirements.md §4 — so any non-empty string is accepted for now.
	RequestedAttributes []string `json:"requestedAttributes,omitempty"`

	// ErrorCode is set on rejection/expiry to a machine-readable reason
	// (e.g. FACE_NO_MATCH, DOC_EXPIRED). Empty while not in a failed state.
	ErrorCode string `json:"errorCode,omitempty"`

	// Result carries the method-specific outcome payload. This is a
	// placeholder until the shared result schema (issue #5) lands; treat
	// its shape as unstable.
	Result map[string]any `json:"result,omitempty"`

	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	OpenedAt    *time.Time `json:"openedAt,omitempty"`
	SubmittedAt *time.Time `json:"submittedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	ExpiresAt   time.Time  `json:"expiresAt"`

	// AAChallenge is the server-generated Active Authentication challenge
	// (RND.IFD), hex-encoded, generated once at creation. The app-facing
	// session view hands it to the app, which must use it as the nonce sent
	// to the chip's INTERNAL AUTHENTICATE command; submitted evidence is only
	// trusted as proof the chip was actually present for this session if its
	// nonce matches this value. A self-chosen nonce can't be trusted: gmrtd's
	// own activeauth.VerifyEvidence doc comment calls this out explicitly as
	// the relay-attack window ("callers must additionally verify that
	// evidence.Nonce matches the challenge they supplied during reading").
	// See the "Tampered evidence" section of docs/session-model.md.
	AAChallenge string `json:"aaChallenge,omitempty"`

	// ResetCount is how many times IPS's relying party reset this session to
	// opened. The wallet never resets one, so it stays 0; it is still in the
	// app view (resetCount, changeKey), which the Idem app watches.
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

	// Access is which device currently controls each of the session's
	// client slots (web, native), plus the pending handover grant - see
	// Access.
	Access Access `json:"access,omitempty"`

	// Steps is the browser hosted flow's (requirements.md §1) accumulated
	// per-step evidence, populated incrementally by
	// POST /api/v1/app/{token}/steps/{document,selfie,nfc} as the user
	// walks through the flow — unlike the older single-shot
	// POST .../result, a session's steps are collected across several
	// requests, so a page reload (or a QR handover from desktop to phone)
	// can resume exactly where the user left off instead of starting over.
	// A nil field means that step hasn't been submitted yet.
	Steps StepEvidence `json:"steps,omitempty"`
}

// ForStorage is sess as it is persisted: a session whose face step can no
// longer run (decided, under review, expired or cancelled) keeps no
// relying-party reference photo.
func (sess Session) ForStorage() Session {
	if sess.Status.Terminal() || sess.Status == StatusNeedsReview {
		sess.ReferencePhoto, sess.ReferencePhotoMime = "", ""
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
	// LastFrame is the latest matching frame, released as the selfie when
	// requested.
	LastFrame   string `json:"lastFrame,omitempty"`
	FaceStarted bool   `json:"faceStarted,omitempty"`
}

// StepEvidence is what's been submitted so far for each of a flow's steps
// (see flow.Step) — nil fields are steps not yet completed. See
// Session.Steps.
type StepEvidence struct {
	Document      *DocumentStepEvidence      `json:"document,omitempty"`
	Selfie        *SelfieStepEvidence        `json:"selfie,omitempty"`
	NFC           *NFCStepEvidence           `json:"nfc,omitempty"`
	DocumentPhoto *DocumentPhotoStepEvidence `json:"documentPhoto,omitempty"`
	// Started lists the flow steps the user has begun but not necessarily
	// finished (e.g. the browser camera sending its first selfie preview
	// frame), so each start is audited once - see api.markStepStarted.
	Started []string `json:"started,omitempty"`
}

// StepTiming stamps when a step started (first touched) and finished
// (evidence accepted) — both server clock times, never trusted from the
// client, so the requirements.md §1 "timing instrumentation per step"
// target (~2 minutes end to end) is measured against something the caller
// can't fake by lying about its own clock.
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

// DocumentPhotoSide is one side's photo. Image is plain base64. BSNRegion,
// when the app located the printed BSN, is where a BlurBSN policy blurs it
// (see api.redactStepsForStorage).
type DocumentPhotoSide struct {
	Image     string       `json:"image"`
	MimeType  string       `json:"mimeType"`
	BSNRegion *ImageRegion `json:"bsnRegion,omitempty"`
}

// ImageRegion is a normalized ([0,1], top-left origin) box within an image.
type ImageRegion struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// DocumentStepEvidence is the document_capture step's accumulated evidence
// — always sourced from the native nfc_read hand-off: vcmrtd reads the
// document's own MRZ with its own camera (outside this repo) and submits
// the resulting identity alongside the chip evidence (see
// api.documentEvidenceFromNativeDocument). There is no browser-side
// capture and no server-side OCR/MRZ parsing anywhere in this codebase —
// document_capture is fulfilled exclusively through the app.
type DocumentStepEvidence struct {
	// Parsed is the document identity vcmrtd submitted alongside the chip
	// evidence — see ParsedMRZ's doc comment.
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

// ChipAccessKey is what vcmrtd derives from the MRZ to open the chip
// (BAC/PACE, or BAP for a driving licence), in vcmrtd's own vocabulary.
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

// ParsedMRZ is a document's ICAO 9303 MRZ-derived identity fields,
// flattened for storage — sourced entirely from the native app's own
// document identity submission (see api.documentEvidenceFromNativeDocument),
// never parsed from an image server side (this package deliberately has no
// MRZ/OCR dependency; there isn't one in this codebase).
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

// SelfieStepEvidence is the selfie/liveness/face_match steps' accumulated
// evidence — all three are one live capture, so they're stored together.
type SelfieStepEvidence struct {
	Image    string `json:"image,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	// LivenessPassed reports this server's own liveness/anti-spoof check
	// (api.checkLiveness — a real MiniFASNet-based model when one is
	// loaded, falling back to a frame-integrity heuristic otherwise), never
	// an app self-report.
	LivenessPassed bool `json:"livenessPassed"`
	// LivenessScore is the anti-spoof model's own live-class confidence
	// (0..1, rounded to 3 decimals) when the real model computed
	// LivenessPassed — nil when the server fell back to the frame-
	// distinctness heuristic instead (no model provisioned), since that
	// heuristic has no comparable score, only a pass/fail count.
	LivenessScore *float64 `json:"livenessScore,omitempty"`
	// FaceMatchScore/FaceVerified are this server's own 1:1 embedding
	// comparison (see api.computeFaceMatch, the same engine bound_login.go
	// already uses for its live face check) against whichever reference
	// photo the flow resolved — the chip's DG2 photo, or the relying
	// party's own referencePhoto — never a client-asserted claim. Both nil
	// when the resolved flow has no face_match step.
	FaceMatchScore *float64 `json:"faceMatchScore,omitempty"`
	FaceVerified   *bool    `json:"faceVerified,omitempty"`
	// Provider is who computed the verdicts: "regula" (the Regula Face API,
	// no Image kept) or "" for this server's own engine.
	Provider string     `json:"provider,omitempty"`
	Timing   StepTiming `json:"timing"`
}

// NFCStepEvidence is the nfc_read step's accumulated evidence — the same
// raw chip bytes api.appResultRequest.MrtdEvidence always carried, now
// submitted through its own step endpoint (POST .../steps/nfc) instead of
// the old single-shot POST .../result, so a browser flow can offer NFC as
// an in-flow optional upgrade (requirements.md §1) rather than the whole
// app being vcmrtd.
type NFCStepEvidence struct {
	// Raw carries the exact wire shape api.mrtdEvidenceRequest defines,
	// re-marshalled as JSON — session does not import the api package
	// (that would be a dependency cycle), so this is deliberately untyped
	// here and decoded again by api.handleSubmitNFCStep.
	Raw    map[string]any `json:"raw"`
	Timing StepTiming     `json:"timing"`
}

// CompletedSteps reports which of flow.Step's vocabulary this session has
// accepted evidence for — see appSessionView.CompletedSteps, which is what
// lets the browser flow resume to the right screen after a reload instead
// of restarting the whole session. Deliberately returns plain strings, not
// flow.Step, so this low-level package doesn't need to import
// backend/internal/flow just to name its own steps.
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
	return out
}

// IsExpired reports whether the session has passed its expiry time and has
// not already reached a terminal status. A session in needs_review never
// expires: it waits for a reviewer's decision (POST .../decision), however
// long that takes.
func (s Session) IsExpired(now time.Time) bool {
	return !s.Status.Terminal() && s.Status != StatusNeedsReview && !s.ExpiresAt.IsZero() && now.After(s.ExpiresAt)
}

// SetStatus validates and applies a status transition, stamping OpenedAt /
// SubmittedAt / CompletedAt / UpdatedAt as appropriate. It is the only way callers should
// change Status, so the status machine cannot be bypassed by a direct field
// assignment.
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

// newToken generates the opaque, high-entropy secret used to authorise
// polling and result callbacks. It is deliberately longer and separately
// generated from newID: the id is fine to put in a URL path and log lines,
// the token must not be guessable from it.
func newToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// newAAChallenge generates the 8-byte Active Authentication challenge
// (ISO 7816 INTERNAL AUTHENTICATE / TR-03110 RND.IFD) bound to a session —
// see Session.AAChallenge.
func newAAChallenge() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// newTenantReference generates Session.TenantReference — a display/lookup
// correlation id like newID, not a credential, so it uses the same entropy
// (12 random bytes) rather than newToken's higher-entropy secret.
func newTenantReference() string {
	return "tref_" + newID()
}
