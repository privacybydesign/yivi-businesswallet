package proofingengine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/i18n"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// The relying-party side, called in-process by internal/proofing: flows and
// sessions. Errors are pp.ErrNotFound, *pp.RejectedError (status and message)
// and pp.ErrMethodUnavailable.

// rejected is a refused request.
func rejected(status int, msg string) error {
	return &pp.RejectedError{Status: status, Message: msg}
}

// ---- flows ------------------------------------------------------------------

func toDefinition(tenantID, flowID string, in pp.FlowSpec) flow.FlowDefinition {
	fd := flow.FlowDefinition{
		ID: flowID, TenantID: tenantID, Name: in.Name, Steps: toSteps(in.Steps),
		SelfieLocation: flow.StepLocation(in.SelfieLocation), FaceProvider: flow.FaceProvider(in.FaceProvider),
		AcceptedDocumentTypes: in.AcceptedDocumentTypes, AcceptedIssuingCountries: in.AcceptedIssuingCountries,
		RequiredAssuranceLevel: flow.AssuranceLevel(in.RequiredAssuranceLevel),
		BSNPolicy:              privacyBSN(in.BSNPolicy), BlurFace: in.BlurFace, BlurBSN: in.BlurBSN,
		RetentionOverride: time.Duration(in.RetentionOverrideSeconds) * time.Second,
		LegalBasis:        privacyBasis(in.LegalBasis), ProcessingPurpose: in.ProcessingPurpose,
	}
	for _, c := range in.RequiredChecks {
		fd.RequiredChecks = append(fd.RequiredChecks, flow.Check(c))
	}
	if len(in.CheckThresholds) > 0 {
		fd.CheckThresholds = map[flow.Check]float64{}
		for c, v := range in.CheckThresholds {
			fd.CheckThresholds[flow.Check(c)] = v
		}
	}
	for _, t := range in.AssuranceTiers {
		fd.AssuranceTiers = append(fd.AssuranceTiers, flow.AssuranceTier{Level: t.Level, MinPercent: t.MinPercent})
	}
	fd.RequestedAttributes = in.RequestedAttributes
	return fd
}

func toSteps(in []string) []flow.Step {
	out := make([]flow.Step, 0, len(in))
	for _, s := range in {
		out = append(out, flow.Step(s))
	}
	return out
}

func toFlow(fd flow.FlowDefinition) pp.Flow {
	spec := pp.FlowSpec{
		Name: fd.Name, RequestedAttributes: requestedAttributesOf(fd),
		SelfieLocation: string(fd.EffectiveSelfieLocation()), FaceProvider: string(fd.FaceProvider),
		AcceptedDocumentTypes: fd.AcceptedDocumentTypes, AcceptedIssuingCountries: fd.AcceptedIssuingCountries,
		RequiredAssuranceLevel: string(fd.RequiredAssuranceLevel), BSNPolicy: string(fd.BSNPolicy),
		BlurFace: fd.BlurFace, BlurBSN: fd.BlurBSN, RetentionOverrideSeconds: int(fd.RetentionOverride / time.Second),
		LegalBasis: string(fd.LegalBasis), ProcessingPurpose: fd.ProcessingPurpose,
	}
	for _, st := range fd.Steps {
		spec.Steps = append(spec.Steps, string(st))
	}
	for _, c := range fd.RequiredChecks {
		spec.RequiredChecks = append(spec.RequiredChecks, string(c))
	}
	if len(fd.CheckThresholds) > 0 {
		spec.CheckThresholds = map[string]float64{}
		for c, v := range fd.CheckThresholds {
			spec.CheckThresholds[string(c)] = v
		}
	}
	// Only the flow's own tiers: a flow without keeps DefaultAssuranceTiers.
	for _, t := range fd.AssuranceTiers {
		spec.AssuranceTiers = append(spec.AssuranceTiers, pp.AssuranceTier{Level: t.Level, MinPercent: t.MinPercent})
	}
	return pp.Flow{FlowSpec: spec, ID: fd.ID, Version: fd.Version, Active: fd.Active, CreatedAt: fd.CreatedAt}
}

