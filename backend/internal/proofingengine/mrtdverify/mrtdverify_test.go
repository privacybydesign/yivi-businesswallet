package mrtdverify

import (
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/mrtdverify/mrtdtestfixtures"

	"github.com/gmrtd/gmrtd/cms"
	"github.com/gmrtd/gmrtd/tlv"
)

// syntheticDg15FromCscaHex builds a structurally valid DG15/DG13-shaped blob
// (tag 0x6F wrapping a real SubjectPublicKeyInfo) by reusing the fixture
// CSCA certificate's own public key: good enough to exercise
// ValidateActiveAuthSignature's parsing path with a signature that's
// guaranteed not to verify against it.
func syntheticDg15FromCscaHex(t *testing.T) string {
	t.Helper()
	cert, err := x509.ParseCertificate(mrtdtestfixtures.TestCsca)
	if err != nil {
		t.Fatalf("parse mrtdtestfixtures.TestCsca: %v", err)
	}
	node := tlv.NewTlvSimpleNode(tlv.TlvTag(0x6F), cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(node.Encode())
}

func trustedTestCertPool(t *testing.T) cms.CertPool {
	t.Helper()
	var pool cms.GenericCertPool
	if err := pool.Add(mrtdtestfixtures.TestCsca); err != nil {
		t.Fatalf("Add(mrtdtestfixtures.TestCsca): %v", err)
	}
	return &pool
}

func TestVerifyPassiveValid(t *testing.T) {
	result, err := VerifyPassive(mrtdtestfixtures.TestSodHex, map[string]string{
		"DG1": mrtdtestfixtures.TestDg1Hex,
		"DG2": mrtdtestfixtures.Dg2Hex,
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

func TestVerifyPassiveTamperedDG(t *testing.T) {
	// Flip a byte in DG1 so it no longer matches the hash EF.SOD signed:
	// tampering with the data after the chip was signed should be caught,
	// independent of the signature itself still being valid.
	dg1Bytes, err := hex.DecodeString(mrtdtestfixtures.TestDg1Hex)
	if err != nil {
		t.Fatalf("decode mrtdtestfixtures.TestDg1Hex: %v", err)
	}
	dg1Bytes[len(dg1Bytes)-1] ^= 0xFF
	tamperedDg1 := hex.EncodeToString(dg1Bytes)

	result, err := VerifyPassive(mrtdtestfixtures.TestSodHex, map[string]string{
		"DG1": tamperedDg1,
		"DG2": mrtdtestfixtures.Dg2Hex,
	}, trustedTestCertPool(t))
	if err != nil {
		t.Fatalf("VerifyPassive: %v", err)
	}
	if !result.SODSignatureValid {
		t.Error("SOD signature itself should still verify: only DG1's content changed")
	}
	if result.DataGroupHashesValid {
		t.Error("expected DataGroupHashesValid=false for a tampered data group")
	}
	if len(result.InvalidDataGroups) != 1 || result.InvalidDataGroups[0] != "DG1" {
		t.Errorf("expected InvalidDataGroups=[DG1], got %v", result.InvalidDataGroups)
	}
}

func TestVerifyPassiveInjectedDG(t *testing.T) {
	// DG11 was never part of this fixture's signed hash list, so submitting
	// it anyway must be caught as data injection, not silently ignored.
	result, err := VerifyPassive(mrtdtestfixtures.TestSodHex, map[string]string{
		"DG1":  mrtdtestfixtures.TestDg1Hex,
		"DG2":  mrtdtestfixtures.Dg2Hex,
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

func TestVerifyPassiveUntrusted(t *testing.T) {
	// An empty pool (no CSCA at all trusted) must fail closed, not silently
	// skip the signature check.
	result, err := VerifyPassive(mrtdtestfixtures.TestSodHex, map[string]string{
		"DG1": mrtdtestfixtures.TestDg1Hex,
		"DG2": mrtdtestfixtures.Dg2Hex,
	}, &cms.GenericCertPool{})
	if err != nil {
		t.Fatalf("VerifyPassive: %v", err)
	}
	if result.SODSignatureValid || result.CSCATrustChainValid {
		t.Error("expected signature/trust-chain verification to fail against an untrusted (empty) cert pool")
	}
}

func TestVerifyPassiveMalformed(t *testing.T) {
	pool := trustedTestCertPool(t)

	if _, err := VerifyPassive("not hex", map[string]string{}, pool); err == nil {
		t.Error("expected an error for invalid EF.SOD hex")
	}
	if _, err := VerifyPassive(mrtdtestfixtures.TestSodHex, map[string]string{"DGx": "aabb"}, pool); err == nil {
		t.Error("expected an error for an invalid data group name")
	}
	if _, err := VerifyPassive(mrtdtestfixtures.TestSodHex, map[string]string{"DG1": "not hex"}, pool); err == nil {
		t.Error("expected an error for invalid data group hex")
	}
}

func TestVerifyActiveNoKey(t *testing.T) {
	result, err := VerifyActive("", "", "")
	if err != nil {
		t.Fatalf("VerifyActive: %v", err)
	}
	if result.Attempted || result.Passed {
		t.Errorf("expected Attempted=false, Passed=false when no AA key was supplied, got %+v", result)
	}
}

func TestVerifyActiveNoNonceOrSig(t *testing.T) {
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

func TestVerifyActiveMalformedKey(t *testing.T) {
	if _, err := VerifyActive("not hex", "aabb", "ccdd"); err == nil {
		t.Error("expected an error for invalid AA key hex")
	}
	// Well-formed hex, but not a valid tag-0x6F-wrapped SubjectPublicKeyInfo.
	if _, err := VerifyActive("aabbcc", "aabb", "ccdd"); err == nil {
		t.Error("expected an error for an unparseable AA key data group")
	}
}

func TestVerifyActiveWrongSig(t *testing.T) {
	// A structurally valid DG15 (tag 0x6F wrapping a real SubjectPublicKeyInfo,
	// reusing the fixture CSCA's own SPKI as a stand-in RSA key) but a
	// signature that cannot possibly be a valid AA response: this must be
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

// A licence CSCA loads from PEM (generation 01) and from DER (02 and 03), as
// go-passport-issuer-ops ships them.
func TestLoadCertPool(t *testing.T) {
	dir := t.TempDir()
	pemPath, derPath := filepath.Join(dir, "gen1.cer"), filepath.Join(dir, "gen2.cer")
	if err := os.WriteFile(pemPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: mrtdtestfixtures.TestCsca}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(derPath, mrtdtestfixtures.TestCsca, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{pemPath, derPath} {
		pool, err := LoadCertPool([]string{path})
		if err != nil {
			t.Fatalf("LoadCertPool(%s): %v", path, err)
		}
		if got := len(pool.All()); got != 1 {
			t.Errorf("LoadCertPool(%s) holds %d certificates, want 1", path, got)
		}
	}
	if _, err := LoadCertPool([]string{filepath.Join(dir, "missing.cer")}); err == nil {
		t.Error("LoadCertPool of a missing file: want an error")
	}
}

// A licence's DG1 is mandatory, and leaving out the DG13 its EF.SOD lists
// (to dodge Active Authentication) makes the document incomplete.
func TestPassiveDrivingLicence(t *testing.T) {
	sent := map[string]string{"DG1": mrtdtestfixtures.TestDg1Hex, "DG2": mrtdtestfixtures.Dg2Hex}
	result, err := VerifyPassiveDrivingLicence(mrtdtestfixtures.TestSodHex, sent, trustedTestCertPool(t))
	if err != nil {
		t.Fatalf("VerifyPassiveDrivingLicence: %v", err)
	}
	if !result.SODSignatureValid || !result.CSCATrustChainValid || !result.DataGroupHashesValid || !result.DocumentComplete {
		t.Errorf("result = %+v, want every check to hold", result)
	}

	for _, tc := range []struct {
		name  string
		extra []int
		want  bool
	}{
		{"SOD lists DG13", []int{drivingLicenceAAKeyNumber}, false},
		{"SOD lists no DG13", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := VerifyPassiveDrivingLicence(unsignedSODHex(t, tc.extra...), sent, trustedTestCertPool(t))
			if err != nil {
				t.Fatalf("VerifyPassiveDrivingLicence: %v", err)
			}
			if result.DocumentComplete != tc.want {
				t.Errorf("DocumentComplete = %v, want %v (DocumentVerifyErr=%q)", result.DocumentComplete, tc.want, result.DocumentVerifyErr)
			}
		})
	}

	if _, err := VerifyPassiveDrivingLicence(mrtdtestfixtures.TestSodHex, map[string]string{"DG2": mrtdtestfixtures.Dg2Hex}, trustedTestCertPool(t)); err == nil {
		t.Error("VerifyPassiveDrivingLicence without DG1: want an error")
	}
}

// Without configured CSCAs no licence verifies.
func TestEmptyPoolRefusesLicence(t *testing.T) {
	pool, err := LoadCertPool(nil)
	if err != nil {
		t.Fatalf("LoadCertPool: %v", err)
	}
	result, err := VerifyPassiveDrivingLicence(mrtdtestfixtures.TestSodHex, map[string]string{"DG1": mrtdtestfixtures.TestDg1Hex}, pool)
	if err != nil {
		t.Fatalf("VerifyPassiveDrivingLicence: %v", err)
	}
	if result.CSCATrustChainValid {
		t.Errorf("result = %+v, want the signer refused", result)
	}
}
