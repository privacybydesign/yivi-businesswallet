// Package mrtdverify runs ICAO 9303 chip checks server side, with gmrtd (as
// go-passport-issuer does): Passive Authentication (EF.SOD's signature against a
// CSCA anchor, then every data group's hash against its signed list) and
// Active Authentication (the chip's signed challenge against the key in DG15,
// or DG13 for a driving licence).
//
// Two Passive Authentication entry points:
//
//   - VerifyPassive is generic: it checks whatever data groups were sent
//     against the whole trust pool. VerifyPassiveDrivingLicence builds on it
//     for EU driving licences, whose DG1/DG6/DG13 encodings gmrtd's typed
//     parsers cannot read, as go-passport-issuer's PassiveAuthenticationEDL
//     does.
//   - VerifyPassiveICAO is for passports and ID cards: gmrtd's
//     passiveauth.PassiveAuth on a typed document, which also checks the
//     signer's country against DG1's and narrows the pool to that country's
//     CSCAs, as go-passport-issuer's PassiveAuthenticationPassport does.
package mrtdverify

import (
	"bytes"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
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

// PassiveResult is Passive Authentication's outcome.
type PassiveResult struct {
	SODSignatureValid   bool
	CSCATrustChainValid bool
	// IssuingCSCA is the trusted CSCA's subject, best effort: empty when it could
	// not be read, even after a successful verification.
	IssuingCSCA string
	// DataGroupHashesValid is false when any submitted data group's hash differs
	// from EF.SOD's, or EF.SOD does not list it (an injected group).
	DataGroupHashesValid bool
	// InvalidDataGroups are the groups that failed, sorted.
	InvalidDataGroups []string
	// DocumentComplete is false when a data group EF.SOD lists is missing that
	// Active Authentication needs: the hash check only sees what was sent, so
	// dropping the key would otherwise dodge it. VerifyPassiveICAO asks gmrtd's
	// Document.Verify (DG14 or DG15 above all), VerifyPassiveDrivingLicence
	// checks DG13. Always true for VerifyPassive.
	DocumentComplete bool
	// DocumentVerifyErr is Document.Verify's error, for diagnostics.
	DocumentVerifyErr string
}

// ActiveResult: whether the app engaged Active/Chip Authentication (sent a
// nonce/signature, not just a key) and whether it verified.
type ActiveResult struct {
	Attempted bool
	Passed    bool
}

// VerifyPassive checks the hex EF.SOD against trustedCerts and every data group
// ("DG1".."DG16", hex, raw as read) against its hashes. An error is malformed
// input, a bad request; a check that ran and failed is in the result.
func VerifyPassive(efSODHex string, dataGroupsHex map[string]string, trustedCerts cms.CertPool) (PassiveResult, error) {
	// No typed document here, so no completeness check.
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
		// The chain did not verify: a result, not an error.
		return result, nil
	}
	result.SODSignatureValid = true
	result.CSCATrustChainValid = true
	result.IssuingCSCA = issuingCSCASubject(certChain)
	return result, nil
}

// VerifyPassiveDrivingLicence is VerifyPassive for an EU driving licence
// (ISO/IEC 18013-3), against LoadCertPool's pool. DG1 must be sent, a
// missing one is a bad request; and a DG13 (the Active Authentication key)
// that EF.SOD lists but was not sent makes the document incomplete, as
// go-passport-issuer's ActiveAuthenticationEDL refuses it.
func VerifyPassiveDrivingLicence(efSODHex string, dataGroupsHex map[string]string, trustedCerts cms.CertPool) (PassiveResult, error) {
	if _, ok := dataGroupsHex[drivingLicenceDG1]; !ok {
		return PassiveResult{}, fmt.Errorf("mrtdverify: %s is mandatory for driving licence passive authentication but was not provided", drivingLicenceDG1)
	}

	result, err := VerifyPassive(efSODHex, dataGroupsHex, trustedCerts)
	if err != nil {
		return result, err
	}

	// VerifyPassive parsed the same EF.SOD without error.
	sod, err := parseSOD(efSODHex)
	if err != nil {
		return result, err
	}

	if _, sent := dataGroupsHex[drivingLicenceAAKey]; !sent && sod.HasDgHash(drivingLicenceAAKeyNumber) {
		result.DocumentComplete = false
		result.DocumentVerifyErr = fmt.Sprintf("EF.SOD lists %s (the Active Authentication key) but it was not provided", drivingLicenceAAKey)
	}
	return result, nil
}

