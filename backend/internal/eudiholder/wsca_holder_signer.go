//go:build wsca

package eudiholder

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jws"
	"github.com/privacybydesign/irmago/eudi/credentials/proofs"
	"github.com/privacybydesign/irmago/eudi/holderkeys"
	"github.com/privacybydesign/irmago/eudi/openid4vci"
	"github.com/privacybydesign/irmago/eudi/storage/db"
	"github.com/privacybydesign/irmago/eudi/storage/db/models"
	"github.com/privacybydesign/wallet-provider/mobile/walletmobile"
	"gorm.io/datatypes"
)

// Ported from wallet-provider's mobile/walletmobile/irmabinding, which is built
// against jwx v3 and no longer exists upstream; irmago's holderkeys needs jwx v4.

const (
	wscaKeyIDBytes     = 16
	es256CoordinateLen = 32
)

// wscaHolderSigner is irmago's HolderSigner over an activated walletmobile wallet;
// the holder private keys never leave the WSCA/HSM.
type wscaHolderSigner struct {
	w   *walletmobile.Wallet
	pin func() (string, error)
}

func (s *wscaHolderSigner) GenerateKeys(num uint) ([]string, []*ecdsa.PublicKey, error) {
	pin, err := s.pin()
	if err != nil {
		return nil, nil, fmt.Errorf("wsca: obtain PIN: %w", err)
	}
	refs := make([]string, 0, num)
	pubs := make([]*ecdsa.PublicKey, 0, num)
	for range num {
		keyID, err := randomWSCAKeyID()
		if err != nil {
			return nil, nil, err
		}
		res, err := s.w.GenerateKey(keyID, pin)
		if err != nil {
			return nil, nil, fmt.Errorf("wsca: GenerateKey: %w", err)
		}
		pubHex, err := wscaResultField(res, "public_key_hex")
		if err != nil {
			return nil, nil, err
		}
		pub, err := wscaPublicKeyFromHex(pubHex)
		if err != nil {
			return nil, nil, err
		}
		refs = append(refs, keyID)
		pubs = append(pubs, pub)
	}
	return refs, pubs, nil
}

// SignES256 returns the raw r||s signature; the WSCA hashes the input with SHA-256.
func (s *wscaHolderSigner) SignES256(ref string, signingInput []byte) ([]byte, error) {
	pin, err := s.pin()
	if err != nil {
		return nil, fmt.Errorf("wsca: obtain PIN: %w", err)
	}
	res, err := s.w.Sign(ref, string(signingInput), pin)
	if err != nil {
		return nil, fmt.Errorf("wsca: Sign: %w", err)
	}
	derHex, err := wscaResultField(res, "signature_hex")
	if err != nil {
		return nil, err
	}
	der, err := hex.DecodeString(derHex)
	if err != nil {
		return nil, fmt.Errorf("wsca: decode signature: %w", err)
	}
	return derToRawES256(der)
}

// Reference matches pub by JWK thumbprint against the WSCA's key list.
func (s *wscaHolderSigner) Reference(pub jwk.Key) (string, error) {
	want, err := jwkThumbprintHex(pub)
	if err != nil {
		return "", err
	}
	listJSON, err := s.w.ListKeys()
	if err != nil {
		return "", fmt.Errorf("wsca: ListKeys: %w", err)
	}
	var keys []struct {
		KeyID        string `json:"key_id"`
		PublicKeyHex string `json:"public_key_hex"`
	}
	if err := json.Unmarshal([]byte(listJSON), &keys); err != nil {
		return "", fmt.Errorf("wsca: parse key list: %w", err)
	}
	for _, k := range keys {
		ecPub, err := wscaPublicKeyFromHex(k.PublicKeyHex)
		if err != nil {
			continue
		}
		tp, err := ecdsaThumbprintHex(ecPub)
		if err == nil && tp == want {
			return k.KeyID, nil
		}
	}
	return "", errors.New("wsca: no WSCA key matches the requested public key")
}

func (s *wscaHolderSigner) Remove(refs []string) error {
	pin, err := s.pin()
	if err != nil {
		return fmt.Errorf("wsca: obtain PIN: %w", err)
	}
	for _, ref := range refs {
		if _, err := s.w.RemoveKey(ref, pin); err != nil {
			return fmt.Errorf("wsca: RemoveKey: %w", err)
		}
	}
	return nil
}

// wscaIssuanceBinder is irmago's HolderKeyBinder: WSCA keys sign the OpenID4VCI
// proofs, and a holder-key row referencing each WSCA key links it to the cnf.
type wscaIssuanceBinder struct {
	signer *wscaHolderSigner
	store  db.HolderBindingKeyStore
}