func toFlows(fds []flow.FlowDefinition) []pp.Flow {
	out := make([]pp.Flow, 0, len(fds))
	for _, fd := range fds {
		out = append(out, toFlow(fd))
	}
	return out
}

// flowError maps a flow store error: unknown is pp.ErrNotFound, a
// validation failure the admin's own message.
func flowError(op string, err error) error {
	switch {
	case errors.Is(err, flow.ErrNotFound):
		return fmt.Errorf("proofingengine: %s: %w", op, pp.ErrNotFound)
	case isValidation(err):
		return rejected(http.StatusBadRequest, err.Error())
	default:
		return fmt.Errorf("proofingengine: %s: %w", op, err)
	}
}

// isValidation is a flow.Validate refusal, as opposed to a storage failure.
func isValidation(err error) bool {
	var invalid *flow.ValidationError
	return errors.As(err, &invalid)
}

// maxRetentionOverrideSeconds is flow.MaxRetentionOverride in the spec's
// seconds.
const maxRetentionOverrideSeconds = int(flow.MaxRetentionOverride / time.Second)

// checkFlowSpec refuses what toDefinition cannot carry over faithfully, and a
// face provider this server lacks. A retention past the bound is refused on
// the seconds: converted first, a huge value would wrap to a Duration that
// flow.Validate accepts.
func (s *Server) checkFlowSpec(in pp.FlowSpec) error {
	if in.RetentionOverrideSeconds < 0 || in.RetentionOverrideSeconds > maxRetentionOverrideSeconds {
		return rejected(http.StatusBadRequest, fmt.Sprintf("flow: retentionOverride must be between 0 and %d days", flow.MaxRetentionOverrideDays))
	}
	return s.checkFaceProvider(in)
}

func (s *Server) checkFaceProvider(in pp.FlowSpec) error {
	switch flow.FaceProvider(in.FaceProvider) {
	case flow.FaceProviderRegula:
		if s.cfg.Regula == nil {
			return rejected(http.StatusBadRequest, "flow: faceProvider regula is not configured on this server")
		}
	case flow.FaceProviderEngine:
		return rejected(http.StatusBadRequest, "flow: faceProvider engine is not available: faces are verified with Regula")
	}
	return nil
}

// ListFlows returns the active version of each of the org's flows, newest first.
func (s *Server) ListFlows(ctx context.Context, t pp.Tenant) ([]pp.Flow, error) {
	fds, err := s.flows.List(ctx, t.ID)
	if err != nil {
		return nil, flowError("list flows", err)
	}
	return toFlows(fds), nil
}

// CreateFlow creates a flow at version 1, active.
func (s *Server) CreateFlow(ctx context.Context, t pp.Tenant, in pp.FlowSpec) (pp.Flow, error) {
	if err := s.checkFlowSpec(in); err != nil {
		return pp.Flow{}, err
	}
	fd, err := s.flows.Save(ctx, toDefinition(t.ID, "", in))
	if err != nil {
		return pp.Flow{}, flowError("create flow", err)
	}
	return toFlow(fd), nil
}

// CreateFlowVersion saves the next version of flow id, active at once.
func (s *Server) CreateFlowVersion(ctx context.Context, t pp.Tenant, id string, in pp.FlowSpec) (pp.Flow, error) {
	if err := s.checkFlowSpec(in); err != nil {
		return pp.Flow{}, err
	}
	fd, err := s.flows.Save(ctx, toDefinition(t.ID, id, in))
	if err != nil {
		return pp.Flow{}, flowError("create flow version", err)
	}
	return toFlow(fd), nil
}

// ListFlowVersions returns every version of flow id, oldest first.
func (s *Server) ListFlowVersions(ctx context.Context, t pp.Tenant, id string) ([]pp.Flow, error) {
	fds, err := s.flows.ListVersions(ctx, t.ID, id)
	if err != nil {
		return nil, flowError("list flow versions", err)
	}
	if len(fds) == 0 {
		return nil, fmt.Errorf("proofingengine: list flow versions: %w", pp.ErrNotFound)
	}
	return toFlows(fds), nil
}

