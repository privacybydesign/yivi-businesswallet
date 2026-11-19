//go:build wsca

package eudiholder

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/privacybydesign/irmago/eudi/storage/db"
	"github.com/privacybydesign/irmago/eudi/storage/db/models"
	"gorm.io/datatypes"
)

// rowStore serves only the two lookups wscaRowSigner performs.
type rowStore struct {
	db.HolderBindingKeyStore
	byDID   map[string]*models.HolderBindingKey
	byThumb map[string]*models.HolderBindingKey
}

func (s rowStore) GetByDidUrl(did string) (*models.HolderBindingKey, error) {
	if row, ok := s.byDID[did]; ok {
		return row, nil
	}
	return nil, errors.New("not found")
}

func (s rowStore) GetByThumbprint(thumb string) (*models.HolderBindingKey, error) {
	if row, ok := s.byThumb[thumb]; ok {
		return row, nil
	}
	return nil, errors.New("not found")
}

// The WSCA key reference comes from the holder-key row the issuance binder wrote,
// looked up by the cnf key's DID URL (fragment-stripped) — never from the WSCA
// key list, whose public_key_hex is not parseable as DER.
func TestWSCARowSignerResolvesReferenceFromRow(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := jwk.Import(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Set(jwk.KeyIDKey, "did:key:zExample#zExample"); err != nil {
		t.Fatal(err)
	}
	store := rowStore{byDID: map[string]*models.HolderBindingKey{
		"did:key:zExample": {PrivateKey: []byte("wsca:key-42"), DidUrl: datatypes.NullString{V: "did:key:zExample", Valid: true}},
	}}
	// A nil inner signer proves the row path never reaches the WSCA.
	ref, err := (&wscaRowSigner{keys: store}).Reference(pub)
	if err != nil {
		t.Fatalf("Reference: %v", err)
	}
	if ref != "key-42" {
		t.Errorf("ref = %q, want key-42", ref)
	}
}
