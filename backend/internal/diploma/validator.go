package diploma

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/eutl"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/trust"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/verify"
)

// ErrTrustUnavailable is returned when no trust anchors could be loaded, so a
// signature cannot be validated at all. The document itself is not at fault.
var ErrTrustUnavailable = errors.New("trust anchors unavailable")

// Verification is the outcome of the cryptographic verification of one
// extract: the PAdES signature, the timestamp, the certificate chain and the
// identity of the signer (DUO).
type Verification struct {
	// Valid is true only when every check passed.
	Valid bool `json:"valid"`
	// Checks lists every individual check in the order it ran.
	Checks []verify.Check `json:"checks"`
	// Signer is the subject of the signing certificate, e.g. "CN=Dienst
	// Uitvoering Onderwijs,O=Dienst Uitvoering Onderwijs (DUO),C=NL".
	Signer string `json:"signer,omitempty"`
	// TrustAnchor describes where the trust anchor came from (an EU trusted
	// list entry or a pinned root).
	TrustAnchor string `json:"trust_anchor,omitempty"`
	// SigningTime is the time the signature was made (from the RFC 3161
	// timestamp when TimestampTrusted, otherwise as claimed by the signer).
	SigningTime      time.Time `json:"signing_time"`
	TimestampTrusted bool      `json:"timestamp_trusted"`
}

// Key is a stable identifier for the outcome: "valid" or the ID of the first
// failed check.
func (v *Verification) Key() string {
	if v.Valid {
		return "valid"
	}
	for _, c := range v.Checks {
		if !c.OK {
			return c.ID
		}
	}
	return "invalid"
}

// FailedChecks returns the checks that did not pass.
func (v *Verification) FailedChecks() []verify.Check {
	var failed []verify.Check
	for _, c := range v.Checks {
		if !c.OK {
			failed = append(failed, c)
		}
	}
	return failed
}

// Validator checks a diploma extract for authenticity and integrity.
type Validator interface {
	// Validate verifies the signature. A well formed answer, including a
	// rejection, is returned as a Verification; an error means the check
	// could not be performed (ErrTrustUnavailable or an internal failure).
	Validate(ctx context.Context, pdf []byte) (*Verification, error)
	// Ping fails when the validator has no trust anchors to check against.
	Ping(ctx context.Context) error
}

// Trust sources: the EU Trusted Lists (eIDAS) or the embedded PKIoverheid and
// certSIGN roots, which work offline.
const (
	TrustSourceEUTL   = "eutl"
	TrustSourcePinned = "pinned"
)

// TrustTerritories are the member state lists DUO's extracts need: NL for
// DUO's PKIoverheid CA, RO for certSIGN's timestamps.
var TrustTerritories = []string{"NL", "RO"}

const (
	// TrustRefresh is how old the loaded lists may get before the next
	// validation reloads them.
	TrustRefresh = 24 * time.Hour
	// trustHTTPTimeout bounds one trusted-list download; ocspHTTPTimeout one
	// revocation check.
	trustHTTPTimeout = 30 * time.Second
	ocspHTTPTimeout  = 10 * time.Second
)

// TrustConfig is where the trust anchors come from.
type TrustConfig struct {
	// Source is TrustSourceEUTL (the default) or TrustSourcePinned.
	Source string
	// CacheDir keeps the downloaded lists between restarts; empty keeps none.
	CacheDir string
	// FallbackToPinned uses the embedded roots when the EU lists cannot be
	// loaded; otherwise a failed first load fails the boot and a failed
	// reload keeps the lists already loaded.
	FallbackToPinned bool
}

// TrustStore holds the current trust anchors. It reloads them on use once
// they are TrustRefresh old, never on a timer. Safe for concurrent use.
type TrustStore struct {
	config     TrustConfig
	httpClient *http.Client
	now        func() time.Time

	mutex       sync.Mutex
	source      verify.TrustSource
	description string
	loadedAt    time.Time
}

