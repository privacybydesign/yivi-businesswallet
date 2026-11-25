package proofingengine

import (
	"context"
	"slices"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// nationalityDocuments are the documents that carry a nationality: a
// driving licence has none.
var nationalityDocuments = []string{flow.DocumentKindPassport, flow.DocumentKindIdentityCard, flow.DocumentKindResidencePermit}

// faceSteps are the steps that check a live face.
var faceSteps = []flow.Step{flow.StepFaceVerification, flow.StepSelfie, flow.StepLiveness, flow.StepFaceMatch}

// sessionAsked is what sess's flow asks for; nil when the flow is unknown.
func (s *Server) sessionAsked(ctx context.Context, sess session.Session) (*pp.Asked, error) {
	fd, err := s.resolveSessionFlow(ctx, sess)
	if err != nil || fd == nil {
		return nil, err
	}
	return askedOf(*fd, sess), nil
}

// askedOf is what fd asks of sess. The Yivi app shares a document it read
// earlier, so only a chip read in the Idem app has chip checks.
func askedOf(fd flow.FlowDefinition, sess session.Session) *pp.Asked {
	photo := slices.Contains(fd.Steps, flow.StepDocumentPhoto)
	chip := slices.Contains(fd.Steps, flow.StepNFCRead) &&
		sess.Method != session.MethodBiometricBoundLogin && sess.Method != session.MethodBrowser
	documents := slices.DeleteFunc(slices.Clone(fd.AcceptedDocumentTypes), func(t string) bool { return t == flow.DocumentKindPersonalData })
	// The Yivi app shares a document unless the flow takes the personal data alone.
	yiviDocument := sess.Method == session.MethodBiometricBoundLogin && (len(documents) > 0 || !fd.AcceptsPersonalData())
	readsDocument := photo || chip || yiviDocument
	anyDocument := len(documents) == 0
	carriesNationality := anyDocument || slices.ContainsFunc(documents, func(t string) bool { return slices.Contains(nationalityDocuments, t) })
	bsnPolicy, _ := effectivePrivacyPolicy(&fd)
	keepsBSN := bsnPolicy.Effective() != privacy.BSNPolicyOmit
	return &pp.Asked{
		Nationality:      attrRequested(sess, attrDocument) && readsDocument && carriesNationality,
		DutchNationality: fd.AcceptsPersonalData(),
		BSN:              keepsBSN && (fd.AcceptsPersonalData() || photo || (chip && attrRequested(sess, attrDG11))),
		ChipChecks:       chip,
		Face:             slices.ContainsFunc(fd.Steps, func(st flow.Step) bool { return slices.Contains(faceSteps, st) }),
		DocumentCheck:    photo,
	}
}
