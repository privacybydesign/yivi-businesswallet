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

// trustTerritories are the member state lists DUO's extracts need: NL for
// DUO's PKIoverheid CA, RO for certSIGN's timestamps.
var trustTerritories = []string{"NL", "RO"}

const (
	// trustRefresh is how old the loaded lists may get before the next
	// validation reloads them: as long as eutl reuses a cached list.
	trustRefresh = eutl.DefaultMaxAge
	// trustRetry is how long after a failed reload (or a boot on the pinned
	// fallback) the next one is tried.
	trustRetry = 15 * time.Minute
	// trustReloadTimeout bounds a whole reload (the List of Trusted Lists and
	// every territory's list), apart from the upload that triggered it.
	trustReloadTimeout = 2 * time.Minute
)

// TrustConfig is where the trust anchors come from.
type TrustConfig struct {
	// Source is TrustSourceEUTL (the default) or TrustSourcePinned.
	Source string
	// CacheDir keeps the downloaded lists between restarts; empty keeps none.
	CacheDir string
	// FallbackToPinned boots on the embedded roots when the EU lists cannot be
	// loaded at startup; otherwise that fails the boot. Either way a failed
	// reload later keeps the anchors already loaded.
	FallbackToPinned bool
}

// TrustStore holds the current trust anchors. It reloads them in the
// background once they are trustRefresh old, never on a timer, and retries a
// failed reload after trustRetry. Safe for concurrent use.
type TrustStore struct {
	config     TrustConfig
	httpClient *http.Client
	now        func() time.Time
	// lotl overrides the List of Trusted Lists URL (tests).
	lotl string
	// reloads tracks background reloads, so a test can wait for one.
	reloads sync.WaitGroup

	// mutex guards the fields below; it is never held across a load.
	mutex       sync.Mutex
	source      verify.TrustSource
	description string
	// loadedAt is when source was loaded from config.Source; zero while the
	// store runs on the pinned fallback.
	loadedAt    time.Time
	attemptedAt time.Time
	reloading   bool
}

// NewTrustStore loads the anchors once. With TrustSourceEUTL this downloads
// the EU List of Trusted Lists and trustTerritories' lists (or reads them
// from the cache), which takes a few seconds.
func NewTrustStore(ctx context.Context, config TrustConfig) (*TrustStore, error) {
	store, err := newTrustStore(config)
	if err != nil {
		return nil, err
	}
	if err := store.init(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func newTrustStore(config TrustConfig) (*TrustStore, error) {
	if config.Source == "" {
		config.Source = TrustSourceEUTL
	}
	if config.Source != TrustSourceEUTL && config.Source != TrustSourcePinned {
		return nil, fmt.Errorf("diploma: unknown trust source %q (use %q or %q)", config.Source, TrustSourceEUTL, TrustSourcePinned)
	}
	return &TrustStore{config: config, httpClient: &http.Client{Timeout: eutl.DefaultHTTPTimeout}, now: time.Now}, nil
}

// init loads the anchors before the store is shared, falling back to the
// pinned roots when configured to.
func (s *TrustStore) init(ctx context.Context) error {
	now := s.now()
	s.attemptedAt = now
	source, description, err := s.load(ctx)
	if err == nil {
		s.source, s.description, s.loadedAt = source, description, now
		slog.InfoContext(ctx, "diploma: trust anchors loaded", slog.String("source", description))
		return nil
	}
	if !s.config.FallbackToPinned {
		return err
	}
	pinned, perr := trust.NewPinned()
	if perr != nil {
		return perr
	}
	s.source, s.description = pinned, "pinned roots (EU trusted lists unavailable)"
	slog.WarnContext(ctx, "diploma: EU trusted lists unavailable, using the pinned roots until a reload succeeds",
		slog.String("error", err.Error()))
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
		MaxAge:      trustRefresh,
		Territories: trustTerritories,
		LOTL:        s.lotl,
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

// Current returns the anchors at once. When they are trustRefresh old (or the
// store runs on the pinned fallback) and no reload ran in the last trustRetry,
// it starts one in the background, detached from ctx: an upload cut short must
// not cut a reload short halfway, nor wait for one.
func (s *TrustStore) Current(ctx context.Context) verify.TrustSource {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	now := s.now()
	if !s.reloading && now.Sub(s.loadedAt) >= trustRefresh && now.Sub(s.attemptedAt) >= trustRetry {
		s.reloading, s.attemptedAt = true, now
		s.reloads.Add(1)
		go s.reload(context.WithoutCancel(ctx))
	}
	return s.source
}

// reload loads the anchors anew; a failure keeps the current ones until the
// next attempt.
func (s *TrustStore) reload(ctx context.Context) {
	defer s.reloads.Done()
	ctx, cancel := context.WithTimeout(ctx, trustReloadTimeout)
	defer cancel()
	source, description, err := s.load(ctx)

	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.reloading = false
	if err != nil {
		slog.WarnContext(ctx, "diploma: reload trust anchors failed, keeping the current ones",
			slog.String("current", s.description), slog.String("error", err.Error()))
		return
	}
	s.source, s.description, s.loadedAt = source, description, s.now()
	slog.InfoContext(ctx, "diploma: trust anchors reloaded", slog.String("source", description))
}

// PadesValidator verifies the PAdES signature DUO puts on every extract
// against the anchors of a TrustStore.
type PadesValidator struct {
	trust      *TrustStore
	ocsp       bool
	httpClient *http.Client
}

// Revocation is where a PadesValidator checks the revocation of the signer's
// chain: against the revocation information embedded in the signature
// (offline), or online against the OCSP responders and CRLs. Either way an
// unchecked certificate fails the check.
type Revocation int

const (
	RevocationOffline Revocation = iota
	RevocationOCSP
)

// NewPadesValidator creates a validator that checks revocation as asked.
func NewPadesValidator(store *TrustStore, revocation Revocation) *PadesValidator {
	return &PadesValidator{trust: store, ocsp: revocation == RevocationOCSP, httpClient: &http.Client{Timeout: verify.OCSPTimeout}}
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
	return fromResult(result), nil
}

// StubValidator accepts every PDF as signed by DUO, for dev and CI, where no
// one holds a DUO-signed extract of a test person. The extract is still
// parsed and matched for real.
type StubValidator struct{}

func (StubValidator) Ping(context.Context) error { return nil }

func (StubValidator) Validate(context.Context, []byte) (*Verification, error) {
	return &Verification{Valid: true, Checks: []verify.Check{}, Signer: "stub", TrustAnchor: "stub"}, nil
}

// fromResult converts the verifier's result into a Verification.
func fromResult(result *verify.Result) *Verification {
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