// NewTrustStore loads the anchors once. With TrustSourceEUTL this downloads
// the EU List of Trusted Lists and TrustTerritories' lists (or reads them
// from the cache), which takes a few seconds.
func NewTrustStore(ctx context.Context, config TrustConfig) (*TrustStore, error) {
	if config.Source == "" {
		config.Source = TrustSourceEUTL
	}
	if config.Source != TrustSourceEUTL && config.Source != TrustSourcePinned {
		return nil, fmt.Errorf("diploma: unknown trust source %q (use %q or %q)", config.Source, TrustSourceEUTL, TrustSourcePinned)
	}
	store := &TrustStore{config: config, httpClient: &http.Client{Timeout: trustHTTPTimeout}, now: time.Now}
	if err := store.refresh(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

// refresh reloads the anchors. Called with the mutex held, or before the store
// is shared.
func (s *TrustStore) refresh(ctx context.Context) error {
	source, description, err := s.load(ctx)
	if err != nil {
		if !s.config.FallbackToPinned {
			return err
		}
		pinned, perr := trust.NewPinned()
		if perr != nil {
			return perr
		}
		slog.WarnContext(ctx, "diploma: EU trusted lists unavailable, using the pinned roots", slog.String("error", err.Error()))
		source, description = pinned, "pinned roots (EU trusted lists unavailable)"
	}
	s.source, s.description, s.loadedAt = source, description, s.now()
	slog.InfoContext(ctx, "diploma: trust anchors loaded", slog.String("source", description))
	return nil
}

func (s *TrustStore) load(ctx context.Context) (verify.TrustSource, string, error) {
	if s.config.Source == TrustSourcePinned {
		pinned, err := trust.NewPinned()
		if err != nil {
			return nil, "", err
		}
		return pinned, "pinned roots", nil
	}
	store, err := eutl.Load(ctx, eutl.Options{
		HTTPClient:  s.httpClient,
		CacheDir:    s.config.CacheDir,
		MaxAge:      TrustRefresh,
		Territories: TrustTerritories,
	})
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrTrustUnavailable, err)
	}
	for territory, terr := range store.Errors {
		slog.WarnContext(ctx, "diploma: EU trusted list not loaded", slog.String("territory", territory), slog.String("error", terr.Error()))
	}
	description := fmt.Sprintf("EU trusted lists (%d territories, %d services, fetched %s)",
		len(store.Loaded), len(store.Services), store.FetchedAt.UTC().Format(time.RFC3339))
	return store, description, nil
}

// Current returns the anchors, first reloading them when they are
// TrustRefresh old; a failed reload keeps the current ones.
func (s *TrustStore) Current(ctx context.Context) verify.TrustSource {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.now().Sub(s.loadedAt) >= TrustRefresh {
		if err := s.refresh(ctx); err != nil {
			slog.WarnContext(ctx, "diploma: reload trust anchors, keeping the current ones", slog.String("error", err.Error()))
		}
	}
	return s.source
}

// PadesValidator verifies the PAdES signature DUO puts on every extract
// against the anchors of a TrustStore.
type PadesValidator struct {
	trust      *TrustStore
	ocsp       bool
	httpClient *http.Client
}

// NewPadesValidator creates a validator. With ocsp the signer certificate is
// also checked online against DUO's OCSP responder.
func NewPadesValidator(store *TrustStore, ocsp bool) *PadesValidator {
	return &PadesValidator{trust: store, ocsp: ocsp, httpClient: &http.Client{Timeout: ocspHTTPTimeout}}
}

// Ping fails when no anchors are loaded.
func (v *PadesValidator) Ping(ctx context.Context) error {
	if v.trust.Current(ctx) == nil {
		return ErrTrustUnavailable
	}
	return nil
}

// Validate verifies the signature of the PDF.
func (v *PadesValidator) Validate(ctx context.Context, pdf []byte) (*Verification, error) {
	source := v.trust.Current(ctx)
	if source == nil {
		return nil, ErrTrustUnavailable
	}
	result, err := verify.PDF(ctx, pdf, verify.Options{
		Trust:      source,
		OCSP:       v.ocsp,
		HTTPClient: v.httpClient,
	})
	if err != nil {
		return nil, fmt.Errorf("diploma: verify signature: %w", err)
	}
	return FromResult(result), nil
}

// StubValidator accepts every PDF as signed by DUO, for dev and CI, where no
// one holds a DUO-signed extract of a test person. The extract is still
// parsed and matched for real.
type StubValidator struct{}

func (StubValidator) Ping(context.Context) error { return nil }

func (StubValidator) Validate(context.Context, []byte) (*Verification, error) {
	return &Verification{Valid: true, Checks: []verify.Check{}, Signer: "stub", TrustAnchor: "stub"}, nil
}

// FromResult converts the verifier's result into a Verification.
func FromResult(result *verify.Result) *Verification {
	v := &Verification{
		Valid:            result.Valid,
		Checks:           result.Checks,
		TrustAnchor:      result.Anchor,
		SigningTime:      result.SigningTime,
		TimestampTrusted: result.TimestampTrusted,
	}
	if v.Checks == nil {
		v.Checks = []verify.Check{}
	}
	if result.Signer != nil {
		v.Signer = result.Signer.Subject.String()
	}
	return v
}
