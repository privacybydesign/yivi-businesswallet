package mrtdverify

import (
	"encoding/hex"
	"testing"

	"github.com/gmrtd/gmrtd/cms"
	"github.com/gmrtd/gmrtd/document"
	"github.com/gmrtd/gmrtd/tlv"
)

// countryMismatchDg1Hex rebuilds the fixture's own DG1 with a different
// issuing state ("NLD" instead of the fixture's "GBR"), so the resulting
// document has a validly-signed EF.SOD (signed by TestCsca, a "gb" CSCA) but
// a DG1 that claims a different country — exactly what
// passiveauth.PassiveAuth's country cross-check exists to catch. This is
// buildable at all only because MRZ's issuing-state field carries no check
// digit (see mrz.decodeTD3: documentNumber/dateOfBirth/dateOfExpiry/
// composite are check-digited, issuingState/nationality are not) — swapping
// it doesn't need touching any checksum. This is deliberately a *different*
// scenario from a tampered/injected data group (VerifyPassive's existing
// tests): the resulting DG1 hash no longer matches TestSodHex's signed hash
// for DG1 either, but PassiveAuth's country check runs and fails before it
// would ever reach that hash check (see countryCscaCerts in gmrtd's
// passiveauth package), so this specifically exercises the country
// cross-check path, not the hash-check path.
func countryMismatchDg1Hex(t *testing.T) string {
	t.Helper()
	dg1Bytes, err := hex.DecodeString(TestDg1Hex)
	if err != nil {
		t.Fatalf("decode TestDg1Hex: %v", err)
	}
	dg1, err := document.NewDG1(dg1Bytes)
	if err != nil {
		t.Fatalf("parse TestDg1Hex: %v", err)
	}
	if dg1.Mrz.IssuingState != "GBR" {
		t.Fatalf("fixture DG1 issuing state = %q, want GBR (test assumption changed?)", dg1.Mrz.IssuingState)
	}

	mrz := []byte(dg1.RawMrz)
	copy(mrz[2:5], []byte("NLD")) // TD3 issuing-state field, mrz[2:5] — see decodeTD3.

	inner := tlv.NewTlvSimpleNode(tlv.TlvTag(0x5f1f), mrz)
	root := tlv.NewTlvConstructedNode(tlv.TlvTag(0x61))
	root.AddChild(inner)
	return hex.EncodeToString(root.Encode())
}

// tamperedDg1Hex flips a byte within the fixture DG1's MRZ name field
// (offset 5:44 of the 88-byte TD3 MRZ — see decodeTD3), which carries no
// check digit, so the result still parses as a structurally valid DG1 (just
// with content that no longer matches what EF.SOD signed).
func tamperedDg1Hex(t *testing.T) string {
	t.Helper()
	dg1Bytes, err := hex.DecodeString(TestDg1Hex)
	if err != nil {
		t.Fatalf("decode TestDg1Hex: %v", err)
	}
	dg1, err := document.NewDG1(dg1Bytes)
	if err != nil {
		t.Fatalf("parse TestDg1Hex: %v", err)
	}

	mrz := []byte(dg1.RawMrz)
	mrz[10] ^= 0xFF // within the name field (offset 5:44), no check digit there.

	inner := tlv.NewTlvSimpleNode(tlv.TlvTag(0x5f1f), mrz)
	root := tlv.NewTlvConstructedNode(tlv.TlvTag(0x61))
	root.AddChild(inner)
	return hex.EncodeToString(root.Encode())
}

