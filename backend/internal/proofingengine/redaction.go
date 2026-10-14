package proofingengine

import (
	"errors"
	"log/slog"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/redact"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// redactStepsForStorage applies the BSN and redaction policies to the stored
// steps. It runs only after buildResult: the face match reads the raw chip
// portrait, so redacting earlier would match against a blurred image. The raw
// DG1 is dropped too; Passive Authentication already ran on it.
func redactStepsForStorage(sess *session.Session, bsnPolicy privacy.BSNPolicy, redaction privacy.RedactionPolicy) {
	var storedState string
	if sess.Steps.Document != nil {
		storedState = sess.Steps.Document.Parsed.IssuingState
	}
	if coversDocumentBSN(redaction, bsnPolicy, storedState) {
		redactStoredDocumentPhoto(sess)
	}
	if !redaction.KeepFacePhotos && sess.Steps.Selfie != nil && sess.Steps.Selfie.Image != "" {
		// A copy, not a write through the pointer (see handleSubmitNFCStep). The
		// verdicts stay; only the picture goes.
		selfie := *sess.Steps.Selfie
		selfie.Image, selfie.MimeType = "", ""
		sess.Steps.Selfie = &selfie
	}
	if sess.Steps.NFC == nil {
		return
	}
	req, err := decodeNFCStepRequest(sess.Steps.NFC.Raw)
	if err != nil {
		return
	}
	changed := false
	if !redaction.KeepFacePhotos && req.Photo != nil {
		req.Photo = nil
		changed = true
	}
	if req.MrtdEvidence != nil {
		// The raw data groups are the same face and the same BSN again, as
		// the chip's own bytes. Passive Authentication needed them; the result
		// is built.
		if !redaction.KeepFacePhotos {
			changed = dropDataGroups(req.MrtdEvidence, dataGroupPortrait, dataGroupDLPortrait) || changed
		}
		if p := bsnPolicy.Effective(); (p == privacy.BSNPolicyMask || p == privacy.BSNPolicyOmit) && dutchDocument(*sess, req) {
			// Older Dutch documents carry the BSN in the MRZ's optional data
			// (DG1) as well as in DG11.
			changed = dropDataGroups(req.MrtdEvidence, dataGroupMRZ, dataGroupPersonalDetails) || changed
		}
	}
	if !changed {
		return
	}
	raw, err := marshalToMap(req)
	if err != nil {
		// Never keep the unredacted original: without a re-marshalled copy,
		// drop the stored chip evidence's raw form altogether.
		slog.Warn("identity proofing: could not store the redacted chip evidence", slog.String("session_id", sess.ID), slog.Any("error", err))
		raw = nil
	}
	nfc := *sess.Steps.NFC
	nfc.Raw = raw
	sess.Steps.NFC = &nfc
}

const (
	dataGroupMRZ             = "DG1"
	dataGroupPortrait        = "DG2"
	dataGroupDLPortrait      = "DG6"
	dataGroupPersonalDetails = "DG11"
	// dataGroupAAKey is a passport's or ID card's Active Authentication key.
	dataGroupAAKey = "DG15"
	// dataGroupDLAAKey is an EU driving licence's Active Authentication key.
	dataGroupDLAAKey = "DG13"
)

func dropDataGroups(ev *mrtdEvidenceRequest, names ...string) bool {
	dropped := false
	for _, name := range names {
		if _, ok := ev.DataGroups[name]; ok {
			delete(ev.DataGroups, name)
			dropped = true
		}
	}
	return dropped
}

// coversDocumentBSN reports whether a document photo's printed BSN is
// covered: under BlurBSN, and under a mask or omit BSN policy unless the
// document is known not to be Dutch.
func coversDocumentBSN(redaction privacy.RedactionPolicy, bsnPolicy privacy.BSNPolicy, issuingState string) bool {
	if redaction.BlurBSN {
		return true
	}
	p := bsnPolicy.Effective()
	return (p == privacy.BSNPolicyMask || p == privacy.BSNPolicyOmit) && (issuingState == "" || issuingState == dutchIssuingState)
}

// resultIssuingState is the issuing state of the document a result is built
// from: the submitted one, else the captured one.
func resultIssuingState(sess session.Session, req appResultRequest) string {
	if req.Document != nil && req.Document.IssuingState != "" {
		return req.Document.IssuingState
	}
	if sess.Steps.Document != nil {
		return sess.Steps.Document.Parsed.IssuingState
	}
	return ""
}

func dutchDocument(sess session.Session, req submitNFCStepRequest) bool {
	if req.Document != nil && req.Document.IssuingState != "" {
		return req.Document.IssuingState == dutchIssuingState
	}
	return sess.Steps.Document != nil && sess.Steps.Document.Parsed.IssuingState == dutchIssuingState
}

func documentImageFromSide(side *session.DocumentPhotoSide) *documentImageInfo {
	if side == nil {
		return nil
	}
	img := &documentImageInfo{ImageBase64: side.Image, MimeType: side.MimeType}
	if r := side.BSNRegion; r != nil {
		img.BSNRegion = &imageRegion{X: r.X, Y: r.Y, W: r.W, H: r.H}
	}
	return img
}

// redactStoredDocumentPhoto covers the printed BSN in the stored photos as
// buildResult did in the result, and drops a photo it could not cover.
func redactStoredDocumentPhoto(sess *session.Session) {
	p := sess.Steps.DocumentPhoto
	if p == nil {
		return
	}
	photo := *p
	photo.Front = redactedSide(sess, photo.Front)
	if photo.Back != nil {
		back := redactedSide(sess, *photo.Back)
		photo.Back = &back
	}
	sess.Steps.DocumentPhoto = &photo
}

func redactedSide(sess *session.Session, side session.DocumentPhotoSide) session.DocumentPhotoSide {
	if side.Image == "" {
		return side
	}
	var region *imageRegion
	if r := side.BSNRegion; r != nil {
		region = &imageRegion{X: r.X, Y: r.Y, W: r.W, H: r.H}
	}
	if blurred, mime, err := coverBSN(side.Image, side.MimeType, region); err != nil {
		slog.Warn("identity proofing: could not blur the BSN in a document photo; dropping it", slog.String("session_id", sess.ID), slog.Any("error", err))
		side.Image, side.MimeType = "", ""
	} else {
		side.Image, side.MimeType = blurred, mime
	}
	return side
}

// errNoBSNRegion is a document photo under a BlurBSN policy whose BSN the app
// did not locate: the server never guesses where it is, so the photo is
// withheld rather than kept or released readable.
var errNoBSNRegion = errors.New("no BSN region to cover")

// coverBSN fills the printed BSN's region in a document photo.
func coverBSN(imageBase64, mimeType string, region *imageRegion) (string, string, error) {
	if region == nil {
		return "", "", errNoBSNRegion
	}
	return redact.Region(imageBase64, mimeType, redact.Rect{X: region.X, Y: region.Y, W: region.W, H: region.H})
}
