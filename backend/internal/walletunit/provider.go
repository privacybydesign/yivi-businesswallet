package walletunit

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// Config is what the provider states about the wallet solution and its HSM.
type Config struct {
	// BaseURL is the public base (no /api/v1) the status lists are served under.
	BaseURL       string
	WalletName    string
	WalletVersion string
	// CertificationInfo is the wallet solution's certification information; empty
	// leaves the claim out.
	CertificationInfo string
	// SKA is nil when the deployment states no HSM certification: then no SKA is
	// issued, as nothing honest could be said about key storage.
	SKA *SKAConfig
}

// SKAConfig describes the HSM the WSCA keeps keys in (OpenID4VCI Appendix D.1).
type SKAConfig struct {
	CertificationURL   string
	KeyStorage         []string
	UserAuthentication []string
}

// IssueRequest names the keys a wallet unit wants attested.
type IssueRequest struct {
	// InstanceKey becomes the BWIA cnf: the key the unit proves possession of
	// at the token endpoint (CS-05 Stage A).
	InstanceKey *ecdsa.PublicKey
	// AttestedKeys go into the SKA, which binds credentials to them (Stage B).
	AttestedKeys []*ecdsa.PublicKey
	// KeysInHSM says the keys are held in the WSCA/HSM. Without it no SKA is issued.
	KeysInHSM bool
}

// Issued is one BWUA. SKA is empty when none was issued.
type Issued struct {
	BWIA string
	SKA  string
}

type orgReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (organization.Organization, error)
}

// Provider issues BWUAs and signs the status lists they reference.
type Provider struct {
	store  *Store
	orgs   orgReader
	signer *Signer
	cfg    Config
	now    func() time.Time
}

func NewProvider(store *Store, orgs orgReader, signer *Signer, cfg Config) *Provider {
	return &Provider{store: store, orgs: orgs, signer: signer, cfg: cfg.normalized(), now: time.Now}
}

func (c Config) normalized() Config {
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	return c
}

// Attester is the wallet unit attestation source: Provider, or StubAttester in
// local dev and CI (WALLET_UNIT_ATTESTER).
type Attester interface {
	Issue(ctx context.Context, orgID uuid.UUID, req IssueRequest) (Issued, error)
	StatusList(ctx context.Context, list int64) (string, error)
}

var _ Attester = (*Provider)(nil)

// prepare checks the org can be attested and encodes the keys req names. The
// attested keys are empty unless an SKA is due.
func (c Config) prepare(ctx context.Context, orgs orgReader, orgID uuid.UUID, req IssueRequest) (organization.Organization, map[string]any, []map[string]any, error) {
	if req.InstanceKey == nil {
		return organization.Organization{}, nil, nil, ErrNoInstanceKey
	}
	org, err := orgs.GetByID(ctx, orgID)
	if err != nil {
		return organization.Organization{}, nil, nil, fmt.Errorf("walletunit: org %s: %w", orgID, err)
	}
	if org.Status != organization.StatusActive {
		return organization.Organization{}, nil, nil, ErrOrgNotActive
	}
	if org.EUID == "" {
		return organization.Organization{}, nil, nil, ErrNoEUID
	}
	cnf, err := ecJWK(req.InstanceKey)
	if err != nil {
		return organization.Organization{}, nil, nil, err
	}
	var attested []map[string]any
	if c.SKA != nil && req.KeysInHSM {
		for _, k := range req.AttestedKeys {
			j, err := ecJWK(k)
			if err != nil {
				return organization.Organization{}, nil, nil, err
			}
			attested = append(attested, j)
		}
	}
	return org, cnf, attested, nil
}