// ActivateFlowVersion makes version the active one of flow id.
func (s *Server) ActivateFlowVersion(ctx context.Context, t pp.Tenant, id string, version int) (pp.Flow, error) {
	if err := s.flows.Activate(ctx, t.ID, id, version); err != nil {
		return pp.Flow{}, flowError("activate flow version", err)
	}
	fd, err := s.flows.Get(ctx, t.ID, id, version)
	if err != nil {
		return pp.Flow{}, flowError("activate flow version", err)
	}
	return toFlow(fd), nil
}

// ---- sessions ---------------------------------------------------------------

// Bounds on what a session is created with.
const (
	maxClientReferenceLength = 200
	maxFlowIDLength          = 100
	maxLanguageLength        = 35
)

// CreateSession starts a session on flow in.FlowID.
func (s *Server) CreateSession(ctx context.Context, t pp.Tenant, in pp.SessionInput) (pp.Session, error) {
	switch {
	case len(in.ClientReference) > maxClientReferenceLength:
		return pp.Session{}, rejected(http.StatusBadRequest, "clientReference is too long (max 200 characters)")
	case len(in.FlowID) > maxFlowIDLength:
		return pp.Session{}, rejected(http.StatusBadRequest, "flow is too long (max 100 characters)")
	case len(in.Language) > maxLanguageLength, in.Language != "" && !i18n.Valid(in.Language):
		return pp.Session{}, rejected(http.StatusBadRequest, "language must be a BCP 47 language tag")
	}
	method, ttl := session.MethodNFCPassport, s.cfg.SessionCreateTTL
	switch in.Method {
	case "", pp.MethodIdem:
	case pp.MethodYivi:
		if !s.YiviAvailable() {
			return pp.Session{}, fmt.Errorf("proofingengine: create session: %w", pp.ErrMethodUnavailable)
		}
		method, ttl = session.MethodBiometricBoundLogin, s.cfg.BoundLoginTTL
	default:
		return pp.Session{}, fmt.Errorf("proofingengine: no session can be started for method %q", in.Method)
	}
	var resolvedFlow *flow.FlowDefinition
	if in.FlowID != "" {
		fd, err := s.flows.Get(ctx, t.ID, in.FlowID, 0)
		if err != nil && !errors.Is(err, flow.ErrNotFound) {
			return pp.Session{}, fmt.Errorf("proofingengine: resolve flow: %w", err)
		}
		if err == nil {
			resolvedFlow = &fd
		}
	}
	var requestedAttributes []string
	var flowVersion int
	var retention time.Duration
	if resolvedFlow != nil {
		requestedAttributes, flowVersion, retention = requestedAttributesOf(*resolvedFlow), resolvedFlow.Version, resolvedFlow.RetentionOverride
	}
	// A face match without nfc_read has no chip photo: the customer sends a
	// reference photo with each session, and only such a flow takes one.
	needsReference := resolvedFlow != nil && flowNeedsFaceMatch(resolvedFlow) && !slices.Contains(resolvedFlow.Steps, flow.StepNFCRead)
	hasReference := in.ReferencePhoto != nil && in.ReferencePhoto.Base64 != ""
	switch {
	case needsReference && !hasReference:
		return pp.Session{}, rejected(http.StatusBadRequest, "referencePhoto is required: the flow matches the face without nfc_read, so there is no chip photo to compare the live face against")
	case !needsReference && hasReference:
		return pp.Session{}, rejected(http.StatusBadRequest, "referencePhoto is only taken by a flow that matches the face without nfc_read")
	case hasReference && !slices.Contains(displayableImageTypes, in.ReferencePhoto.MimeType):
		return pp.Session{}, rejected(http.StatusBadRequest, "referencePhoto must be a PNG, JPEG or WebP image")
	}
	if in.Retention > 0 {
		retention = in.Retention
	}
	if in.TTL > 0 {
		ttl = min(max(in.TTL, s.cfg.SessionMinTTL), s.cfg.SessionMaxTTL)
	}
	if s.cfg.SessionMaxLifetime > 0 {
		ttl = min(ttl, s.cfg.SessionMaxLifetime)
	}
	sess, err := s.sessions.Create(session.Session{
		TenantID: t.ID, Method: method, ClientReference: in.ClientReference,
		Flow: in.FlowID, FlowVersion: flowVersion, Language: in.Language, RequestedAttributes: requestedAttributes,
		ExpiresAt: time.Now().UTC().Add(ttl), RetentionOverride: retention,
		ReferencePhoto: referenceBase64(in.ReferencePhoto), ReferencePhotoMime: referenceMime(in.ReferencePhoto),
	})
	if err != nil {
		return pp.Session{}, fmt.Errorf("proofingengine: create session: %w", err)
	}
	s.auditProofing(sess, eventSessionCreated, nil)
	out := pp.Session{ID: sess.ID, Token: sess.Token, ExpiresAt: sess.ExpiresAt, FlowVersion: sess.FlowVersion}
	if sess.Method == session.MethodNFCPassport && sessionOpenForDevices(sess) == nil {
		updated, claims, err := s.mintClaimTokens(sess, "create", session.DeviceRoleNative)
		if err != nil {
			return pp.Session{}, fmt.Errorf("proofingengine: mint claim: %w", err)
		}
		out.ExpiresAt = updated.ExpiresAt
		if c := claims.Native; c != nil {
			out.Claim = &pp.Claim{DeepLink: c.DeepLink, ExpiresAt: c.ExpiresAt}
		}
	}
	return out, nil
}

