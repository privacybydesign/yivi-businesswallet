package proofingengine

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/images"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// The Yivi method (IPS's biometric-bound login): the wallet verifies the
// subject's OpenID4VP disclosure of a passport or ID-card credential itself
// and hands the engine its photo and claims as the reference
// (SubmitReference); the subject's browser camera then sends live frames
// (SubmitFaceFrame), each compared 1:1 with that photo, until a run of
// BoundLoginStableFrames matches approves the session or
// BoundLoginMaxAttempts usable frames reject it. The reference and the run
// live in the session (sealed, session.YiviState), so any API replica can
// score the next frame, and are cleared as the session settles.
//
// IPS scored frames with its own TFLite face engine; the wallet has none,
// so each frame is a Regula image-to-image match. Without Regula the method
// is unavailable.

// eventDisclosureReceived marks the engine accepting a disclosure as reference.
const eventDisclosureReceived = "proofing.disclosure.received"

// Error codes a Yivi-method session can end with.
const (
	errCodeFaceNoMatch        = "FACE_NO_MATCH"     // attempt budget spent without a stable match
	errCodeReferenceNoFace    = "REFERENCE_NO_FACE" // the disclosed photo is not usable
	errCodePhotoMissing       = "PHOTO_MISSING"     // the credential was disclosed without its photo
	yiviPhotoAttribute        = "photo"             // the passport/idcard credentials' photo claim
	boundLoginEngine          = "regula_image_match"
	boundLoginLiveness        = "not_performed" // no liveness check on the browser frames
	attrDisclosedAttributes   = "attributes"    // requestedAttributes key releasing the disclosed values
	boundLoginDuplicateFrames = "duplicate_frames"
	maxDisclosedPhotoBytes    = 4 << 20 // a passport photo is tens of kB
	referenceSourceOpenID4VP  = "openid4vp"
	maxReferenceAttributes    = 20
	maxReferenceAttrKeyLen    = 40
	maxReferenceAttrValueLen  = 200
	maxReferenceCredentialLen = 200
)

// Disclosure codes SubmitReference answers a refused reference with.
const (
	disclosureCodePhotoMissing    = "photo_missing"
	disclosureCodeReferenceNoFace = "reference_no_face"
)

// disclosureInfo is the result's "disclosure": where the photo and claims came from.
type disclosureInfo struct {
	Source         string            `json:"source"`
	Credential     string            `json:"credential,omitempty"`
	Issuer         string            `json:"issuer,omitempty"`
	PhotoAttribute string            `json:"photoAttribute,omitempty"`
	ProofStatus    string            `json:"proofStatus,omitempty"`
	Attributes     map[string]string `json:"attributes,omitempty"`
	AttributeIDs   []string          `json:"attributeIds,omitempty"`
}

// injectionInfo is the frame-integrity part of the Yivi method's biometrics.
type injectionInfo struct {
	DuplicateFrames int      `json:"duplicateFrames"`
	Checks          []string `json:"checks"`
}

// YiviAvailable reports whether the Yivi method can run: it needs Regula.
func (s *Server) YiviAvailable() bool { return s.cfg.Regula != nil }

// boundLoginSession checks that sess is a Yivi-method session that can still take part.
func boundLoginSession(sess session.Session) error {
	if sess.Method != session.MethodBiometricBoundLogin {
		return &proofingprovider.RejectedError{Status: http.StatusConflict, Message: "not a Yivi-method session"}
	}
	if sess.Status == session.StatusExpired {
		return &proofingprovider.RejectedError{Status: http.StatusGone, Message: "session expired"}
	}
	if sess.Status.Terminal() {
		return &proofingprovider.RejectedError{Status: http.StatusConflict, Message: "session already finished (" + string(sess.Status) + ")"}
	}
	return nil
}

func (s *Server) disclosureAccepted() proofingprovider.YiviDisclosure {
	return proofingprovider.YiviDisclosure{OK: true, StableFrames: s.cfg.BoundLoginStableFrames, MaxAttempts: s.cfg.BoundLoginMaxAttempts}
}