func TestVerifyPassiveICAO_validDocumentPasses(t *testing.T) {
	result, err := VerifyPassiveICAO(TestSodHex, map[string]string{
		"DG1": TestDg1Hex,
		"DG2": Dg2Hex,
	}, trustedTestCertPool(t))
	if err != nil {
		t.Fatalf("VerifyPassiveICAO: %v", err)
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
	if result.IssuingCSCA == "" {
		t.Error("expected a non-empty IssuingCSCA")
	}
	if !result.DocumentComplete {
		t.Errorf("expected DocumentComplete (this fixture's SOD doesn't reference DG14/DG15), got DocumentVerifyErr=%q", result.DocumentVerifyErr)
	}
}

// NB there's no test here for the DG14/DG15-referenced-but-omitted case
// itself (an app stripping DG15 to dodge Active Authentication) — doing so
// needs a fixture whose EF.SOD genuinely signs a hash list that includes
// DG15, and this package has no CSCA private key to produce one; the fixture
// above (ported from go-passport-issuer) only ever covered DG1/DG2.
// document.Document.Verify's own test suite in gmrtd covers that check
// directly; VerifyPassiveICAO's part is just calling it and recording the
// result, exercised (for the "nothing to flag" case) above.

func TestVerifyPassiveICAO_countryMismatchFailsWithoutError(t *testing.T) {
	// EF.SOD is signed by a "gb" CSCA (TestCsca); this DG1 claims "NLD".
	// PassiveAuth's country cross-check must catch that and fail closed —
	// this is exactly the gap VerifyPassive (used unchanged for EU driving
	// licences) cannot catch.
	result, err := VerifyPassiveICAO(TestSodHex, map[string]string{
		"DG1": countryMismatchDg1Hex(t),
		"DG2": Dg2Hex,
	}, trustedTestCertPool(t))
	if err != nil {
		t.Fatalf("VerifyPassiveICAO: %v (a country mismatch is a legitimate verification failure, not a request error)", err)
	}
	if result.SODSignatureValid || result.CSCATrustChainValid {
		t.Errorf("expected SODSignatureValid=false, CSCATrustChainValid=false for a country mismatch, got %+v", result)
	}
}

func TestVerifyPassiveICAO_untrustedCertPoolFailsSignatureCheck(t *testing.T) {
	result, err := VerifyPassiveICAO(TestSodHex, map[string]string{
		"DG1": TestDg1Hex,
		"DG2": Dg2Hex,
	}, &cms.GenericCertPool{})
	if err != nil {
		t.Fatalf("VerifyPassiveICAO: %v", err)
	}
	if result.SODSignatureValid || result.CSCATrustChainValid {
		t.Error("expected signature/trust-chain verification to fail against an untrusted (empty) cert pool")
	}
}

func TestVerifyPassiveICAO_tamperedDataGroupStillFailsHashCheckDiagnostic(t *testing.T) {
	// Unlike VerifyPassive's own tamper test (which flips the last raw byte
	// of the whole TLV blob — fine there, since VerifyPassive never parses
	// MRZ semantics at all), VerifyPassiveICAO builds a typed document.DG1,
	// which validates MRZ check digits on parse. Flipping a byte inside a
	// check-digited field (document number/DOB/expiry/composite) would fail
	// to parse at all rather than exercise the hash-mismatch path this test
	// wants, so this flips a byte inside the name field (MRZ offset 5:44),
	// which carries no check digit — see decodeTD3.
	tamperedDg1 := tamperedDg1Hex(t)

	result, err := VerifyPassiveICAO(TestSodHex, map[string]string{
		"DG1": tamperedDg1,
		"DG2": Dg2Hex,
	}, trustedTestCertPool(t))
	if err != nil {
		t.Fatalf("VerifyPassiveICAO: %v", err)
	}
	if result.DataGroupHashesValid {
		t.Error("expected DataGroupHashesValid=false for a tampered data group")
	}
	if len(result.InvalidDataGroups) != 1 || result.InvalidDataGroups[0] != "DG1" {
		t.Errorf("expected InvalidDataGroups=[DG1], got %v", result.InvalidDataGroups)
	}
	// Unlike VerifyPassive, PassiveAuth's own internal hash check gates its
	// signature verification, so a tampered data group also fails the
	// signature/trust-chain verdict here — see the VerifyPassiveICAO doc
	// comment for why. This assertion documents that trade-off rather than
	// asserting the (different, and arguably more useful) VerifyPassive
	// behavior.
	if result.SODSignatureValid || result.CSCATrustChainValid {
		t.Error("expected SODSignatureValid=false, CSCATrustChainValid=false too — PassiveAuth's hash check gates its own signature verification")
	}
}

func TestVerifyPassiveICAO_missingMandatoryDataGroupIsARequestError(t *testing.T) {
	pool := trustedTestCertPool(t)

	if _, err := VerifyPassiveICAO(TestSodHex, map[string]string{"DG2": Dg2Hex}, pool); err == nil {
		t.Error("expected an error when DG1 (mandatory) is missing")
	}
	if _, err := VerifyPassiveICAO(TestSodHex, map[string]string{"DG1": TestDg1Hex}, pool); err == nil {
		t.Error("expected an error when DG2 (mandatory) is missing")
	}
}

func TestVerifyPassiveICAO_malformedInputsAreRequestErrors(t *testing.T) {
	pool := trustedTestCertPool(t)

	if _, err := VerifyPassiveICAO("not hex", map[string]string{}, pool); err == nil {
		t.Error("expected an error for invalid EF.SOD hex")
	}
	if _, err := VerifyPassiveICAO(TestSodHex, map[string]string{"DGx": "aabb", "DG1": TestDg1Hex, "DG2": Dg2Hex}, pool); err == nil {
		t.Error("expected an error for an invalid data group name")
	}
	if _, err := VerifyPassiveICAO(TestSodHex, map[string]string{"DG1": "not hex", "DG2": Dg2Hex}, pool); err == nil {
		t.Error("expected an error for invalid data group hex")
	}
}

// TestVerifyPassiveICAO_unparseableDataGroupIsAVerificationFailureNotARequestError
// checks the deliberate split documented on VerifyPassiveICAO: DG1 present
// in dataGroupsHex (structurally valid hex) but not a parseable MRZ is what
// a corrupted/tampered document looks like, not a malformed request — so
// this must come back as a verification failure (nil error), not an error,
// unlike a bad data-group *name* or non-hex value (still hard errors, see
// TestVerifyPassiveICAO_malformedInputsAreRequestErrors).
func TestVerifyPassiveICAO_unparseableDataGroupIsAVerificationFailureNotARequestError(t *testing.T) {
	result, err := VerifyPassiveICAO(TestSodHex, map[string]string{"DG1": "aabbcc", "DG2": Dg2Hex}, trustedTestCertPool(t))
	if err != nil {
		t.Fatalf("VerifyPassiveICAO: %v (an unparseable-but-present data group is a verification failure, not a request error)", err)
	}
	if result.SODSignatureValid || result.CSCATrustChainValid {
		t.Errorf("expected SODSignatureValid=false, CSCATrustChainValid=false when DG1 can't be parsed at all, got %+v", result)
	}
	if len(result.InvalidDataGroups) == 0 {
		t.Error("expected DG1 to be flagged in InvalidDataGroups (its hash can't possibly match either)")
	}
}