// referenceBase64 and referenceMime are a relying party's reference photo
// as a session holds it; "" for none.
func referenceBase64(img *pp.Image) string {
	if img == nil {
		return ""
	}
	return img.Base64
}

func referenceMime(img *pp.Image) string {
	if img == nil {
		return ""
	}
	return img.MimeType
}

// session resolves an org's session by id and token.
func (s *Server) session(t pp.Tenant, id, token string) (session.Session, error) {
	sess, err := s.sessions.AuthenticateSession(t.ID, id, token)
	if errors.Is(err, session.ErrNotFound) {
		return session.Session{}, fmt.Errorf("proofingengine: session: %w", pp.ErrNotFound)
	}
	if err != nil {
		return session.Session{}, fmt.Errorf("proofingengine: session: %w", err)
	}
	return sess, nil
}

// statusView is a session's status without personal data.
type statusView struct {
	Status      pp.Status  `json:"status"`
	ErrorCode   string     `json:"errorCode"`
	CompletedAt *time.Time `json:"completedAt"`
	Assurance   *struct {
		Level      string `json:"level"`
		EIDASLevel string `json:"eidasLevel"`
	} `json:"assurance"`
	Disclosure bool `json:"disclosure"`
	Devices    []struct {
		Role    string `json:"role"`
		Current bool   `json:"current"`
		Away    bool   `json:"away"`
	} `json:"devices"`
}

// roundTrip re-reads v through JSON into out: a stored result holds typed
// structs when fresh and maps once loaded, and this reads both the same way.
func roundTrip(v, out any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("proofingengine: encode view: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("proofingengine: decode view: %w", err)
	}
	return nil
}

// SessionStatus reads a session's status and assurance summary, without
// the document or images: the read to reconcile on.
func (s *Server) SessionStatus(ctx context.Context, t pp.Tenant, id, token string) (pp.Result, error) {
	sess, err := s.session(t, id, token)
	if err != nil {
		return pp.Result{}, err
	}
	_, disclosure := sess.Result["disclosure"]
	var out statusView
	if err := roundTrip(sessionStatusView{
		ID: sess.ID, Status: sess.Status, ErrorCode: sess.ErrorCode, CompletedAt: sess.CompletedAt,
		FlowVersion: sess.FlowVersion, Assurance: sess.Result["assurance"], Disclosure: disclosure,
		Devices: s.statusDevices(sess.Access, time.Now()),
	}, &out); err != nil {
		return pp.Result{}, err
	}
	res := pp.Result{Status: out.Status, ErrorCode: out.ErrorCode, CompletedAt: out.CompletedAt}
	var roles []string
	for _, d := range out.Devices {
		roles = append(roles, d.Role)
		if d.Current && d.Role == string(session.DeviceRoleNative) {
			res.App = pp.AppConnected
			if d.Away {
				res.App = pp.AppAway
			}
		}
	}
	if res.App == "" {
		res.App = pp.AppWaiting
	}
	res.Method = methodOf(roles)
	if out.Disclosure {
		res.Method = pp.MethodYivi
	}
	if out.Assurance != nil {
		res.AssuranceLevel, res.EIDASLevel = out.Assurance.Level, out.Assurance.EIDASLevel
	}
	return res, nil
}