// Issue attests the org's wallet unit: a fresh BWIA for req.InstanceKey and, when
// the keys are HSM-held and the deployment describes its HSM, an SKA. Each gets
// its own status index, so two attestations cannot be linked by it.
func (p *Provider) Issue(ctx context.Context, orgID uuid.UUID, req IssueRequest) (Issued, error) {
	org, cnf, attested, err := p.cfg.prepare(ctx, p.orgs, orgID, req)
	if err != nil {
		return Issued{}, err
	}

	unit, err := p.store.activeUnitForIssue(ctx, orgID)
	if err != nil {
		return Issued{}, err
	}
	now := p.now()
	exp, statusExp := now.Add(tokenTTL), now.Add(statusMaintenance)

	idx, err := p.store.recordAttestation(ctx, unit.ID, KindBWIA, exp, statusExp)
	if err != nil {
		return Issued{}, err
	}
	var out Issued
	if out.BWIA, err = p.signer.sign(bwiaTyp, p.cfg.bwiaClaims(unit.ID.String(), org, cnf, idx, now)); err != nil {
		return Issued{}, err
	}
	if len(attested) == 0 {
		return out, nil
	}

	idx, err = p.store.recordAttestation(ctx, unit.ID, KindSKA, exp, statusExp)
	if err != nil {
		return Issued{}, err
	}
	if out.SKA, err = p.signer.sign(skaTyp, p.cfg.skaClaims(attested, idx, now)); err != nil {
		return Issued{}, err
	}
	return out, nil
}

// StatusList is the signed Token Status List `list`, as served at its URI.
func (p *Provider) StatusList(ctx context.Context, list int64) (string, error) {
	if list < 0 || list >= statusListCount {
		return "", ErrUnknownList
	}
	statuses, err := p.store.listStatuses(ctx, list)
	if err != nil {
		return "", err
	}
	lst, err := encodeStatusList(statuses)
	if err != nil {
		return "", err
	}
	return p.signer.sign(statusListTyp, p.cfg.statusListClaims(list, lst, p.now()))
}

// bwiaClaims is the BWIA for unit, whose status entry is idx.
func (c Config) bwiaClaims(unit string, org organization.Organization, cnf map[string]any, idx int64, now time.Time) jwt.MapClaims {
	claims := jwt.MapClaims{
		"sub":            c.BaseURL + "/wallet-units/" + unit,
		"iat":            now.Unix(),
		"exp":            now.Add(tokenTTL).Unix(),
		"cnf":            map[string]any{"jwk": cnf},
		"wallet_name":    c.WalletName,
		"wallet_version": c.WalletVersion,
		"wallet_link":    c.BaseURL,
		"ebwoid_id":      org.EUID,
		"legal_name":     org.Name,
		"client_status":  map[string]any{"status": map[string]any{"status_list": statusRef(c.BaseURL, idx)}, "exp": now.Add(statusMaintenance).Unix()},
	}
	if c.CertificationInfo != "" {
		claims["wallet_solution_certification_information"] = c.CertificationInfo
	}
	return claims
}

// skaClaims is the SKA for attested, whose status entry is idx. c.SKA is set.
func (c Config) skaClaims(attested []map[string]any, idx int64, now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iat":                 now.Unix(),
		"exp":                 now.Add(tokenTTL).Unix(),
		"attested_keys":       attested,
		"key_storage":         c.SKA.KeyStorage,
		"user_authentication": c.SKA.UserAuthentication,
		"certification":       c.SKA.CertificationURL,
		"key_storage_status":  map[string]any{"status": map[string]any{"status_list": statusRef(c.BaseURL, idx)}, "exp": now.Add(statusMaintenance).Unix()},
	}
}

func (c Config) statusListClaims(list int64, lst string, now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"sub":         statusListURI(c.BaseURL, list),
		"iat":         now.Unix(),
		"exp":         now.Add(statusListTokenTTL).Unix(),
		"ttl":         int64(statusListCacheTTL / time.Second),
		"status_list": map[string]any{"bits": statusBits, "lst": lst},
	}
}

// ecJWK is pub as a public EC JWK (RFC 7518 §6.2).
func ecJWK(pub *ecdsa.PublicKey) (map[string]any, error) {
	ecdhKey, err := pub.ECDH()
	if err != nil {
		return nil, fmt.Errorf("walletunit: key: %w", err)
	}
	if pub.Curve.Params().Name != "P-256" {
		return nil, fmt.Errorf("walletunit: key curve %s, want P-256", pub.Curve.Params().Name)
	}
	// Uncompressed point: 0x04 || X || Y, each coordinate 32 bytes on P-256.
	raw := ecdhKey.Bytes()
	half := (len(raw) - 1) / 2
	return map[string]any{
		"kty": "EC",
		"crv": "P-256",
		"x":   base64.RawURLEncoding.EncodeToString(raw[1 : 1+half]),
		"y":   base64.RawURLEncoding.EncodeToString(raw[1+half:]),
	}, nil
}
