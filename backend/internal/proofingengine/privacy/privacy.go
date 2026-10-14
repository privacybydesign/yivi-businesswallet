// Package privacy holds the privacy settings a flow sets on what a session
// keeps: the BSN policy, image redaction and the GDPR processing basis. Unset,
// the defaults here apply.
package privacy

// BSNPolicy controls what happens to a Dutch document's BSN (read off DG11
// or the MRZ optional data) before it is stored: kept, masked or dropped.
// Applied server side whatever the session asked to receive back.
type BSNPolicy string

const (
	// BSNPolicyRetrieve keeps the BSN as extracted.
	BSNPolicyRetrieve BSNPolicy = "retrieve"
	// BSNPolicyMask replaces all but the BSN's last three digits with stars
	// (internal/proofingengine/bsn.Mask).
	BSNPolicyMask BSNPolicy = "mask"
	// BSNPolicyOmit drops the BSN entirely before storage.
	BSNPolicyOmit BSNPolicy = "omit"
)

// DefaultBSNPolicy is what an empty policy means. An organisation may process
// a BSN only where a law allows it (Wet algemene bepalingen
// burgerservicenummer), so keeping one is a flow's explicit choice.
const DefaultBSNPolicy = BSNPolicyOmit

// ValidBSNPolicy reports whether p is a known BSNPolicy, the empty one included.
func ValidBSNPolicy(p BSNPolicy) bool {
	switch p {
	case "", BSNPolicyRetrieve, BSNPolicyMask, BSNPolicyOmit:
		return true
	default:
		return false
	}
}

// Effective is the policy to apply: an empty one is DefaultBSNPolicy.
func (p BSNPolicy) Effective() BSNPolicy {
	if p == "" {
		return DefaultBSNPolicy
	}
	return p
}

// LegalBasis is the GDPR Article 6(1) ground a flow processes personal data
// under. Empty means not configured; there is no default.
type LegalBasis string

const (
	LegalBasisConsent             LegalBasis = "consent"
	LegalBasisContract            LegalBasis = "contract"
	LegalBasisLegalObligation     LegalBasis = "legal_obligation"
	LegalBasisVitalInterests      LegalBasis = "vital_interests"
	LegalBasisPublicTask          LegalBasis = "public_task"
	LegalBasisLegitimateInterests LegalBasis = "legitimate_interests"
)

// ValidLegalBasis reports whether b is a known LegalBasis, the empty one included.
func ValidLegalBasis(b LegalBasis) bool {
	switch b {
	case "", LegalBasisConsent, LegalBasisContract, LegalBasisLegalObligation,
		LegalBasisVitalInterests, LegalBasisPublicTask, LegalBasisLegitimateInterests:
		return true
	default:
		return false
	}
}

// RedactionPolicy is which evidence images are blurred before storage.
type RedactionPolicy struct {
	// BlurFace blurs the document portrait (DG2) and the live selfie.
	BlurFace bool
	// BlurBSN blurs a document photo's located BSN region.
	BlurBSN bool
}