// methodOf is how the subject took part on a device: the slots of the
// devices that claimed the session, native first. A Yivi disclosure in the
// result is the Yivi method instead, which the caller sets.
func methodOf(roles []string) pp.Method {
	switch {
	case slices.Contains(roles, string(session.DeviceRoleNative)):
		return pp.MethodIdem
	case slices.Contains(roles, string(session.DeviceRoleWeb)):
		return pp.MethodBrowser
	default:
		return ""
	}
}

// resultView is the part of a session's result the wallet reads.
type resultView struct {
	Status      pp.Status  `json:"status"`
	ErrorCode   string     `json:"errorCode"`
	CompletedAt *time.Time `json:"completedAt"`
	Result      *struct {
		Assurance *struct {
			Level      string `json:"level"`
			EIDASLevel string `json:"eidasLevel"`
		} `json:"assurance"`
		Document *struct {
			Type         string `json:"type"`
			IssuingState string `json:"issuingState"`
			Nationality  string `json:"nationality"`
			DisplayName  string `json:"displayName"`
			FirstName    string `json:"firstName"`
			LastName     string `json:"lastName"`
			DateOfBirth  string `json:"dateOfBirth"`
			DateOfExpiry string `json:"dateOfExpiry"`
		} `json:"document"`
		ChipChecks *struct {
			Passive *struct {
				SODSignatureValid    *bool `json:"sodSignatureValid"`
				DataGroupHashesValid *bool `json:"dataGroupHashesValid"`
				CSCATrustChainValid  *bool `json:"cscaTrustChainValid"`
			} `json:"passiveAuthentication"`
			Active *struct {
				Attempted *bool `json:"attempted"`
				Passed    *bool `json:"passed"`
			} `json:"activeAuthentication"`
		} `json:"chipChecks"`
		Biometrics *struct {
			FaceMatchScore *float64 `json:"faceMatchScore"`
			LivenessResult string   `json:"livenessResult"`
		} `json:"biometrics"`
		Disclosure *struct {
			Source string `json:"source"`
		} `json:"disclosure"`
		Photo             *imageView `json:"photo"`
		Selfie            *imageView `json:"selfie"`
		ReferencePhoto    *imageView `json:"referencePhoto"`
		FaceReference     string     `json:"faceReference"`
		DocumentImage     *imageView `json:"documentImage"`
		DocumentImageBack *imageView `json:"documentImageBack"`
	} `json:"result"`
	Devices []struct {
		Role string `json:"role"`
	} `json:"devices"`
}

func (s *Server) readResult(t pp.Tenant, id, token string) (resultView, error) {
	sess, err := s.session(t, id, token)
	if err != nil {
		return resultView{}, err
	}
	var out resultView
	err = roundTrip(sessionResultView{
		ID: sess.ID, Status: sess.Status, ErrorCode: sess.ErrorCode,
		Result: sess.Result, CompletedAt: sess.CompletedAt, Devices: deviceParticipation(sess.Access),
	}, &out)
	return out, err
}

func (v resultView) base() pp.Result {
	res := pp.Result{Status: v.Status, ErrorCode: v.ErrorCode, CompletedAt: v.CompletedAt}
	roles := make([]string, 0, len(v.Devices))
	for _, d := range v.Devices {
		roles = append(roles, d.Role)
	}
	res.Method = methodOf(roles)
	if v.Result != nil && v.Result.Disclosure != nil {
		res.Method = pp.MethodYivi
	}
	if v.Result != nil && v.Result.Assurance != nil {
		res.AssuranceLevel, res.EIDASLevel = v.Result.Assurance.Level, v.Result.Assurance.EIDASLevel
	}
	return res
}

