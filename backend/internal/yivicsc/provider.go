package yivicsc

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
)

// remoteProvider is a signing provider reached over the network: every
// organisation that connected its own.
type remoteProvider interface {
	Discover(ctx context.Context, baseURL string) (signingprovider.Info, error)
	AuthorizeURL(issuer string, p signingprovider.AuthorizeParams) string
	ExchangeToken(ctx context.Context, issuer, clientID, clientSecret, code, codeVerifier, redirectURI string) (signingprovider.Token, error)
	ListCredentials(ctx context.Context, baseURL, accessToken string) ([]string, error)
	CredentialInfo(ctx context.Context, baseURL, accessToken, credentialID string) (signingprovider.Credential, error)
	SignHash(ctx context.Context, baseURL, accessToken, credentialID string, hashesB64 []string, signAlgoOID, hashAlgoOID string) ([]string, error)
}

// Provider is the signing provider the ceremony (internal/signing) drives. A
// call for this service's own base URL is answered in process, by the same
// Service the HTTP routes call, so the backend never has to reach its own
// public address; every other base URL goes to remote unchanged. Only the
// browser's leg, the authorize URL, is ever a real request to this service.
type Provider struct {
	svc    *Service
	remote remoteProvider
}

func NewProvider(svc *Service, remote remoteProvider) *Provider {
	return &Provider{svc: svc, remote: remote}
}

func (p *Provider) own(baseURL string) bool { return baseURL == p.svc.baseURL }

func (p *Provider) Discover(ctx context.Context, baseURL string) (signingprovider.Info, error) {
	if !p.own(baseURL) {
		return p.remote.Discover(ctx, baseURL)
	}
	info := p.svc.Info()
	return signingprovider.Info{Name: info.Name, Specs: info.Specs, OAuth2: info.OAuth2}, nil
}

// AuthorizeURL is the remote's for both: the URL has one shape, and the
// service's issuer is its base URL.
func (p *Provider) AuthorizeURL(issuer string, params signingprovider.AuthorizeParams) string {
	return p.remote.AuthorizeURL(issuer, params)
}

func (p *Provider) ExchangeToken(ctx context.Context, issuer, clientID, clientSecret, code, codeVerifier, redirectURI string) (signingprovider.Token, error) {
	if !p.own(issuer) {
		return p.remote.ExchangeToken(ctx, issuer, clientID, clientSecret, code, codeVerifier, redirectURI)
	}
	token, err := p.svc.Token(TokenRequest{
		GrantType: grantTypeAuthCode, Code: code, RedirectURI: redirectURI,
		CodeVerifier: codeVerifier, ClientID: clientID, ClientSecret: clientSecret,
	})
	if err != nil {
		return signingprovider.Token{}, requestError(err)
	}
	return signingprovider.Token{AccessToken: token.AccessToken, TokenType: token.TokenType, ExpiresIn: token.ExpiresIn}, nil
}

func (p *Provider) ListCredentials(ctx context.Context, baseURL, accessToken string) ([]string, error) {
	if !p.own(baseURL) {
		return p.remote.ListCredentials(ctx, baseURL, accessToken)
	}
	ids, err := p.svc.ListCredentials(ctx, accessToken)
	return ids, requestError(err)
}

func (p *Provider) CredentialInfo(ctx context.Context, baseURL, accessToken, credentialID string) (signingprovider.Credential, error) {
	if !p.own(baseURL) {
		return p.remote.CredentialInfo(ctx, baseURL, accessToken, credentialID)
	}
	info, err := p.svc.CredentialInfo(ctx, accessToken, credentialID, "")
	if err != nil {
		return signingprovider.Credential{}, requestError(err)
	}
	chain := make([]*x509.Certificate, 0, len(info.Cert.Certificates))
	for _, encoded := range info.Cert.Certificates {
		der, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return signingprovider.Credential{}, &signingprovider.RequestError{Reason: "the credential certificate was not valid base64"}
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return signingprovider.Credential{}, &signingprovider.RequestError{Reason: "the credential certificate could not be parsed"}
		}
		chain = append(chain, cert)
	}
	if len(chain) == 0 {
		return signingprovider.Credential{}, &signingprovider.RequestError{Reason: "the credential carried no certificate"}
	}
	return signingprovider.Credential{ID: credentialID, Certificate: chain[0], Chain: chain, KeyAlgo: info.Key.Algo}, nil
}

func (p *Provider) SignHash(ctx context.Context, baseURL, accessToken, credentialID string, hashesB64 []string, signAlgoOID, hashAlgoOID string) ([]string, error) {
	if !p.own(baseURL) {
		return p.remote.SignHash(ctx, baseURL, accessToken, credentialID, hashesB64, signAlgoOID, hashAlgoOID)
	}
	signatures, err := p.svc.SignHash(ctx, accessToken, SignHashRequest{
		CredentialID: credentialID, Hashes: hashesB64, HashAlgorithmOID: hashAlgoOID, SignAlgo: signAlgoOID,
	})
	return signatures, requestError(err)
}

// requestError gives the ceremony the error a remote provider would: what the
// service told the client, as a reason. A failure of the service's own is
// passed on as it is, for the ceremony to log.
func requestError(err error) error {
	if refused, ok := errors.AsType[*Error](err); ok {
		return &signingprovider.RequestError{Reason: "the signing service answered " + refused.Code + ": " + refused.Description}
	}
	return err
}
