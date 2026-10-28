package proofingengine

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/images"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// The Yivi method: the wallet verifies the subject's OpenID4VP disclosure of a
// passport or ID-card credential and hands the engine its claims, with the
// photo only when the flow keeps it (SubmitReference). The disclosure decides
// the session: the Yivi app vouches for its holder, so no face check runs
// here, and the session reaches eIDAS low (boundLoginAssurance).

// eventDisclosureReceived marks the engine accepting a disclosure as reference.
const eventDisclosureReceived = "proofing.disclosure.received"

// Limits and names of a Yivi-method disclosure.
const (
	yiviPhotoAttribute        = "photo"      // the passport/idcard credentials' photo claim
	attrDisclosedAttributes   = "attributes" // requestedAttributes key releasing the disclosed values
	maxDisclosedPhotoBytes    = 4 << 20      // a passport photo is tens of kB
	referenceSourceOpenID4VP  = "openid4vp"
	maxReferenceAttributes    = 20
	maxReferenceAttrKeyLen    = 40
	maxReferenceAttrValueLen  = 200
	maxReferenceCredentialLen = 200
)

// Codes SubmitReference refuses a disclosure with: the flow restricts the
// document type, issuing country or expiry and the disclosure does not show
// it complies, or the flow checks the age and the disclosure does not answer
// it.
const (
	disclosureCodeDocumentRefused = "document_not_accepted"
	disclosureCodeAgeNotDisclosed = "age_not_disclosed"
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

// acceptReference decides sess from its disclosure ref: approved with the
// result, or rejected when the document does not comply with the flow.
func (s *Server) acceptReference(ctx context.Context, sess session.Session, ref proofingprovider.Reference) (proofingprovider.YiviDisclosure, error) {
	if err := boundLoginSession(sess); err != nil {
		return proofingprovider.YiviDisclosure{}, err
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

	fd, err := s.resolveSessionFlow(ctx, sess)
	if err != nil {
		return proofingprovider.YiviDisclosure{}, fmt.Errorf("proofingengine: accept reference: %w", err)
	}
	var ageOver *ageOverInfo
	if asksAge(fd) {
		ageOver = disclosedAgeOver(info.Attributes, fd.AgeOver)
		withholdDisclosedBirthDate(&info)
	}

	details := map[string]any{detailStage: "disclosure", "credential": info.Credential}
	if failed, errorCode := disclosureComplianceFailure(fd, documentFromDisclosure(info), time.Now()); failed {
		details[detailReason] = disclosureCodeDocumentRefused
		s.boundLoginFinish(sess, session.StatusRejected, errorCode, nil, details)
		return proofingprovider.YiviDisclosure{OK: false, Code: disclosureCodeDocumentRefused}, nil
	}
	if asksAge(fd) && ageOver == nil {
		details[detailReason] = disclosureCodeAgeNotDisclosed
		s.boundLoginFinish(sess, session.StatusRejected, errCodeAgeNotDisclosed, nil, details)
		return proofingprovider.YiviDisclosure{OK: false, Code: disclosureCodeAgeNotDisclosed}, nil
	}

	_, redaction := effectivePrivacyPolicy(fd)
	result := buildBoundLoginResult(sess, info, s.disclosedPhoto(sess, ref.Photo, redaction), ageOver)

	if sess.Status == session.StatusCreated {
		opened := sess
		opened.Status = session.StatusOpened
		s.auditProofing(opened, eventSessionOpened, nil)
	}
	s.auditProofing(sess, eventDisclosureReceived, map[string]any{"credential": info.Credential})
	if _, ok := s.boundLoginFinish(sess, session.StatusApproved, "", result, details); !ok {
		return proofingprovider.YiviDisclosure{}, fmt.Errorf("proofingengine: accept reference: session %s not approved", sess.ID)
	}
	return proofingprovider.YiviDisclosure{OK: true}, nil
}

// disclosedPhoto is the disclosed photo as the result releases it: only when
// the flow keeps the face photos and asks for one, as in buildResult. A photo
// that cannot be read is left out.
func (s *Server) disclosedPhoto(sess session.Session, photo string, redaction privacy.RedactionPolicy) *photoInfo {
	if strings.TrimSpace(photo) == "" || !redaction.KeepFacePhotos || !attrRequested(sess, attrDG2, attrFaceImage) {
		return nil
	}
	raw, mime, err := decodeImageBase64(photo, defaultImageMime)
	if err != nil || len(raw) > maxDisclosedPhotoBytes {
		slog.Warn("identity proofing: could not read the disclosed photo; leaving it out", slog.String("session_id", sess.ID))
		return nil
	}
	image := base64.StdEncoding.EncodeToString(raw)
	// A chip-derived photo may be JPEG2000, which browsers need as PNG.
	if converted, convertedMime, err := images.ToDisplayablePNG(image, mime); err == nil && converted != "" {
		image, mime = converted, convertedMime
	}
	return &photoInfo{ImageBase64: image, MimeType: mime}
}

// disclosureComplianceFailure is flowComplianceFailure for a disclosed
// document: a flow restricting the type or issuing country refuses one that
// does not show it, and a disclosed expiry in the past is refused.
func disclosureComplianceFailure(fd *flow.FlowDefinition, doc documentInfo, now time.Time) (failed bool, errorCode string) {
	if fd == nil {
		return false, ""
	}
	if !fd.AcceptsDocumentType(doc.Type, doc.Kind) {
		return true, errCodeDocTypeRefused
	}
	if len(fd.AcceptedIssuingCountries) > 0 &&
		(doc.IssuingState == "" || !slices.Contains(fd.AcceptedIssuingCountries, flow.IssuingStateCode(doc.IssuingState))) {
		return true, errCodeCountryRefused
	}
	if expiry, ok := disclosedDate(doc.DateOfExpiry); ok && expiry.Before(now.UTC().Truncate(day)) {
		return true, errCodeDocExpired
	}
	return false, ""
}

// disclosedDateLayouts are the date formats a disclosed credential uses.
var disclosedDateLayouts = []string{time.DateOnly, "02-01-2006"}

// disclosedDate parses a disclosed date; ok is false when absent or in no
// known format, the credential's own validity then standing for it.
func disclosedDate(raw string) (time.Time, bool) {
	for _, layout := range disclosedDateLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// boundLoginAssurance is an approved Yivi-method session's assurance: the Yivi
// app vouches for the holder, with no chip check and no face check here,
// which is eIDAS low. It is not scored on the tier ladder, which needs checks
// this method does not run.
func boundLoginAssurance() assuranceInfo {
	return assuranceInfo{EIDASLevel: flow.AssuranceLevelLow}
}

// buildBoundLoginResult is the released result, gated by requestedAttributes:
// the disclosed document and claims, the age answer, and photo when the flow
// keeps it (disclosedPhoto).
func buildBoundLoginResult(sess session.Session, info disclosureInfo, photo *photoInfo, ageOver *ageOverInfo) map[string]any {
	result := map[string]any{}
	if attrRequested(sess, attrDisclosedAttributes, attrDocument) {
		result["document"] = documentFromDisclosure(info)
	} else {
		info.Attributes = nil
	}
	result["disclosure"] = info
	if ageOver != nil && attrRequested(sess, attrAgeOver) {
		result["ageOver"] = ageOver
	}
	if photo != nil {
		result["photo"] = photo
	}
	result["assurance"] = boundLoginAssurance()
	return result
}

// boundLoginFinish settles sess as to (stepping through opened and
// in_progress), stores result and error code, and reports it.
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
		sess.ErrorCode = errorCode
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
		details[detailErrorCode] = updated.ErrorCode
	}
	s.auditProofing(updated, eventTypeForStatus(updated.Status), details)
	return updated, true
}

// yiviDocumentKinds are the kinds of the identity credentials, by the last
// segment of their vct (pbdf-staging.pbdf.residencepermit).
var yiviDocumentKinds = map[string]string{
	"passport":        flow.DocumentKindPassport,
	"idcard":          flow.DocumentKindIdentityCard,
	"residencepermit": flow.DocumentKindResidencePermit,
}

// documentFromDisclosure maps the passport, idcard or residencepermit claims
// onto documentInfo.
func documentFromDisclosure(info disclosureInfo) documentInfo {
	a := info.Attributes
	doc := documentInfo{
		Type: a["documentType"], Kind: yiviDocumentKinds[info.Credential[strings.LastIndex(info.Credential, ".")+1:]],
		Number: a["documentNumber"], IssuingState: a["country"], Nationality: a["nationality"],
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
