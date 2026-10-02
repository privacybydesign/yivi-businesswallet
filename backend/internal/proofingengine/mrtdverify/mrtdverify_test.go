package mrtdverify

import (
	"crypto/x509"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/gmrtd/gmrtd/cms"
	"github.com/gmrtd/gmrtd/tlv"
)

// syntheticDg15FromCscaHex builds a structurally valid DG15/DG13-shaped blob
// (tag 0x6F wrapping a real SubjectPublicKeyInfo) by reusing the fixture
// CSCA certificate's own public key — good enough to exercise
// ValidateActiveAuthSignature's parsing path with a signature that's
// guaranteed not to verify against it.
func syntheticDg15FromCscaHex(t *testing.T) string {
	t.Helper()
	cert, err := x509.ParseCertificate(TestCsca)
	if err != nil {
		t.Fatalf("parse TestCsca: %v", err)
	}
	node := tlv.NewTlvSimpleNode(tlv.TlvTag(0x6F), cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(node.Encode())
}

func trustedTestCertPool(t *testing.T) cms.CertPool {
	t.Helper()
	var pool cms.GenericCertPool
	if err := pool.Add(TestCsca); err != nil {
		t.Fatalf("Add(TestCsca): %v", err)
	}
	return &pool
}

func TestVerifyPassive_validDocumentPasses(t *testing.T) {
	result, err := VerifyPassive(TestSodHex, map[string]string{
		"DG1": TestDg1Hex,
		"DG2": Dg2Hex,
	}, trustedTestCertPool(t))
	if err != nil {
		t.Fatalf("VerifyPassive: %v", err)
	}
	if !result.SODSignatureValid {
		t.Error("expected SODSignatureValid")
	}
	if !result.CSCATrustChainValid {
		t.Error("expected CSCATrustChainValid")
	}
	if !result.DataGroupHashesValid {
		t.Errorf("expected DataGroupHashesValid, got InvalidDataGroups=%v", result.InvalidDataGroups)
	}
	if len(result.InvalidDataGroups) != 0 {
		t.Errorf("expected no invalid data groups, got %v", result.InvalidDataGroups)
	}
	if result.IssuingCSCA == "" {
		t.Error("expected a non-empty IssuingCSCA")
	}
}

func TestVerifyPassive_tamperedDataGroupFailsHashCheckButStillVerifiesSignature(t *testing.T) {
	// Flip a byte in DG1 so it no longer matches the hash EF.SOD signed —
	// tampering with the data after the chip was signed should be caught,
	// independent of the signature itself still being valid.
	dg1Bytes, err := hex.DecodeString(TestDg1Hex)
	if err != nil {
		t.Fatalf("decode TestDg1Hex: %v", err)
	}
	dg1Bytes[len(dg1Bytes)-1] ^= 0xFF
	tamperedDg1 := hex.EncodeToString(dg1Bytes)

	result, err := VerifyPassive(TestSodHex, map[string]string{
		"DG1": tamperedDg1,
		"DG2": Dg2Hex,
	}, trustedTestCertPool(t))
	if err != nil {
		t.Fatalf("VerifyPassive: %v", err)
	}
	if !result.SODSignatureValid {
		t.Error("SOD signature itself should still verify — only DG1's content changed")
	}
	if result.DataGroupHashesValid {
		t.Error("expected DataGroupHashesValid=false for a tampered data group")
	}
	if len(result.InvalidDataGroups) != 1 || result.InvalidDataGroups[0] != "DG1" {
		t.Errorf("expected InvalidDataGroups=[DG1], got %v", result.InvalidDataGroups)
	}
}

func TestVerifyPassive_injectedDataGroupNotInSodFailsHashCheck(t *testing.T) {
	// DG11 was never part of this fixture's signed hash list — submitting
	// it anyway must be caught as data injection, not silently ignored.
	result, err := VerifyPassive(TestSodHex, map[string]string{
		"DG1":  TestDg1Hex,
		"DG2":  Dg2Hex,
		"DG11": "aabbcc",
	}, trustedTestCertPool(t))
	if err != nil {
		t.Fatalf("VerifyPassive: %v", err)
	}
	if result.DataGroupHashesValid {
		t.Error("expected DataGroupHashesValid=false for an injected data group not covered by EF.SOD")
	}
	if len(result.InvalidDataGroups) != 1 || result.InvalidDataGroups[0] != "DG11" {
		t.Errorf("expected InvalidDataGroups=[DG11], got %v", result.InvalidDataGroups)
	}
}

func TestVerifyPassive_untrustedCertPoolFailsSignatureCheck(t *testing.T) {
	// An empty pool (no CSCA at all trusted) must fail closed, not silently
	// skip the signature check.
	result, err := VerifyPassive(TestSodHex, map[string]string{
		"DG1": TestDg1Hex,
		"DG2": Dg2Hex,
	}, &cms.GenericCertPool{})
	if err != nil {
		t.Fatalf("VerifyPassive: %v", err)
	}
	if result.SODSignatureValid || result.CSCATrustChainValid {
		t.Error("expected signature/trust-chain verification to fail against an untrusted (empty) cert pool")
	}
}

func TestVerifyPassive_malformedInputsAreRequestErrors(t *testing.T) {
	pool := trustedTestCertPool(t)

	if _, err := VerifyPassive("not hex", map[string]string{}, pool); err == nil {
		t.Error("expected an error for invalid EF.SOD hex")
	}
	if _, err := VerifyPassive(TestSodHex, map[string]string{"DGx": "aabb"}, pool); err == nil {
		t.Error("expected an error for an invalid data group name")
	}
	if _, err := VerifyPassive(TestSodHex, map[string]string{"DG1": "not hex"}, pool); err == nil {
		t.Error("expected an error for invalid data group hex")
	}
}

func TestVerifyActive_noKeyMeansNotAttempted(t *testing.T) {
	result, err := VerifyActive("", "", "")
	if err != nil {
		t.Fatalf("VerifyActive: %v", err)
	}
	if result.Attempted || result.Passed {
		t.Errorf("expected Attempted=false, Passed=false when no AA key was supplied, got %+v", result)
	}
}

func TestVerifyActive_keyPresentButMissingNonceOrSignatureIsNotAttempted(t *testing.T) {
	const someKeyHex = "6f050101010101" // arbitrary non-empty bytes; returns before parsing

	result, err := VerifyActive(someKeyHex, "", "aabb")
	if err != nil {
		t.Fatalf("VerifyActive: %v", err)
	}
	if result.Attempted || result.Passed {
		t.Errorf("expected Attempted=false, Passed=false when the nonce is missing, got %+v", result)
	}

	result, err = VerifyActive(someKeyHex, "aabb", "")
	if err != nil {
		t.Fatalf("VerifyActive: %v", err)
	}
	if result.Attempted || result.Passed {
		t.Errorf("expected Attempted=false, Passed=false when the signature is missing, got %+v", result)
	}
}

func TestVerifyActive_malformedKeyIsARequestError(t *testing.T) {
	if _, err := VerifyActive("not hex", "aabb", "ccdd"); err == nil {
		t.Error("expected an error for invalid AA key hex")
	}
	// Well-formed hex, but not a valid tag-0x6F-wrapped SubjectPublicKeyInfo.
	if _, err := VerifyActive("aabbcc", "aabb", "ccdd"); err == nil {
		t.Error("expected an error for an unparseable AA key data group")
	}
}

func TestVerifyActive_wrongSignatureFailsWithoutError(t *testing.T) {
	// A structurally valid DG15 (tag 0x6F wrapping a real SubjectPublicKeyInfo,
	// reusing the fixture CSCA's own SPKI as a stand-in RSA key) but a
	// signature that cannot possibly be a valid AA response — this must be
	// reported as Attempted=true, Passed=false, not a request error.
	dg15Hex := syntheticDg15FromCscaHex(t)

	result, err := VerifyActive(dg15Hex, "0011223344556677", strings.Repeat("00", 128))
	if err != nil {
		t.Fatalf("VerifyActive: %v", err)
	}
	if !result.Attempted {
		t.Error("expected Attempted=true once a key is present")
	}
	if result.Passed {
		t.Error("expected Passed=false for a signature that cannot verify")
	}
}

func TestDrivingLicenceCertPool_isEmpty(t *testing.T) {
	// Unlike PassportCertPool, gmrtd bundles nothing for EU driving licences
	// (see DrivingLicenceCertPool's doc comment) — this stays empty until
	// this project has an actual source of trust-anchor certs to point at.
	// CSCATrustChainValid always comes back false for driving licences as a
	// result, rather than silently skipping the check.
	pool, err := DrivingLicenceCertPool()
	if err != nil {
		t.Fatalf("DrivingLicenceCertPool: %v", err)
	}
	if got := len(pool.All()); got != 0 {
		t.Errorf("expected an empty pool, got %d certs", got)
	}
}