// SessionResult reads a session's outcome and the holder's name.
func (s *Server) SessionResult(_ context.Context, t pp.Tenant, id, token string) (pp.Result, error) {
	v, err := s.readResult(t, id, token)
	if err != nil {
		return pp.Result{}, err
	}
	res := v.base()
	if v.Result != nil && v.Result.Document != nil {
		doc := v.Result.Document
		res.Name = strings.TrimSpace(doc.DisplayName)
		if res.Name == "" {
			res.Name = strings.TrimSpace(strings.TrimSpace(doc.FirstName) + " " + strings.TrimSpace(doc.LastName))
		}
	}
	return res, nil
}

// SessionIdentity reads who was proofed and on what evidence: never the
// document number, personal number or place of birth.
func (s *Server) SessionIdentity(_ context.Context, t pp.Tenant, id, token string) (pp.Identity, error) {
	v, err := s.readResult(t, id, token)
	if err != nil {
		return pp.Identity{}, err
	}
	out := pp.Identity{Result: v.base()}
	res := v.Result
	if res == nil {
		return out, nil
	}
	ev := &pp.Evidence{Type: pp.EvidenceEMRTD, PassiveAuth: pp.CheckNotPerformed, ActiveAuth: pp.CheckNotPerformed}
	if res.Disclosure != nil {
		ev.Type = pp.EvidenceYivi
	}
	if res.FaceReference == faceReferenceRelyingParty {
		ev.Type = pp.EvidenceReferencePhoto
	}
	if ev.Type == pp.EvidenceEMRTD && res.ChipChecks == nil {
		ev.Type = pp.EvidenceDocumentPhoto
	}
	if doc := res.Document; doc != nil {
		out.GivenName, out.FamilyName = strings.TrimSpace(doc.FirstName), strings.TrimSpace(doc.LastName)
		out.BirthDate, out.Nationality = doc.DateOfBirth, doc.Nationality
		ev.DocumentType, ev.IssuingState, ev.ExpiryDate = doc.Type, doc.IssuingState, doc.DateOfExpiry
	}
	if chip := res.ChipChecks; chip != nil {
		if p := chip.Passive; p != nil {
			ev.PassiveAuth = checkOf(allTrue(p.SODSignatureValid, p.DataGroupHashesValid, p.CSCATrustChainValid))
		}
		if a := chip.Active; a != nil && a.Attempted != nil && *a.Attempted {
			ev.ActiveAuth = checkOf(a.Passed)
		}
	}
	if b := res.Biometrics; b != nil {
		ev.FaceMatch, ev.Liveness = b.FaceMatchScore, b.LivenessResult
	}
	out.Evidence = ev
	out.Photo, out.Selfie, out.ReferencePhoto = res.Photo.image(), res.Selfie.image(), res.ReferencePhoto.image()
	out.DocumentImage, out.DocumentImageBack = res.DocumentImage.image(), res.DocumentImageBack.image()
	return out, nil
}

// imageView is an image in a result.
type imageView struct {
	ImageBase64 string `json:"imageBase64"`
	MimeType    string `json:"mimeType"`
}

// displayableImageTypes are the formats a browser shows; anything else (an
// unconverted JPEG2000 portrait, or a type a browser would run) is left out.
var displayableImageTypes = []string{"image/png", "image/jpeg", "image/webp"}

func (i *imageView) image() *pp.Image {
	if i == nil || i.ImageBase64 == "" || !slices.Contains(displayableImageTypes, i.MimeType) {
		return nil
	}
	if _, err := base64.StdEncoding.DecodeString(i.ImageBase64); err != nil {
		return nil
	}
	return &pp.Image{MimeType: i.MimeType, Base64: i.ImageBase64}
}

// allTrue is nil when no check ran, else whether each that ran passed.
func allTrue(checks ...*bool) *bool {
	var ran, ok bool
	for _, c := range checks {
		if c == nil {
			continue
		}
		if !ran {
			ran, ok = true, true
		}
		ok = ok && *c
	}
	if !ran {
		return nil
	}
	return &ok
}

func checkOf(passed *bool) string {
	switch {
	case passed == nil:
		return pp.CheckNotPerformed
	case *passed:
		return pp.CheckValid
	default:
		return pp.CheckInvalid
	}
}

// ---- review, handover, cancel, delete ---------------------------------------

