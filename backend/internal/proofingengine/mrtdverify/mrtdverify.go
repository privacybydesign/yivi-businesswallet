// Package mrtdverify performs independent, server-side ICAO 9303 Passive
// Authentication (verifying the EF.SOD's signature against a CSCA trust
// anchor, then checking every submitted data group's hash against the
// signed hash list inside it) and Active Authentication (verifying the
// chip's signed challenge response against the public key carried in DG15,
// or in DG13 for EU driving licences — both encode the same "tag 0x6F wraps
// a SubjectPublicKeyInfo" shape).
//
// This exists because api.chipChecksInfo was, until now, whatever the app
// self-reported: nothing this server had independently confirmed (see
// docs/session-model.md). This package is that confirmation. Reuses the
// same gmrtd library (github.com/privacybydesign/gmrtd, imported here under
// its github.com/gmrtd/gmrtd module path via a go.mod replace) that
// go-passport-issuer already relies on for the same checks, rather than
// hand-rolling CMS/ASN.1/X.509 chain verification.
//
// Two Passive Authentication entry points, for two different document
// shapes:
//
//   - VerifyPassive is generic and document-type-agnostic: it hash-checks
//     whatever data groups were submitted and verifies EF.SOD's signature
//     against the whole trust pool, unfiltered. This is the only option for
//     EU driving licences, whose DG1/DG6/DG13 use a non-ICAO encoding that
//     gmrtd's typed document parsers cannot read at all (matches
//     go-passport-issuer's own separate, un-country-filtered EDL path).
//   - VerifyPassiveICAO is for passports/ID cards specifically: it builds a
//     typed document.Document and calls gmrtd's passiveauth.PassiveAuth,
//     which additionally cross-checks that EF.SOD's signing certificate's
//     country matches DG1 MRZ's declared issuing country and narrows the
//     trust pool to just that country's CSCAs before verifying — catching a
//     document whose signer's country doesn't match what it claims to be,
//     which VerifyPassive cannot. Mirrors go-passport-issuer's
//     PassiveAuthenticationPassport.
package mrtdverify

import (
	"bytes"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gmrtd/gmrtd/activeauth"
	"github.com/gmrtd/gmrtd/cms"
	"github.com/gmrtd/gmrtd/cryptoutils"
	"github.com/gmrtd/gmrtd/document"
	"github.com/gmrtd/gmrtd/passiveauth"
)

// PassiveResult is the outcome of Passive Authentication: whether the EF.SOD
// signature chains to a trusted CSCA, and whether every submitted data
// group's hash matches what the signed EF.SOD says it should be.
type PassiveResult struct {
	SODSignatureValid   bool
	CSCATrustChainValid bool
	// IssuingCSCA is the trusted CSCA certificate's subject, best-effort —
	// empty when the chain couldn't be parsed for a display name even
	// though verification succeeded.
	IssuingCSCA string
	// DataGroupHashesValid is false if ANY submitted data group's hash
	// doesn't match EF.SOD, or isn't listed in EF.SOD at all (the latter
	// means a data group was injected that the signed hash list never
	// covered — data tampering, not just a mismatch).
	DataGroupHashesValid bool
	// InvalidDataGroups names which submitted groups failed (e.g. ["DG2"]),
	// sorted for stable output. Empty when DataGroupHashesValid is true.
	InvalidDataGroups []string
	// DocumentComplete is false when gmrtd's own document.Document.Verify()
	// (VerifyPassiveICAO only — see there) found the submission structurally
	// incomplete relative to what EF.SOD itself expects: most importantly,
	// DG14 or DG15 referenced by EF.SOD's hash list but never submitted at
	// all. checkDataGroupHashes alone can't catch this — it only validates
	// data groups that WERE submitted, so an app (or a MITM) could otherwise
	// silently drop DG15 to dodge Active Authentication while still passing
	// every check that only looks at what's present. Always true for
	// VerifyPassive, which never builds a typed document.Document to run
	// this check against.
	DocumentComplete bool
	// DocumentVerifyErr is doc.Verify()'s error text when DocumentComplete is
	// false, for diagnostics. Empty otherwise.
	DocumentVerifyErr string
}

// ActiveResult: whether the app engaged Active/Chip Authentication (sent a
// nonce/signature, not just a key) and whether it verified.
type ActiveResult struct {
	Attempted bool
	Passed    bool
}