// acceptReference takes ref as sess's reference and moves it to in_progress.
// A repeated call once the reference is in gets the same answer.
func (s *Server) acceptReference(sess session.Session, ref proofingprovider.Reference) (proofingprovider.YiviDisclosure, error) {
	if err := boundLoginSession(sess); err != nil {
		return proofingprovider.YiviDisclosure{}, err
	}
	if sess.Yivi != nil {
		return s.disclosureAccepted(), nil
	}
	if sess.Status == session.StatusInProgress {
		return proofingprovider.YiviDisclosure{}, &proofingprovider.RejectedError{Status: http.StatusConflict, Message: "disclosure already received; continue with the face check"}
	}
	credential := strings.TrimSpace(ref.Credential)
	if credential == "" || len(credential) > maxReferenceCredentialLen {
		return proofingprovider.YiviDisclosure{}, &proofingprovider.RejectedError{Status: http.StatusBadRequest, Message: "credential is required"}
	}
	if len(ref.Attributes) > maxReferenceAttributes {
		return proofingprovider.YiviDisclosure{}, &proofingprovider.RejectedError{Status: http.StatusBadRequest, Message: "too many attributes"}
	}
	info := disclosureInfo{
		Source: referenceSourceOpenID4VP, Credential: credential,
		PhotoAttribute: credential + "." + yiviPhotoAttribute, Attributes: map[string]string{},
	}
	if parts := strings.Split(credential, "."); len(parts) == 3 {
		info.Issuer = parts[0] + "." + parts[1]
	}
	for k, v := range ref.Attributes {
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" || v == "" || k == yiviPhotoAttribute || len(k) > maxReferenceAttrKeyLen || len(v) > maxReferenceAttrValueLen {
			continue
		}
		info.Attributes[k] = v
		info.AttributeIDs = append(info.AttributeIDs, credential+"."+k)
	}
	refuse := func(errorCode, code, reason string) (proofingprovider.YiviDisclosure, error) {
		details := map[string]any{"stage": "disclosure", "credential": info.Credential, "reason": reason}
		s.boundLoginFinish(sess, session.StatusRejected, errorCode, nil, details)
		return proofingprovider.YiviDisclosure{OK: false, Code: code}, nil
	}
	if strings.TrimSpace(ref.Photo) == "" {
		return refuse(errCodePhotoMissing, disclosureCodePhotoMissing, "photo_missing")
	}
	raw, mime, err := decodeImageBase64(ref.Photo, "image/jpeg")
	if err != nil || len(raw) > maxDisclosedPhotoBytes {
		return refuse(errCodeReferenceNoFace, disclosureCodeReferenceNoFace, "photo_undecodable")
	}
	reference := base64.StdEncoding.EncodeToString(raw)
	// A chip-derived photo may be JPEG2000, which Regula and browsers need as PNG.
	if converted, convertedMime, err := images.ToDisplayablePNG(reference, mime); err == nil && converted != "" {
		reference, mime = converted, convertedMime
	}
	disclosure, err := json.Marshal(info)
	if err != nil {
		return proofingprovider.YiviDisclosure{}, fmt.Errorf("proofingengine: encode disclosure: %w", err)
	}
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if sess.Yivi != nil {
			return nil
		}
		if err := boundLoginSession(*sess); err != nil {
			return err
		}
		now := time.Now().UTC()
		if sess.Status == session.StatusCreated {
			if err := sess.SetStatus(session.StatusOpened, now); err != nil {
				return err
			}
			if ext := s.capExpiry(sess.CreatedAt, now.Add(s.cfg.SessionOpenTTL)); ext.After(sess.ExpiresAt) {
				sess.ExpiresAt = ext
			}
		}
		sess.Yivi = &session.YiviState{
			Reference: reference, ReferenceMime: mime, Disclosure: disclosure,
			KeepPhoto: attrRequested(*sess, attrDG2, attrFaceImage),
		}
		return sess.SetStatus(session.StatusInProgress, now)
	})
	if err != nil {
		var rejected *proofingprovider.RejectedError
		if errors.As(err, &rejected) {
			return proofingprovider.YiviDisclosure{}, err
		}
		return proofingprovider.YiviDisclosure{}, fmt.Errorf("proofingengine: accept reference: %w", err)
	}
	if sess.Status == session.StatusCreated {
		s.auditProofing(updated, eventSessionOpened, nil)
	}
	s.auditProofing(updated, eventDisclosureReceived, map[string]any{"credential": info.Credential})
	return s.disclosureAccepted(), nil
}