// errCodeReviewRejected is a rejection a reviewer decided without naming a code.
const errCodeReviewRejected = "MANUAL_REVIEW_REJECTED"

// Bounds on a review decision's reason and reviewer.
const (
	maxReviewReasonLength   = 500
	maxReviewReviewerLength = 200
)

var reviewErrorCodePattern = regexp.MustCompile(`^[A-Z0-9_]{1,64}$`)

var errNotUnderReview = errors.New("the session is not under review")

// DecideReview settles a session in needs_review as the reviewer decided.
func (s *Server) DecideReview(_ context.Context, t pp.Tenant, id, token string, d pp.ReviewDecision) error {
	reason, reviewer := strings.TrimSpace(d.Reason), strings.TrimSpace(d.Reviewer)
	switch {
	case d.Approve && d.ErrorCode != "":
		return rejected(http.StatusBadRequest, "errorCode is only for a rejection")
	case d.ErrorCode != "" && !reviewErrorCodePattern.MatchString(d.ErrorCode):
		return rejected(http.StatusBadRequest, "errorCode must be 1-64 of A-Z, 0-9 and _")
	case reason == "" || utf8.RuneCountInString(reason) > maxReviewReasonLength:
		return rejected(http.StatusBadRequest, "reason is required (max 500 characters)")
	case reviewer == "" || len(reviewer) > maxReviewReviewerLength:
		return rejected(http.StatusBadRequest, "reviewer is required (max 200 characters)")
	}
	to, errorCode := session.StatusApproved, ""
	if !d.Approve {
		to, errorCode = session.StatusRejected, d.ErrorCode
		if errorCode == "" {
			errorCode = errCodeReviewRejected
		}
	}
	sess, err := s.session(t, id, token)
	if err != nil {
		return err
	}
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if sess.Status != session.StatusNeedsReview {
			return errNotUnderReview
		}
		if err := sess.SetStatus(to, time.Now().UTC()); err != nil {
			return err
		}
		sess.ErrorCode = errorCode
		return nil
	})
	switch {
	case errors.Is(err, session.ErrNotFound):
		return fmt.Errorf("proofingengine: decide review: %w", pp.ErrNotFound)
	case errors.Is(err, errNotUnderReview):
		return rejected(http.StatusConflict, err.Error())
	case err != nil:
		return fmt.Errorf("proofingengine: decide review: %w", err)
	}
	details := map[string]any{"stage": "manual_review"}
	if updated.ErrorCode != "" {
		details["errorCode"] = updated.ErrorCode
	}
	s.auditProofing(updated, eventTypeForStatus(updated.Status), details)
	return nil
}

// errDeviceActive refuses a handover of a slot whose device is still there.
var errDeviceActive = errors.New("the device holding this slot is still active; only it can hand the session over")

// SessionHandover gives a fresh Idem app link for the native slot: its claim
// while empty, a handover once its app left (inactive or stale). An app
// still active is refused with pp.CodeDeviceActive.
func (s *Server) SessionHandover(_ context.Context, t pp.Tenant, id, token string) (pp.Claim, error) {
	sess, err := s.session(t, id, token)
	if err != nil {
		return pp.Claim{}, err
	}
	if sess.Method != session.MethodNFCPassport {
		return pp.Claim{}, rejected(http.StatusConflict, "only Idem-app sessions have device slots")
	}
	role := session.DeviceRoleNative
	var grant string
	slotClaimed := false
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if err := sessionOpenForDevices(*sess); err != nil {
			return err
		}
		ttl := s.cfg.ClaimTokenTTL
		if d := sess.Access.Slot(role); d != nil {
			if !s.deviceAway(d, time.Now()) {
				return errDeviceActive
			}
			ttl, slotClaimed = s.cfg.HandoverTokenTTL, true
		}
		var err error
		grant, err = sess.Access.MintHandover(role, s.grantExpiry(*sess, ttl))
		return err
	})
	switch {
	case errors.Is(err, errDeviceActive):
		return pp.Claim{}, &pp.RejectedError{Status: http.StatusConflict, Message: err.Error(), Code: pp.CodeDeviceActive}
	case errors.Is(err, errSessionExpired):
		return pp.Claim{}, &pp.RejectedError{Status: http.StatusGone, Message: err.Error(), Code: errCodeSessionExpired}
	case errors.Is(err, errSessionComplete):
		return pp.Claim{}, &pp.RejectedError{Status: http.StatusConflict, Message: err.Error(), Code: errCodeSessionComplete}
	case err != nil:
		return pp.Claim{}, fmt.Errorf("proofingengine: session handover: %w", err)
	}
	expiresAt := updated.Access.Grant(role).ExpiresAt
	event := eventClaimTokenIssued
	if slotClaimed {
		event = eventHandoverIssued
	}
	s.auditProofing(updated, event, map[string]any{"role": string(role), "via": "relying_party_handover"})
	resp := s.grantResponse(role, grant, expiresAt)
	return pp.Claim{DeepLink: resp.DeepLink, ExpiresAt: expiresAt, Handover: slotClaimed}, nil
}