// VerifyPassive checks efSODHex (hex-encoded raw EF.SOD) against
// trustedCerts, then verifies every data group in dataGroupsHex (keyed
// "DG1".."DG16", hex-encoded raw bytes exactly as read off the chip — not
// re-derived from parsed fields, or the hash won't match) against the
// hashes EF.SOD lists.
//
// A non-nil error means verification could not even be attempted (malformed
// input) — the caller should treat that as a bad request. A verification
// that ran and failed (bad signature, untrusted chain, hash mismatch) is
// reported via the returned PassiveResult's fields with a nil error: that's
// an expected, legitimate outcome (a fraudulent or corrupted document), not
// a request error.
func VerifyPassive(efSODHex string, dataGroupsHex map[string]string, trustedCerts cms.CertPool) (PassiveResult, error) {
	// This path never builds a typed document.Document, so there's nothing
	// to run gmrtd's completeness check (Document.Verify) against — see
	// PassiveResult.DocumentComplete.
	result := PassiveResult{DocumentComplete: true}

	sod, err := parseSOD(efSODHex)
	if err != nil {
		return result, err
	}

	invalid, err := checkDataGroupHashes(sod, dataGroupsHex)
	if err != nil {
		return result, err
	}
	result.InvalidDataGroups = invalid
	result.DataGroupHashesValid = len(invalid) == 0

	certChain, err := sod.SD.Verify(trustedCerts)
	if err != nil {
		// The chain didn't verify — a legitimate (if unwelcome) result, not
		// a request error.
		return result, nil
	}
	result.SODSignatureValid = true
	result.CSCATrustChainValid = true
	result.IssuingCSCA = issuingCSCASubject(certChain)
	return result, nil
}

// VerifyPassiveICAO is VerifyPassive's counterpart for ICAO passports/ID
// cards — see the package doc comment for why this exists. Same inputs and
// PassiveResult shape as VerifyPassive, but SODSignatureValid/
// CSCATrustChainValid come from gmrtd's passiveauth.PassiveAuth against a
// typed document.Document (which additionally cross-checks EF.SOD's
// signing certificate's country against DG1's declared issuing country, and
// narrows the trust pool to that country) instead of a raw
// sod.SD.Verify(trustedCerts) call against the whole pool.
//
// That check comes at a cost: PassiveAuth's own internal data-group-hash
// check runs before, and gates, its signature verification, and its error
// doesn't say *why* it failed (country mismatch vs. no CSCA for that
// country vs. hash mismatch vs. bad signature) — so unlike VerifyPassive, a
// single tampered/injected data group here can also pull
// SODSignatureValid/CSCATrustChainValid down to false even though EF.SOD's
// own signature might still be genuinely valid on its own. To keep the
// granular diagnostic precise regardless, InvalidDataGroups/
// DataGroupHashesValid are still computed independently via the same
// checkDataGroupHashes helper VerifyPassive uses, not derived from
// PassiveAuth's opaque error.
//
// DG1 and DG2 must be present as entries in dataGroupsHex (matches ICAO 9303
// and go-passport-issuer's own parsePassportDGs) — an entry simply missing
// from the map is a hard request error, same convention as VerifyPassive
// (the app should always read and send DG1/DG2 for a passport/ID-card read;
// not doing so is an integration bug, not something a chip read could ever
// produce). A data group that IS present but fails to parse into its typed
// form — a corrupted or tampered MRZ, say — is different: that's exactly
// what a fraudulent or corrupted document looks like, so it's treated like
// any other verification failure (see below), not a request error. Any
// entry with an invalid name or non-hex value is still a hard error,
// regardless — checkDataGroupHashes above already validates that for every
// entry before this function does anything document-shape-specific.
func VerifyPassiveICAO(efSODHex string, dataGroupsHex map[string]string, trustedCerts cms.CertPool) (PassiveResult, error) {
	var result PassiveResult

	sod, err := parseSOD(efSODHex)
	if err != nil {
		return result, err
	}

	invalid, err := checkDataGroupHashes(sod, dataGroupsHex)
	if err != nil {
		return result, err
	}
	result.InvalidDataGroups = invalid
	result.DataGroupHashesValid = len(invalid) == 0

	if _, ok := dataGroupsHex["DG1"]; !ok {
		return result, fmt.Errorf("mrtdverify: DG1 is mandatory for ICAO passive authentication but was not provided")
	}
	if _, ok := dataGroupsHex["DG2"]; !ok {
		return result, fmt.Errorf("mrtdverify: DG2 is mandatory for ICAO passive authentication but was not provided")
	}

	// dataGroupsHex was already fully validated by checkDataGroupHashes
	// above (every name matches "DG<1-16>", every value is valid hex), so
	// the only way doc.NewDG can still fail below is the data group's
	// *content* not parsing into its typed form.
	var doc document.Document
	doc.Mf.Lds1.Sod = sod
	for dgName, dgHex := range dataGroupsHex {
		dgNumber, _ := dataGroupNumber(dgName)
		dgBytes, _ := hex.DecodeString(dgHex)
		// A parse failure here is left out of doc rather than erroring the
		// whole function — see the doc comment above. checkDataGroupHashes
		// already flagged it in InvalidDataGroups either way.
		_ = doc.NewDG(dgNumber, dgBytes)
	}
	if doc.Mf.Lds1.Dg1 == nil {
		// DG1 was present in the request (checked above) but failed to
		// parse: no MRZ to cross-check a country against, and no
		// country-narrowed pool to build — report the same as any other
		// verification failure PassiveAuth would have caught.
		return result, nil
	}

	// gmrtd's own completeness check: catches EF.SOD's hash list referencing
	// DG14/DG15 that was never submitted at all — the "app stripped DG15 to
	// dodge Active Authentication" case checkDataGroupHashes can't see, since
	// it only validates data groups that ARE present. Run regardless of
	// PassiveAuth's own outcome below, since an incomplete submission is a
	// failure on its own terms.
	if verifyErr := doc.Verify(); verifyErr != nil {
		result.DocumentVerifyErr = verifyErr.Error()
	} else {
		result.DocumentComplete = true
	}

	paResult, err := passiveauth.PassiveAuth(&doc, trustedCerts)
	if err != nil || paResult == nil || !paResult.Success || paResult.Sod == nil {
		// PassiveAuth ran the check and found something wrong (country
		// mismatch, no CSCA for that country, a hash mismatch, or a bad
		// signature) — the "verification ran and failed" case, reported via
		// zero-value fields with a nil error, not a request error. See the
		// doc comment above for why this can't distinguish which of those it
		// was.
		return result, nil
	}
	result.SODSignatureValid = true
	result.CSCATrustChainValid = true
	result.IssuingCSCA = issuingCSCASubject(paResult.Sod.CertChain)
	return result, nil
}