// faceFrame scores one live frame of sess against its reference. The Regula
// call runs outside the row lock; the counters are applied under it, so a
// frame scored by another replica in between is never lost.
func (s *Server) faceFrame(ctx context.Context, sess session.Session, frame string) (proofingprovider.FaceVerdict, error) {
	if err := boundLoginSession(sess); err != nil {
		return proofingprovider.FaceVerdict{}, err
	}
	if sess.Yivi == nil || sess.Status != session.StatusInProgress {
		return proofingprovider.FaceVerdict{}, &proofingprovider.RejectedError{Status: http.StatusConflict, Message: "disclosure not completed yet"}
	}
	if s.cfg.Regula == nil {
		return proofingprovider.FaceVerdict{}, proofingprovider.ErrMethodUnavailable
	}
	raw, _, err := decodeImageBase64(frame, "image/jpeg")
	if err != nil {
		return proofingprovider.FaceVerdict{}, &proofingprovider.RejectedError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	if !sess.Yivi.FaceStarted {
		s.auditProofing(sess, eventSessionInProgress, map[string]any{"stage": string(flow.StepFaceVerification)})
	}

	mctx, cancel := context.WithTimeout(ctx, regula.DefaultTimeout)
	defer cancel()
	match, err := s.cfg.Regula.MatchImages(mctx, sess.Yivi.Reference, base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		return proofingprovider.FaceVerdict{}, fmt.Errorf("proofingengine: face frame: %w", err)
	}
	score := round3(match.Similarity)
	verdict := proofingprovider.FaceVerdict{
		FaceDetected: match.LiveFaceDetected, StableFrames: s.cfg.BoundLoginStableFrames,
		MaxAttempts: s.cfg.BoundLoginMaxAttempts, Decision: proofingprovider.FaceDecisionPending,
	}
	var details map[string]any
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if err := boundLoginSession(*sess); err != nil {
			return err
		}
		st := sess.Yivi
		if st == nil || sess.Status != session.StatusInProgress {
			return &proofingprovider.RejectedError{Status: http.StatusConflict, Message: "disclosure not completed yet"}
		}
		st.FaceStarted = true
		matched := false
		switch {
		case !match.LiveFaceDetected:
			// A frame without a face costs nothing but breaks the run.
			st.Consecutive = 0
		case slices.Contains(st.FrameHashes, hash):
			st.Duplicates++
			st.Consecutive = 0
		default:
			st.FrameHashes = append(st.FrameHashes, hash)
			st.Attempts++
			st.LastScore, st.BestScore = score, math.Max(st.BestScore, score)
			matched = score >= s.cfg.RegulaFaceMatchThreshold
			if matched {
				st.Consecutive++
				st.LastFrame = frame
			} else {
				st.Consecutive = 0
			}
		}
		verdict.Matched, verdict.Consecutive, verdict.Attempts = matched, st.Consecutive, st.Attempts
		approved := st.Consecutive >= s.cfg.BoundLoginStableFrames
		exhausted := !approved && st.Attempts >= s.cfg.BoundLoginMaxAttempts
		if !approved && !exhausted {
			return nil
		}
		details = boundLoginOutcomeDetails(st)
		to, errorCode := session.StatusApproved, ""
		verdict.Decision = proofingprovider.FaceDecisionApproved
		if exhausted {
			to, errorCode = session.StatusRejected, errCodeFaceNoMatch
			verdict.Decision = proofingprovider.FaceDecisionRejected
		} else {
			result, err := s.buildBoundLoginResult(*sess, st)
			if err != nil {
				return err
			}
			sess.Result = result
		}
		if err := sess.SetStatus(to, time.Now().UTC()); err != nil {
			return err
		}
		sess.ErrorCode, sess.Yivi = errorCode, nil
		return nil
	})
	if err != nil {
		var rejected *proofingprovider.RejectedError
		if errors.As(err, &rejected) {
			return proofingprovider.FaceVerdict{}, err
		}
		return proofingprovider.FaceVerdict{}, fmt.Errorf("proofingengine: face frame: %w", err)
	}
	if verdict.Decision != proofingprovider.FaceDecisionPending {
		if updated.ErrorCode != "" {
			details["errorCode"] = updated.ErrorCode
		}
		s.auditProofing(updated, eventTypeForStatus(updated.Status), details)
	}
	return verdict, nil
}

// boundLoginAssurance is an approved Yivi-method session's assurance: the
// live face matched the credential's photo, with no chip check and no
// liveness, which is eIDAS low.
func boundLoginAssurance() assuranceInfo {
	return assuranceInfo{
		Level: flow.LevelForScore(flow.DefaultAssuranceTiers, 1), Score: 1, ChecksPassed: 1, ChecksTotal: 1,
		EIDASLevel: flow.AssuranceLevelLow,
	}
}