// CancelSession ends a session with no outcome yet; one that has an outcome
// is refused (409), one already cancelled is left as it is.
func (s *Server) CancelSession(_ context.Context, t pp.Tenant, id, token string) error {
	sess, err := s.session(t, id, token)
	if err != nil {
		return err
	}
	var transitionErr error
	alreadyCancelled := false
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if sess.Status == session.StatusCancelled {
			alreadyCancelled = true
			return nil
		}
		transitionErr = sess.SetStatus(session.StatusCancelled, time.Now().UTC())
		return transitionErr
	})
	switch {
	case errors.Is(err, session.ErrNotFound):
		return fmt.Errorf("proofingengine: cancel session: %w", pp.ErrNotFound)
	case err != nil && errors.Is(err, transitionErr):
		return rejected(http.StatusConflict, err.Error())
	case err != nil:
		return fmt.Errorf("proofingengine: cancel session: %w", err)
	}
	if !alreadyCancelled {
		s.queueRegulaSweep(context.Background(), updated)
		s.auditProofing(updated, eventSessionCancelled, nil)
	}
	return nil
}

// DeleteSession erases a session and its evidence; one already gone is fine.
// The Regula transactions under its tag are swept now, not after the grace.
func (s *Server) DeleteSession(ctx context.Context, t pp.Tenant, id, token string) error {
	sess, err := s.session(t, id, token)
	if errors.Is(err, pp.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.sessions.Delete(sess.TenantID, sess.ID); err != nil && !errors.Is(err, session.ErrNotFound) {
		return fmt.Errorf("proofingengine: delete session: %w", err)
	}
	s.sweepRegulaNow(ctx, sess)
	s.auditProofing(sess, eventSessionPurged, map[string]any{"priorStatus": string(sess.Status)})
	return nil
}

// SubmitReference hands the engine the subject's verified OpenID4VP
// disclosure as a Yivi-method session's reference.
func (s *Server) SubmitReference(ctx context.Context, t pp.Tenant, id, token string, ref pp.Reference) (pp.YiviDisclosure, error) {
	if !s.YiviAvailable() {
		return pp.YiviDisclosure{}, fmt.Errorf("proofingengine: submit reference: %w", pp.ErrMethodUnavailable)
	}
	sess, err := s.session(t, id, token)
	if err != nil {
		return pp.YiviDisclosure{}, err
	}
	return s.acceptReference(ctx, sess, ref)
}

// SubmitFaceFrame scores one live camera frame of a Yivi-method session.
func (s *Server) SubmitFaceFrame(ctx context.Context, token, image string) (pp.FaceVerdict, error) {
	sess, err := s.sessions.Authenticate(token)
	if errors.Is(err, session.ErrNotFound) {
		return pp.FaceVerdict{}, fmt.Errorf("proofingengine: face frame: %w", pp.ErrNotFound)
	}
	if err != nil {
		return pp.FaceVerdict{}, fmt.Errorf("proofingengine: face frame: %w", err)
	}
	return s.faceFrame(ctx, sess, image)
}

func privacyBSN(p string) privacy.BSNPolicy { return privacy.BSNPolicy(p) }

func privacyBasis(b string) privacy.LegalBasis { return privacy.LegalBasis(b) }