func (b *wscaIssuanceBinder) CreateKeyPairsWithProofs(num uint, proofBuilder proofs.ProofBuilder) ([]models.PublicHolderBindingKey, []string, error) {
	jpb, ok := proofBuilder.(*proofs.JwtProofBuilder)
	if !ok {
		return nil, nil, fmt.Errorf("wsca: issuance requires *proofs.JwtProofBuilder, got %T", proofBuilder)
	}
	refs, pubs, err := b.signer.GenerateKeys(num)
	if err != nil {
		return nil, nil, err
	}
	proofsOut := make([]string, num)
	rows := make([]models.HolderBindingKey, num)
	for i := range refs {
		keyID := refs[i]
		proof, err := jpb.BuildWithES256Signer(pubs[i], func(signingInput []byte) ([]byte, error) {
			return b.signer.SignES256(keyID, signingInput)
		})
		if err != nil {
			return nil, nil, fmt.Errorf("wsca: build proof: %w", err)
		}
		proofsOut[i] = proof
		if rows[i], err = wscaBindingKeyRow(keyID, pubs[i], proof); err != nil {
			return nil, nil, err
		}
	}
	if err := b.store.StoreKeys(rows); err != nil {
		return nil, nil, fmt.Errorf("wsca: store holder-key rows: %w", err)
	}
	ids := make([]models.PublicHolderBindingKey, num)
	for i := range rows {
		ids[i] = models.PublicHolderBindingKey{ID: rows[i].ID}
		if rows[i].DidUrl.Valid {
			ids[i].DidUrl = &rows[i].DidUrl.V
		}
		if rows[i].PublicKeyThumbprint.Valid {
			ids[i].PublicKeyThumbprint = &rows[i].PublicKeyThumbprint.V
		}
	}
	return ids, proofsOut, nil
}

func (b *wscaIssuanceBinder) RemoveKeys(ids []datatypes.UUID) error {
	for _, id := range ids {
		if err := b.store.DeleteKey(id); err != nil {
			return fmt.Errorf("wsca: delete holder-key row: %w", err)
		}
	}
	return nil
}

// wscaBindingKeyRow stores the WSCA key id in place of a private key, looked up
// by the proof's DID URL or else the key thumbprint, as irmago's software binder does.
func wscaBindingKeyRow(keyID string, pub *ecdsa.PublicKey, proof string) (models.HolderBindingKey, error) {
	row := models.HolderBindingKey{
		Algorithm:  models.KeyAlgorithmECDSA,
		PrivateKey: []byte(wscaRefPrefix + keyID),
		ECDSA:      &models.ECDSAKeyMetadata{CurveName: "P-256"},
	}
	if did, ok := didURLFromProof(proof); ok {
		row.DidUrl = datatypes.NullString{V: did, Valid: true}
		return row, nil
	}
	tp, err := ecdsaThumbprintHex(pub)
	if err != nil {
		return models.HolderBindingKey{}, err
	}
	row.PublicKeyThumbprint = datatypes.NullString{V: tp, Valid: true}
	return row, nil
}

func didURLFromProof(proof string) (string, bool) {
	msg, err := jws.Parse([]byte(proof))
	if err != nil {
		return "", false
	}
	sigs := msg.Signatures()
	if len(sigs) == 0 {
		return "", false
	}
	kid, ok := sigs[0].ProtectedHeaders().KeyID()
	if !ok || !strings.HasPrefix(kid, "did:") {
		return "", false
	}
	return kid, true
}

func randomWSCAKeyID() (string, error) {
	buf := make([]byte, wscaKeyIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("wsca: generate key id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// wscaResultField reads "name=value" from a WSCA result body of such lines.
func wscaResultField(result, name string) (string, error) {
	for line := range strings.SplitSeq(result, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), name+"="); ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("wsca: field %q not in WSCA result", name)
}

func wscaPublicKeyFromHex(h string) (*ecdsa.PublicKey, error) {
	der, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("wsca: decode public key: %w", err)
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("wsca: parse SPKI public key: %w", err)
	}
	ecPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("wsca: WSCA key is not ECDSA (%T)", pub)
	}
	return ecPub, nil
}

func ecdsaThumbprintHex(pub *ecdsa.PublicKey) (string, error) {
	k, err := jwk.Import[jwk.Key](pub)
	if err != nil {
		return "", fmt.Errorf("wsca: import public key: %w", err)
	}
	return jwkThumbprintHex(k)
}

func jwkThumbprintHex(k jwk.Key) (string, error) {
	tp, err := k.Thumbprint(crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("wsca: key thumbprint: %w", err)
	}
	return hex.EncodeToString(tp), nil
}

func derToRawES256(der []byte) ([]byte, error) {
	var sig struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &sig); err != nil {
		return nil, fmt.Errorf("wsca: parse DER signature: %w", err)
	}
	out := make([]byte, 2*es256CoordinateLen)
	sig.R.FillBytes(out[:es256CoordinateLen])
	sig.S.FillBytes(out[es256CoordinateLen:])
	return out, nil
}

var (
	_ holderkeys.HolderSigner    = (*wscaHolderSigner)(nil)
	_ openid4vci.HolderKeyBinder = (*wscaIssuanceBinder)(nil)
)