// buildBoundLoginResult is the released result, gated by requestedAttributes.
func (s *Server) buildBoundLoginResult(sess session.Session, st *session.YiviState) (map[string]any, error) {
	var info disclosureInfo
	if err := json.Unmarshal(st.Disclosure, &info); err != nil {
		return nil, fmt.Errorf("proofingengine: decode disclosure: %w", err)
	}
	result := map[string]any{}
	if attrRequested(sess, attrDisclosedAttributes, attrDocument) {
		result["document"] = documentFromDisclosure(info)
	} else {
		info.Attributes = nil
	}
	result["disclosure"] = info
	result["assurance"] = boundLoginAssurance()
	if st.KeepPhoto && attrRequested(sess, attrDG2, attrFaceImage) {
		result["photo"] = &photoInfo{ImageBase64: st.Reference, MimeType: st.ReferenceMime}
	}
	if st.LastFrame != "" && attrRequested(sess, attrSelfie) {
		if raw, mime, err := decodeImageBase64(st.LastFrame, "image/jpeg"); err == nil {
			result["selfie"] = &photoInfo{ImageBase64: base64.StdEncoding.EncodeToString(raw), MimeType: mime}
		}
	}
	if attrRequested(sess, attrBiometrics) {
		verified, threshold, frames, score := true, s.cfg.RegulaFaceMatchThreshold, st.Attempts, st.LastScore
		result["biometrics"] = &biometricsInfo{
			FaceMatchScore: &score, FaceVerified: &verified, LivenessResult: boundLoginLiveness, Engine: boundLoginEngine,
			Threshold: &threshold, FramesEvaluated: &frames, ReferenceSource: info.Source + ":" + info.PhotoAttribute,
			Injection: &injectionInfo{DuplicateFrames: st.Duplicates, Checks: []string{boundLoginDuplicateFrames}},
		}
	}
	return result, nil
}

// boundLoginOutcomeDetails is the terminal event's face-check details.
func boundLoginOutcomeDetails(st *session.YiviState) map[string]any {
	return map[string]any{
		"stage": "face", "faceMatchScore": st.LastScore, "bestScore": st.BestScore, "frames": st.Attempts,
		"duplicateFrames": st.Duplicates, "livenessResult": boundLoginLiveness, "engine": boundLoginEngine,
	}
}

// boundLoginFinish settles sess as to (stepping through opened and
// in_progress), stores result and error code, clears the face-check state and
// reports it.
func (s *Server) boundLoginFinish(sess session.Session, to session.Status, errorCode string, result map[string]any, details map[string]any) (session.Session, bool) {
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if sess.Status.Terminal() {
			return errSessionComplete
		}
		now := time.Now().UTC()
		if sess.Status == session.StatusCreated {
			if err := sess.SetStatus(session.StatusOpened, now); err != nil {
				return err
			}
		}
		if sess.Status == session.StatusOpened && to != session.StatusCancelled && to != session.StatusExpired {
			if err := sess.SetStatus(session.StatusInProgress, now); err != nil {
				return err
			}
		}
		if err := sess.SetStatus(to, now); err != nil {
			return err
		}
		sess.ErrorCode, sess.Yivi = errorCode, nil
		if result != nil {
			sess.Result = result
		}
		return nil
	})
	if err != nil {
		if !errors.Is(err, errSessionComplete) {
			slog.Error("identity proofing: could not finish a Yivi-method session",
				slog.String("session_id", sess.ID), slog.String("status", string(to)), slog.Any("error", err))
		}
		return session.Session{}, false
	}
	if details == nil {
		details = map[string]any{}
	}
	if updated.ErrorCode != "" {
		details["errorCode"] = updated.ErrorCode
	}
	s.auditProofing(updated, eventTypeForStatus(updated.Status), details)
	return updated, true
}

// documentFromDisclosure maps the passport/idcard claims onto documentInfo.
func documentFromDisclosure(info disclosureInfo) documentInfo {
	a := info.Attributes
	doc := documentInfo{
		Type: a["documentType"], Number: a["documentNumber"], IssuingState: a["country"], Nationality: a["nationality"],
		FirstName: a["firstName"], LastName: a["lastName"], Sex: a["gender"],
		DateOfBirth: a["dateOfBirth"], DateOfExpiry: a["dateOfExpiry"],
	}
	if doc.FirstName == "" && doc.LastName == "" {
		doc.DisplayName = firstNonEmpty(a["displayName"], a["name"], a["fullName"])
	} else {
		doc.DisplayName = strings.TrimSpace(doc.FirstName + " " + doc.LastName)
	}
	return doc
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// decodeImageBase64 accepts a data URL or bare (standard or raw) base64 and
// returns the bytes plus the MIME type: the data URL's, else defaultMime.
func decodeImageBase64(input, defaultMime string) ([]byte, string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, "", errors.New("image is empty")
	}
	mime := defaultMime
	if strings.HasPrefix(input, "data:") {
		idx := strings.Index(input, ",")
		if idx < 0 {
			return nil, "", errors.New("image data URL has no payload")
		}
		if meta := input[len("data:"):idx]; meta != "" {
			mime = strings.TrimSpace(strings.Split(meta, ";")[0])
		}
		input = input[idx+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(input)
		if err != nil {
			return nil, "", fmt.Errorf("image is not valid base64: %w", err)
		}
	}
	return raw, mime, nil
}
