package proofingengine

import (
	"log/slog"
	"slices"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/bsn"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/images"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// requestedAttributesOf is what fd releases: its own list, or everything its
// steps collect when it lists none. Only attrOutcomeOnly, chosen explicitly,
// releases nothing but the outcome.
func requestedAttributesOf(fd flow.FlowDefinition) []string {
	if len(fd.RequestedAttributes) > 0 {
		return fd.RequestedAttributes
	}
	if attrs := attributesForSteps(fd.Steps); len(attrs) > 0 {
		return attrs
	}
	return []string{attrOutcomeOnly}
}

// attributesForSteps is every result attribute steps collect.
func attributesForSteps(steps []flow.Step) []string {
	var attrs []string
	add := func(vs ...string) {
		for _, v := range vs {
			if !slices.Contains(attrs, v) {
				attrs = append(attrs, v)
			}
		}
	}
	for _, step := range steps {
		switch step {
		case flow.StepDocumentCapture:
			add(attrDocument, attrDocumentImage)
		case flow.StepNFCRead:
			add(attrDG11, attrDG2, attrChipChecks)
		case flow.StepDocumentPhoto:
			add(attrDocumentImage)
		case flow.StepSelfie:
			add(attrSelfie)
		case flow.StepLiveness, flow.StepFaceMatch:
			add(attrBiometrics)
		case flow.StepFaceVerification:
			add(attrSelfie, attrBiometrics)
		}
	}
	return attrs
}

// Result attributes a flow can request (requestedAttributes).
const (
	attrDocument      = "dg1"        // document/MRZ fields, holder identity, validity
	attrDG11          = "dg11"       // DG11 extras: personalNumber, placeOfBirth
	attrDG2           = "dg2"        // raw face image off the chip
	attrFaceImage     = "face_image" // alias for attrDG2
	attrChipChecks    = "chip_checks"
	attrBiometrics    = "biometrics"
	attrSelfie        = "selfie"         // live selfie captured during face verification
	attrDocumentImage = "document_image" // visual (VIZ) capture of the document
	// attrOutcomeOnly is a flow's whole list when the admin chose to release no
	// data: it names no result attribute, so nothing but the outcome (status,
	// assurance) is released.
	attrOutcomeOnly = "outcome_only"
)

// attrRequested reports whether any of keys was requested. An empty
// RequestedAttributes list is unrestricted: only a session no flow governs
// has one, since a flow's session gets requestedAttributesOf the flow.
func attrRequested(sess session.Session, keys ...string) bool {
	if len(sess.RequestedAttributes) == 0 {
		return true
	}
	for _, want := range keys {
		if slices.Contains(sess.RequestedAttributes, want) {
			return true
		}
	}
	return false
}

// buildResult is the stored and released result: only what the session
// requested, after the flow's BSN and redaction policy. It is the one place
// evidence becomes a result, so nothing leaves raw. Chip checks are the
// server's own (verifiedChipChecks), never the app's.
func buildResult(sess session.Session, req appResultRequest, verifiedChipChecks *chipChecksInfo, fd *flow.FlowDefinition, bsnPolicy privacy.BSNPolicy, redaction privacy.RedactionPolicy) map[string]any {
	result := map[string]any{}

	if req.Document != nil && attrRequested(sess, attrDocument) {
		doc := *req.Document
		applyBSNPolicy(&doc, bsnPolicy)
		if !attrRequested(sess, attrDG11) {
			doc.PersonalNumber = ""
			doc.PlaceOfBirth = ""
		}
		result["document"] = doc
	}

	if req.Photo != nil && attrRequested(sess, attrDG2, attrFaceImage) {
		photo := *req.Photo
		// Browsers do not render JPEG2000, which DG2 portraits often are.
		if converted, mime, err := images.ToDisplayablePNG(photo.ImageBase64, photo.MimeType); err != nil {
			slog.Warn("identity proofing: could not convert the photo for display", slog.String("session_id", sess.ID), slog.Any("error", err))
		} else {
			photo.ImageBase64, photo.MimeType = converted, mime
		}
		if released, ok := releasedFace(photo, redaction); ok {
			result["photo"] = released
		} else {
			slog.Warn("identity proofing: could not blur the photo; leaving it out", slog.String("session_id", sess.ID))
		}
	}

	if req.Selfie != nil && attrRequested(sess, attrSelfie) {
		selfie := *req.Selfie
		if converted, mime, err := images.ToDisplayablePNG(selfie.ImageBase64, selfie.MimeType); err != nil {
			slog.Warn("identity proofing: could not convert the selfie for display", slog.String("session_id", sess.ID), slog.Any("error", err))
		} else {
			selfie.ImageBase64, selfie.MimeType = converted, mime
		}
		if released, ok := releasedFace(selfie, redaction); ok {
			result["selfie"] = released
		} else {
			slog.Warn("identity proofing: could not blur the selfie; leaving it out", slog.String("session_id", sess.ID))
		}
	}

	// A face matched against the relying party's own photo (a flow without
	// nfc_read) says so, and releases that photo with the selfie, so whoever
	// reads the result sees both faces that were compared.
	if sess.ReferencePhoto != "" {
		result["faceReference"] = faceReferenceRelyingParty
		if attrRequested(sess, attrSelfie) {
			ref := photoInfo{ImageBase64: sess.ReferencePhoto, MimeType: sess.ReferencePhotoMime}
			if released, ok := releasedFace(ref, redaction); ok {
				result["referencePhoto"] = released
			} else {
				slog.Warn("identity proofing: could not blur the reference photo; leaving it out", slog.String("session_id", sess.ID))
			}
		}
	}

	if attrRequested(sess, attrDocumentImage) {
		if img := releasedDocumentImage(sess, req.DocumentImage, redaction); img != nil {
			result["documentImage"] = img
		}
		if img := releasedDocumentImage(sess, req.DocumentImageBack, redaction); img != nil {
			result["documentImageBack"] = img
		}
	}

	if verifiedChipChecks != nil && attrRequested(sess, attrChipChecks) {
		result["chipChecks"] = verifiedChipChecks
	}
	if req.Biometrics != nil && attrRequested(sess, attrBiometrics) {
		result["biometrics"] = req.Biometrics
	}
	if req.Device != nil {
		result["device"] = req.Device
	}

	// Released whatever was requested, like device: how well the session was
	// verified, not personal data.
	result["assurance"] = computeAssurance(fd, req, verifiedChipChecks, faceMatchSourceOf(sess))
	return result
}

// faceReferenceRelyingParty is a result's faceReference when the face was
// matched against the relying party's own photo rather than the chip's DG2.
const faceReferenceRelyingParty = "relying_party"

// releasedDocumentImage is docImage as the result carries it: its BSN region
// covered under a BlurBSN policy. Nil when there is none, or when the BSN
// could not be covered (no region came in, or covering failed): the photo is
// then withheld, never released readable.
func releasedDocumentImage(sess session.Session, docImage *documentImageInfo, redaction privacy.RedactionPolicy) *documentImageInfo {
	if docImage == nil {
		return nil
	}
	out := *docImage
	if redaction.BlurBSN {
		blurred, mime, err := coverBSN(out.ImageBase64, out.MimeType, out.BSNRegion)
		if err != nil {
			slog.Warn("identity proofing: could not cover the BSN in a document image; withholding it", slog.String("session_id", sess.ID), slog.Any("error", err))
			return nil
		}
		out.ImageBase64, out.MimeType = blurred, mime
	}
	return &out
}

// dutchIssuingState is the only issuing state whose DG11 personal number is a
// BSN.
const dutchIssuingState = "NLD"

// applyBSNPolicy applies the BSN policy to doc.PersonalNumber before doc goes
// into any result, whatever was requested. Only Dutch documents: other
// countries' personal numbers are not BSNs. A Dutch number failing the
// 11-proef is dropped whatever the policy.
func applyBSNPolicy(doc *documentInfo, policy privacy.BSNPolicy) {
	if doc.PersonalNumber == "" || doc.IssuingState != dutchIssuingState {
		return
	}
	if !bsn.Masked(doc.PersonalNumber) && !bsn.Valid(doc.PersonalNumber) {
		doc.PersonalNumber = ""
		return
	}
	switch policy.Effective() {
	case privacy.BSNPolicyOmit:
		doc.PersonalNumber = ""
	case privacy.BSNPolicyMask:
		doc.PersonalNumber = bsn.Mask(doc.PersonalNumber)
	case privacy.BSNPolicyRetrieve:
	}
}

// redactBSNFromEvidence drops the raw DG11 from a Dutch document's evidence
// under a mask or omit policy, before it is stored: raw ASN.1 has no masked
// form. Chip verification does not need DG11. It runs even with an empty
// parsed personal number: older chips carry a BSN in DG11 the MRZ does not
// show.
func redactBSNFromEvidence(ev *mrtdEvidenceRequest, doc *documentInfo, policy privacy.BSNPolicy) {
	if ev == nil || doc == nil || doc.IssuingState != dutchIssuingState {
		return
	}
	switch policy.Effective() {
	case privacy.BSNPolicyMask, privacy.BSNPolicyOmit:
		delete(ev.DataGroups, dataGroupPersonalDetails)
	}
}