// VerifyActive checks the Active/Chip Authentication challenge-response.
// aaKeyDGHex is DG15's (or DG13's) raw bytes. Any of aaKeyDGHex/nonceHex/
// signatureHex empty means AA wasn't attempted (Attempted: false, nil
// error) rather than an error. A non-nil error means verification couldn't
// be attempted at all (malformed input); a checked-but-failed signature is
// Passed=false with a nil error.
func VerifyActive(aaKeyDGHex, nonceHex, signatureHex string) (ActiveResult, error) {
	var result ActiveResult
	if aaKeyDGHex == "" || nonceHex == "" || signatureHex == "" {
		return result, nil
	}
	result.Attempted = true

	keyBytes, err := hex.DecodeString(aaKeyDGHex)
	if err != nil {
		return result, fmt.Errorf("mrtdverify: invalid AA key data group hex: %w", err)
	}
	dg15, err := document.NewDG15(keyBytes)
	if err != nil || dg15 == nil {
		return result, fmt.Errorf("mrtdverify: failed to parse AA public key data group: %w", err)
	}

	nonce, err := hex.DecodeString(nonceHex)
	if err != nil {
		return result, fmt.Errorf("mrtdverify: invalid nonce hex: %w", err)
	}
	signature, err := hex.DecodeString(signatureHex)
	if err != nil {
		return result, fmt.Errorf("mrtdverify: invalid AA signature hex: %w", err)
	}

	res, err := activeauth.ValidateActiveAuthSignature(dg15, signature, nonce)
	if err != nil || res == nil || !res.Success {
		// The signature was checked and didn't verify — legitimate result,
		// not a request error.
		return result, nil
	}
	result.Passed = true
	return result, nil
}

// parseSOD hex-decodes and parses efSODHex into a *document.SOD, or returns
// a hard (malformed-input) error. Shared by VerifyPassive and
// VerifyPassiveICAO.
func parseSOD(efSODHex string) (*document.SOD, error) {
	sodBytes, err := hex.DecodeString(efSODHex)
	if err != nil || len(sodBytes) == 0 {
		return nil, fmt.Errorf("mrtdverify: invalid EF.SOD hex: %w", err)
	}
	sod, err := document.NewSOD(sodBytes)
	if err != nil || sod == nil || sod.LdsSecurityObject == nil {
		return nil, fmt.Errorf("mrtdverify: failed to parse EF.SOD: %w", err)
	}
	return sod, nil
}

