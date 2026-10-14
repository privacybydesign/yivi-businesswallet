package proofingengine

import (
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/mrtdverify"
)

// mrtdEvidenceRequest is the raw chip data the server verifies itself: Passive
// Authentication (EF.SOD against a CSCA anchor, data-group hashes) and
// Active/Chip Authentication (the chip's signature over the server's
// challenge). Hex fields are hex-encoded.
type mrtdEvidenceRequest struct {
	EFSOD string `json:"efSod"`
	// DataGroups are the raw data groups as read: Passive Authentication hashes
	// these bytes.
	DataGroups map[string]string `json:"dataGroups"`
	// DocumentType picks the Passive Authentication path; empty is the ICAO path.
	// An explicit field because a passport without DG15 also has no
	// AAKeyDataGroup.
	DocumentType string `json:"documentType,omitempty"`
	// AAKeyDataGroup names the data group with the Active Authentication key:
	// DG15 for passports and ID cards, DG13 for driving licences; see
	// resolveAAKeyDataGroup for its default.
	AAKeyDataGroup string `json:"aaKeyDataGroup,omitempty"`
	// Nonce and AASignature are the challenge sent to the chip and its signature,
	// required with AAKeyDataGroup.
	Nonce       string `json:"nonce,omitempty"`
	AASignature string `json:"aaSignature,omitempty"`
}

// Document types of mrtdEvidenceRequest. Empty or "icao" is a passport or ID
// card, verified with mrtdverify.VerifyPassiveICAO, which also checks the
// signer's country against DG1's and narrows the trust pool to it.
const (
	// documentTypeEUDrivingLicence runs mrtdverify.VerifyPassiveDrivingLicence
	// against the Dutch licence CSCAs: licences use DG1/DG6/DG13 encodings the
	// typed parsers cannot read. Only a Dutch licence gets this far
	// (drivingLicenceSupported), so the pool is the country cross-check.
	documentTypeEUDrivingLicence = flow.DocumentTypeEUDrivingLicence
)

// resolveAAKeyDataGroup defaults ev.AAKeyDataGroup to the document's Active
// Authentication key (DG15, a licence's DG13) when the client sent it but
// named no key group, as vcmrtd does when it ran no AA; without it a missing
// response would go unnoticed. An explicit value wins.
func resolveAAKeyDataGroup(ev *mrtdEvidenceRequest) string {
	if ev.AAKeyDataGroup != "" {
		return ev.AAKeyDataGroup
	}
	key := dataGroupAAKey
	if ev.DocumentType == documentTypeEUDrivingLicence {
		key = dataGroupDLAAKey
	}
	if _, ok := ev.DataGroups[key]; ok {
		return key
	}
	return ""
}

// chipChecksInfo is the server's own verdict on the chip: Passive
// Authentication (the signed data matches what was read and chains to a
// trusted CSCA) and Active/Chip Authentication (the chip holds its private key,
// which a clone does not). Never taken from the app.
type chipChecksInfo struct {
	PassiveAuthentication *passiveAuthInfo `json:"passiveAuthentication,omitempty"`
	ActiveAuthentication  *activeAuthInfo  `json:"activeAuthentication,omitempty"`
	CloneDetected         *bool            `json:"cloneDetected,omitempty"`
	TamperDetected        *bool            `json:"tamperDetected,omitempty"`
}

type passiveAuthInfo struct {
	SODSignatureValid    *bool    `json:"sodSignatureValid,omitempty"`
	DataGroupHashesValid *bool    `json:"dataGroupHashesValid,omitempty"`
	InvalidDataGroups    []string `json:"invalidDataGroups,omitempty"` // e.g. ["DG2"] when a hash mismatched
	CSCATrustChainValid  *bool    `json:"cscaTrustChainValid,omitempty"`
	IssuingCSCA          string   `json:"issuingCsca,omitempty"`
	// DocumentComplete is false when EF.SOD lists a DG14 or DG15 (a licence's
	// DG13) that was not sent.
	DocumentComplete  *bool  `json:"documentComplete,omitempty"`
	DocumentVerifyErr string `json:"documentVerifyErr,omitempty"`
}

// activeAuthInfo: Attempted says the app ran AA/CA; Passed is the proof of
// possession.
type activeAuthInfo struct {
	Attempted *bool  `json:"attempted,omitempty"`
	Passed    *bool  `json:"passed,omitempty"`
	Method    string `json:"method,omitempty"` // "active_authentication" | "chip_authentication"
}

