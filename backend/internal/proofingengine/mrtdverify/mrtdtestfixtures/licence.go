package mrtdtestfixtures

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/digitorus/pkcs7"
)

// LicenceChip is an EU driving licence chip read (ISO/IEC 18013-2) whose
// EF.SOD a freshly made document signer signed, under CSCA (DER). No RDW
// test vector exists, so NewLicenceChip builds one per call.
type LicenceChip struct {
	CSCA   []byte
	SODHex string
	DG1Hex string
}

// The holder NewLicenceChip writes into DG1.
const (
	LicenceSurname        = "DE BRUIJN"
	LicenceOtherNames     = "WILLEKE LISELOTTE"
	LicenceDateOfBirth    = "1965-03-10"
	LicencePlaceOfBirth   = "UTRECHT"
	LicenceDocumentNumber = "5012345678"
)

// licenceKeyBits is the test keys' size; licenceValidity how long the
// certificates hold around now.
const (
	licenceKeyBits  = 2048
	licenceValidity = 24 * time.Hour
)

// The ISO/IEC 18013-2 tags NewLicenceChip writes: EF.SOD, DG1, its
// personal-data container and the fields in it.
const (
	tagSOD            = 0x77
	tagDG1            = 0x61
	tagPersonalData   = 0x5F02
	tagIssuingState   = 0x5F03
	tagSurname        = 0x5F04
	tagOtherNames     = 0x5F05
	tagDateOfBirth    = 0x5F06
	tagPlaceOfBirth   = 0x5F07
	tagDateOfExpiry   = 0x5F0B
	tagDocumentNumber = 0x5F0E
)

// The BCD DDMMYYYY dates in DG1: LicenceDateOfBirth, and an expiry years
// ahead.
var (
	bcdDateOfBirth  = []byte{0x10, 0x03, 0x19, 0x65}
	bcdDateOfExpiry = []byte{0x01, 0x01, 0x20, 0x35}
)

// dg1Number is DG1's number in EF.SOD's hash list.
const dg1Number = 1

// oidLDSSecurityObject is EF.SOD's content type (ICAO 9303-10).
var oidLDSSecurityObject = asn1.ObjectIdentifier{2, 23, 136, 1, 1, 1}

// NewLicenceChip makes a licence chip issued by issuingState: DG1 only,
// listed in an EF.SOD with its SHA-256 hash.
func NewLicenceChip(issuingState string) (LicenceChip, error) {
	csca, cscaKey, err := newLicenceCert(nil, nil, &x509.Certificate{
		Subject:               pkix.Name{Country: []string{"NL"}, CommonName: "Test CSCA NL eDL"},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	})
	if err != nil {
		return LicenceChip{}, err
	}
	ds, dsKey, err := newLicenceCert(csca, cscaKey, &x509.Certificate{
		Subject:  pkix.Name{Country: []string{"NL"}, CommonName: "Test Document Signer NL eDL"},
		KeyUsage: x509.KeyUsageDigitalSignature,
	})
	if err != nil {
		return LicenceChip{}, err
	}

	dg1 := licenceDG1(issuingState)
	sod, err := signLicenceSOD(ds, dsKey, dg1)
	if err != nil {
		return LicenceChip{}, err
	}
	return LicenceChip{CSCA: csca.Raw, SODHex: hex.EncodeToString(sod), DG1Hex: hex.EncodeToString(dg1)}, nil
}

// newLicenceCert makes template's certificate with a new key, signed by
// parent's key, or self-signed when parent is nil.
func newLicenceCert(parent *x509.Certificate, parentKey *rsa.PrivateKey, template *x509.Certificate) (*x509.Certificate, *rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, licenceKeyBits)
	if err != nil {
		return nil, nil, fmt.Errorf("generating key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, nil, fmt.Errorf("generating serial: %w", err)
	}
	now := time.Now()
	template.SerialNumber = serial
	template.NotBefore, template.NotAfter = now.Add(-licenceValidity), now.Add(licenceValidity)
	if parent == nil {
		parent, parentKey = template, key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
	if err != nil {
		return nil, nil, fmt.Errorf("creating certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing certificate: %w", err)
	}
	return cert, key, nil
}

// licenceDG1 is a DG1 (tag 61) whose personal data (5F02) holds the
// LicenceSurname holder.
func licenceDG1(issuingState string) []byte {
	personal := concat(
		licenceTLV(tagIssuingState, []byte(issuingState)),
		licenceTLV(tagSurname, []byte(LicenceSurname)),
		licenceTLV(tagOtherNames, []byte(LicenceOtherNames)),
		licenceTLV(tagDateOfBirth, bcdDateOfBirth),
		licenceTLV(tagPlaceOfBirth, []byte(LicencePlaceOfBirth)),
		licenceTLV(tagDateOfExpiry, bcdDateOfExpiry),
		licenceTLV(tagDocumentNumber, []byte(LicenceDocumentNumber)),
	)
	return licenceTLV(tagDG1, licenceTLV(tagPersonalData, personal))
}

// licenceTLV is a BER TLV with a one- or two-byte tag.
func licenceTLV(tag int, value []byte) []byte {
	var out []byte
	if tag > 0xFF {
		out = append(out, byte(tag>>8))
	}
	out = append(out, byte(tag))
	switch n := len(value); {
	case n < 0x80:
		out = append(out, byte(n))
	case n <= 0xFF:
		out = append(out, 0x81, byte(n))
	default:
		out = append(out, 0x82, byte(n>>8), byte(n))
	}
	return append(out, value...)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// signLicenceSOD is EF.SOD (tag 77): the CMS SignedData over an
// LDSSecurityObject listing DG1's hash, signed by ds.
func signLicenceSOD(ds *x509.Certificate, dsKey *rsa.PrivateKey, dg1 []byte) ([]byte, error) {
	type algorithmIdentifier struct {
		Algorithm asn1.ObjectIdentifier
	}
	type dataGroupHash struct {
		Number int
		Hash   []byte
	}
	type ldsSecurityObject struct {
		Version       int
		HashAlgorithm algorithmIdentifier
		Hashes        []dataGroupHash
	}
	hash := sha256.Sum256(dg1)
	lds, err := asn1.Marshal(ldsSecurityObject{
		HashAlgorithm: algorithmIdentifier{pkcs7.OIDDigestAlgorithmSHA256},
		Hashes:        []dataGroupHash{{dg1Number, hash[:]}},
	})
	if err != nil {
		return nil, fmt.Errorf("marshalling LDSSecurityObject: %w", err)
	}
	sd, err := pkcs7.NewSignedData(lds)
	if err != nil {
		return nil, fmt.Errorf("creating SignedData: %w", err)
	}
	sd.SetContentType(oidLDSSecurityObject)
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := sd.AddSigner(ds, dsKey, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, fmt.Errorf("signing EF.SOD: %w", err)
	}
	signed, err := sd.Finish()
	if err != nil {
		return nil, fmt.Errorf("finishing EF.SOD: %w", err)
	}
	return licenceTLV(tagSOD, signed), nil
}