// The driving licence data groups VerifyPassiveDrivingLicence requires.
const (
	drivingLicenceDG1         = "DG1"
	drivingLicenceAAKey       = "DG13"
	drivingLicenceAAKeyNumber = 13
)

// VerifyPassiveICAO is VerifyPassive for passports and ID cards, with the
// signature checked by gmrtd's passiveauth.PassiveAuth on a typed document
// (the country cross-check). PassiveAuth's error does not say which check
// failed, and a bad hash also fails its signature check, so the hash result
// comes from checkDataGroupHashes as in VerifyPassive.
//
// DG1 and DG2 must be sent: a missing one is a bad request. A group that is
// sent but does not parse (a tampered MRZ) is a failed verification, not an
// error.
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

	// The names and hex are already validated, so a parse failure is the content's.
	var doc document.Document
	doc.Mf.Lds1.Sod = sod
	for dgName, dgHex := range dataGroupsHex {
		dgNumber, _ := dataGroupNumber(dgName)
		dgBytes, _ := hex.DecodeString(dgHex)
		// A group that does not parse stays out of doc; it is already in
		// InvalidDataGroups.
		_ = doc.NewDG(dgNumber, dgBytes)
	}
	if doc.Mf.Lds1.Dg1 == nil {
		// DG1 does not parse: no country to check, so the verification fails.
		return result, nil
	}

	// gmrtd's completeness check (see PassiveResult.DocumentComplete), whatever
	// PassiveAuth finds.
	if verifyErr := doc.Verify(); verifyErr != nil {
		result.DocumentVerifyErr = verifyErr.Error()
	} else {
		result.DocumentComplete = true
	}

	paResult, err := passiveauth.PassiveAuth(&doc, trustedCerts)
	if err != nil || paResult == nil || !paResult.Success || paResult.Sod == nil {
		// PassiveAuth found something wrong (country, CSCA, hash or signature): a
		// failed verification.
		return result, nil
	}
	result.SODSignatureValid = true
	result.CSCATrustChainValid = true
	result.IssuingCSCA = issuingCSCASubject(paResult.Sod.CertChain)
	return result, nil
}

// VerifyActive checks the Active/Chip Authentication response against the key
// in aaKeyDGHex (DG15, or DG13). With any input empty AA was not attempted. An
// error is malformed input; a signature that does not verify is Passed false.
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
	// A licence's DG13 shares DG15's 0x6F tag (ISO/IEC 18013-3), so one parser reads both.
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
		return result, nil
	}
	result.Passed = true
	return result, nil
}

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

// checkDataGroupHashes hashes every data group with EF.SOD's algorithm and
// returns those whose hash differs or that EF.SOD does not list, sorted.
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

// issuingCSCASubject is the chain root's subject, "" when unreadable.
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

// The CSCAs gmrtd embeds (cms.DefaultMasterList) are parsed once: they are
// the same anchor for every request.
var (
	passportCertPoolOnce sync.Once
	passportCertPool     cms.CertPool
	passportCertPoolErr  error
)

// PassportCertPool returns gmrtd's embedded German and Dutch CSCA master lists
// plus Indonesia's 2010-series CSCAs, used as the trust anchor for
// passport/ID-card Passive Authentication. Only a gmrtd bump refreshes them.
func PassportCertPool() (cms.CertPool, error) {
	passportCertPoolOnce.Do(func() {
		passportCertPool, passportCertPoolErr = cms.DefaultMasterList()
	})
	return passportCertPool, passportCertPoolErr
}

// DrivingLicenceIssuingState is the only issuing member state whose
// driving-licence CSCAs LoadCertPool is given: a licence from any
// other state cannot verify.
const DrivingLicenceIssuingState = "NLD"

// LoadCertPool is the EU driving-licence anchor: gmrtd bundles a masterlist
// for passports but none for licences, so it is the RDW's "CSCA NL eDL" roots
// at paths, every generation, as go-passport-issuer-ops deploys them. A
// certificate is PEM (generation 01) or DER (02 and 03). No paths is an empty
// pool: a licence's CSCATrustChainValid is then false.
func LoadCertPool(paths []string) (cms.CertPool, error) {
	pool := &cms.GenericCertPool{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("mrtdverify: reading %s: %w", path, err)
		}
		if block, _ := pem.Decode(data); block != nil {
			data = block.Bytes
		}
		if err := pool.Add(data); err != nil {
			return nil, fmt.Errorf("mrtdverify: adding %s: %w", path, err)
		}
	}
	return pool, nil
}