// checkDataGroupHashes hashes every entry in dataGroupsHex with the
// algorithm EF.SOD declares and compares against EF.SOD's signed hash list,
// returning the (sorted) names of any that don't match — including ones not
// listed in EF.SOD at all, i.e. present on the chip but never covered by the
// signed hash list: data injection, not just a mismatch. Shared by
// VerifyPassive and VerifyPassiveICAO so both report the same granular
// diagnostic regardless of which one computes
// SODSignatureValid/CSCATrustChainValid.
func checkDataGroupHashes(sod *document.SOD, dataGroupsHex map[string]string) ([]string, error) {
	hashAlg := sod.LdsSecurityObject.HashAlgorithm.Algorithm

	var invalid []string
	for dgName, dgHex := range dataGroupsHex {
		dgNumber, err := dataGroupNumber(dgName)
		if err != nil {
			return nil, fmt.Errorf("mrtdverify: %w", err)
		}
		dgBytes, err := hex.DecodeString(dgHex)
		if err != nil {
			return nil, fmt.Errorf("mrtdverify: invalid %s hex: %w", dgName, err)
		}

		expected := sod.DgHash(dgNumber)
		if len(expected) == 0 {
			invalid = append(invalid, dgName)
			continue
		}

		actual, err := cryptoutils.CryptoHashByOid(hashAlg, dgBytes)
		if err != nil {
			return nil, fmt.Errorf("mrtdverify: hashing %s: %w", dgName, err)
		}
		if !bytes.Equal(actual, expected) {
			invalid = append(invalid, dgName)
		}
	}
	sort.Strings(invalid)
	return invalid, nil
}

// issuingCSCASubject best-effort extracts the trusted CSCA certificate's
// (the chain's root) subject for display; empty if certChain is empty or
// the certificate can't be parsed.
func issuingCSCASubject(certChain [][]byte) string {
	if len(certChain) == 0 {
		return ""
	}
	cert, err := x509.ParseCertificate(certChain[len(certChain)-1])
	if err != nil {
		return ""
	}
	return cert.Subject.String()
}

func dataGroupNumber(dgName string) (int, error) {
	n, ok := strings.CutPrefix(dgName, "DG")
	if !ok {
		return 0, fmt.Errorf("invalid data group name %q (want \"DG<number>\")", dgName)
	}
	num, err := strconv.Atoi(n)
	if err != nil || num < 1 || num > 16 {
		return 0, fmt.Errorf("invalid data group name %q (want \"DG<number>\")", dgName)
	}
	return num, nil
}

// passportCertPoolOnce/-Pool/-Err memoize the embedded ICAO CSCA masterlist
// gmrtd bundles (cms.DefaultMasterList) — loading it does non-trivial ASN.1
// parsing, and it's the same trust anchor for every request.
var (
	passportCertPoolOnce sync.Once
	passportCertPool     cms.CertPool
	passportCertPoolErr  error
)

// PassportCertPool returns the embedded ICAO CSCA masterlist gmrtd bundles,
// used as the trust anchor for passport/ID-card Passive Authentication.
func PassportCertPool() (cms.CertPool, error) {
	passportCertPoolOnce.Do(func() {
		passportCertPool, passportCertPoolErr = cms.DefaultMasterList()
	})
	return passportCertPool, passportCertPoolErr
}

// CertPoolFor returns the trust anchor to use for Passive Authentication,
// selected by which data group carries the Active Authentication public key
// (see mrtdEvidenceRequest.AAKeyDataGroup in the api package): "DG13" (EU
// driving licences) gets DrivingLicenceCertPool; anything else, including
// empty (no AA key at all), gets the passport/ID-card masterlist — DG13 is
// the only data group EU driving licences and passports don't share.
func CertPoolFor(aaKeyDataGroup string) (cms.CertPool, error) {
	if aaKeyDataGroup == "DG13" {
		return DrivingLicenceCertPool()
	}
	return PassportCertPool()
}

// DrivingLicenceCertPool is the trust anchor for EU driving-licence Passive
// Authentication. PassportCertPool works because gmrtd itself bundles the
// ICAO CSCA masterlist (Germany/Netherlands/Indonesia, checked into gmrtd's
// own cms/csca.go via go:embed) — this project just calls into gmrtd for
// that data. gmrtd bundles nothing equivalent for EU driving licences, and
// there's currently nowhere else this project sources that data from
// either, so there's nothing to load here: this returns an empty pool —
// CSCATrustChainValid always comes back false for driving licences, rather
// than silently skipping the check — until that changes.
func DrivingLicenceCertPool() (cms.CertPool, error) {
	return &cms.GenericCertPool{}, nil
}
