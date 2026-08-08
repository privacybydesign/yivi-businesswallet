package signingprovider

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/url"
	"time"
)

// StubProvider is an in-process QTSP for dev/CI and tests: it holds a generated
// EC P-256 key + self-signed certificate and signs a presented digest with raw
// ECDSA exactly as the real QTSP's signHash does. It needs no network, no
// authorization server and no wallet, so the whole signing ceremony (link a
// credential, then sign) can be driven end to end in a test.
//
// The OAuth legs are canned: AuthorizeURL returns a well-formed URL, and
// ExchangeToken returns a fixed token. A test drives the callback directly,
// standing in for the browser+wallet round-trip the real authorization server runs.
type StubProvider struct {
	key          *ecdsa.PrivateKey
	cert         *x509.Certificate
	credentialID string
}

// NewStubProvider builds a StubProvider with a fresh P-256 key + self-signed cert.
func NewStubProvider() (*StubProvider, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("signingprovider: stub key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Stub Signer", Organization: []string{"Yivi Business Wallet Demo"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("signingprovider: stub cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("signingprovider: parse stub cert: %w", err)
	}
	return &StubProvider{key: key, cert: cert, credentialID: "stub-credential"}, nil
}

func (s *StubProvider) Discover(context.Context, string) (Info, error) {
	return Info{Name: "Stub QTSP", Specs: "2.2.0.0", OAuth2: "https://stub-issuer.local"}, nil
}

func (s *StubProvider) AuthorizeURL(issuer string, p AuthorizeParams) string {
	q := url.Values{}
	q.Set("client_id", p.ClientID)
	q.Set("state", p.State)
	q.Set("scope", p.Scope)
	return endpoint(issuer, "/oauth2/authorize") + "?" + q.Encode()
}

func (s *StubProvider) ExchangeToken(context.Context, string, string, string, string, string, string) (Token, error) {
	return Token{AccessToken: "stub-access-token", TokenType: "Bearer", ExpiresIn: 300}, nil
}

func (s *StubProvider) ListCredentials(context.Context, string, string) ([]string, error) {
	return []string{s.credentialID}, nil
}

func (s *StubProvider) CredentialInfo(_ context.Context, _, _, credentialID string) (Credential, error) {
	return Credential{
		ID:          credentialID,
		Certificate: s.cert,
		Chain:       []*x509.Certificate{s.cert},
		KeyAlgo:     []string{"1.2.840.10045.2.1", SignAlgoECDSASHA256OID},
	}, nil
}

// SignHash signs each base64 raw digest with raw ECDSA (ASN.1 DER), the same
// output shape the real QTSP returns.
func (s *StubProvider) SignHash(_ context.Context, _, _, _ string, hashesB64 []string, _, _ string) ([]string, error) {
	out := make([]string, 0, len(hashesB64))
	for _, h := range hashesB64 {
		digest, err := base64.StdEncoding.DecodeString(h)
		if err != nil {
			return nil, &RequestError{Reason: "the hash was not valid base64"}
		}
		sig, err := ecdsa.SignASN1(rand.Reader, s.key, digest)
		if err != nil {
			return nil, fmt.Errorf("signingprovider: stub sign: %w", err)
		}
		out = append(out, base64.StdEncoding.EncodeToString(sig))
	}
	return out, nil
}