// verifyMrtdEvidence runs Passive and, where the chip supports it, Active
// Authentication on the raw chip data: the only source of chip checks. Active
// Authentication counts only with the session's own challenge
// (expectedAAChallenge), against relay and replay. An error is malformed
// evidence (a bad request); a check that ran and failed is in the result.
func (s *Server) verifyMrtdEvidence(ev *mrtdEvidenceRequest, expectedAAChallenge string) (*chipChecksInfo, error) {
	if ev == nil {
		return nil, nil
	}

	aaKeyDataGroup := resolveAAKeyDataGroup(ev)
	pool, verifyPassive := s.cfg.DrivingLicenceCertPool, mrtdverify.VerifyPassiveDrivingLicence
	if ev.DocumentType != documentTypeEUDrivingLicence {
		var err error
		if pool, err = mrtdverify.PassportCertPool(); err != nil {
			return nil, fmt.Errorf("loading CSCA trust anchor: %w", err)
		}
		verifyPassive = mrtdverify.VerifyPassiveICAO
	}
	passive, err := verifyPassive(ev.EFSOD, ev.DataGroups, pool)
	if err != nil {
		return nil, fmt.Errorf("verifying passive authentication: %w", err)
	}
	checks := &chipChecksInfo{
		PassiveAuthentication: &passiveAuthInfo{
			SODSignatureValid:    &passive.SODSignatureValid,
			DataGroupHashesValid: &passive.DataGroupHashesValid,
			InvalidDataGroups:    passive.InvalidDataGroups,
			CSCATrustChainValid:  &passive.CSCATrustChainValid,
			IssuingCSCA:          passive.IssuingCSCA,
			DocumentComplete:     &passive.DocumentComplete,
			DocumentVerifyErr:    passive.DocumentVerifyErr,
		},
	}

	// The chip is trusted only when the whole Passive Authentication holds,
	// document completeness included.
	tamperDetected := !passive.SODSignatureValid || !passive.CSCATrustChainValid || !passive.DataGroupHashesValid || !passive.DocumentComplete
	checks.TamperDetected = &tamperDetected

	if aaKeyDataGroup != "" {
		keyHex, ok := ev.DataGroups[aaKeyDataGroup]
		if !ok {
			return nil, fmt.Errorf("aaKeyDataGroup %q not present in dataGroups", aaKeyDataGroup)
		}
		active, err := mrtdverify.VerifyActive(keyHex, ev.Nonce, ev.AASignature)
		if err != nil {
			return nil, fmt.Errorf("verifying active authentication: %w", err)
		}
		if active.Attempted && !aaChallengeMatches(ev.Nonce, expectedAAChallenge) {
			// A response to another nonce than the session's challenge proves nothing
			// about the chip being here now (a replay or relay), however valid.
			active.Passed = false
		}
		// A passport's DG15 and a licence's DG13 both hold an Active
		// Authentication key.
		method := "chip_authentication"
		if aaKeyDataGroup == dataGroupAAKey || aaKeyDataGroup == dataGroupDLAAKey {
			method = "active_authentication"
		}
		checks.ActiveAuthentication = &activeAuthInfo{
			Attempted: &active.Attempted,
			Passed:    &active.Passed,
			Method:    method,
		}
		// AA ran and did not verify: copied data without the private key, or a
		// foreign nonce.
		cloneDetected := active.Attempted && !active.Passed
		checks.CloneDetected = &cloneDetected
	}

	return checks, nil
}

// aaChallengeMatches compares the nonce with the session's challenge in
// constant time. An empty or malformed side never matches.
func aaChallengeMatches(nonceHex, expectedHex string) bool {
	if nonceHex == "" || expectedHex == "" {
		return false
	}
	nonce, err := hex.DecodeString(nonceHex)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(nonce, expected) == 1
}

// Rejection codes of a chip that failed its authenticity checks
// (authenticityFailure): data that does not match its signature, or a chip
// that did not prove it holds its private key.
const (
	errCodeDocTampered       = "DOC_TAMPERED"
	errCodeChipCloneDetected = "CHIP_CLONE_DETECTED"
)

// authenticityFailure reports a tampered or cloned chip, which rejects the
// session whatever else held. No chip evidence is not a failure.
//
// When fd asks for nfc.chip_auth and the chip carries an AA key, a response
// that is missing counts as cloned too: the app runs AA whenever the check is
// listed, so leaving it out is what a recorded read replayed without the chip
// looks like. A chip whose EF.SOD lists DG15 (a licence's DG13) cannot dodge
// this by leaving it out: Passive Authentication's completeness check marks
// that tampered.
func authenticityFailure(fd *flow.FlowDefinition, checks *chipChecksInfo) (failed bool, errorCode string) {
	if checks == nil {
		return false, ""
	}
	if checks.TamperDetected != nil && *checks.TamperDetected {
		return true, errCodeDocTampered
	}
	if checks.CloneDetected != nil && *checks.CloneDetected {
		return true, errCodeChipCloneDetected
	}
	if aa := checks.ActiveAuthentication; aa != nil && (aa.Passed == nil || !*aa.Passed) && flowListsCheck(fd, flow.CheckNFCChipAuth) {
		return true, errCodeChipCloneDetected
	}
	return false, ""
}

// flowListsCheck is whether c is among the checks the app is asked to run for
// fd (assuranceChecksFor).
func flowListsCheck(fd *flow.FlowDefinition, c flow.Check) bool {
	return fd != nil && slices.Contains(assuranceChecksFor(fd), c)
}
