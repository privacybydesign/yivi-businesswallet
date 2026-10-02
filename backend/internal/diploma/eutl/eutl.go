// Package eutl loads trust anchors from the EU Trusted Lists (eIDAS).
//
// The European Commission publishes a List of Trusted Lists (LOTL) at a fixed
// URL. It points to one trusted list per member state, maintained by that
// state's supervisory body (in the Netherlands the Rijksinspectie Digitale
// Infrastructuur). Each list enumerates the qualified trust service providers
// and, per service, the X.509 certificate that identifies it: the issuing CA
// for a certificate service, or the signing certificate for a timestamp
// service. Under eIDAS those certificates ARE the trust anchors, so a verifier
// needs no root store of its own. This is how the reference implementation
// (EU DSS) validates PAdES signatures.
//
// Scope of this proof of concept: the lists are fetched over HTTPS and cached
// on disk, but their XAdES signatures are not verified (there is no mature Go
// XAdES implementation). Transport security is therefore the integrity
// guarantee. Service status history is also ignored: a service is used if its
// current status is granted. A production verifier should check that the
// service was granted at the signing time.
package eutl

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// LOTLURL is the well-known location of the EU List of Trusted Lists.
const LOTLURL = "https://ec.europa.eu/tools/lotl/eu-lotl.xml"

const tslMime = "application/vnd.etsi.tsl+xml"

// Service type identifiers from ETSI TS 119 612.
const (
	SvcCAQC          = "http://uri.etsi.org/TrstSvc/Svctype/CA/QC"
	SvcTSAQTST       = "http://uri.etsi.org/TrstSvc/Svctype/TSA/QTST"
	SvcTSATSSQC      = "http://uri.etsi.org/TrstSvc/Svctype/TSA/TSS-QC"
	SvcTSATSSAdESQC  = "http://uri.etsi.org/TrstSvc/Svctype/TSA/TSS-AdESQCandQES"
	SvcTSA           = "http://uri.etsi.org/TrstSvc/Svctype/TSA"
	statusGranted    = "http://uri.etsi.org/TrstSvc/TrustedList/Svcstatus/granted"
	statusRecognised = "http://uri.etsi.org/TrstSvc/TrustedList/Svcstatus/recognisedatnationallevel"
)

// Service is one trust service taken from a member state list.
type Service struct {
	Territory string
	Provider  string
	Name      string
	Type      string
	Status    string
	Certs     []*x509.Certificate
}

// Granted reports whether the service currently has a positive status.
func (s Service) Granted() bool {
	return s.Status == statusGranted || s.Status == statusRecognised
}

// Store holds the trust anchors loaded from the lists and implements the
// verify.TrustSource interface.
type Store struct {
	Services []Service
	// Territories that were loaded successfully, and errors for the rest.
	Loaded []string
	Errors map[string]error
	// FetchedAt is when the LOTL was retrieved (or the cache mtime).
	FetchedAt time.Time

	byFingerprint map[string]*Service
	signer, tsa   *x509.CertPool
}

// Options controls loading.
type Options struct {
	// HTTPClient defaults to a client with a 30s timeout.
	HTTPClient *http.Client
	// CacheDir stores downloaded lists. Empty disables caching.
	CacheDir string
	// MaxAge is how long a cached list is reused. Default 24h.
	MaxAge time.Duration
	// Territories limits which member state lists are loaded (e.g. "NL",
	// "RO"). Empty loads all of them.
	Territories []string
	// LOTL overrides the LOTL URL (tests).
	LOTL string
	// Parallelism is the number of concurrent downloads. Default 8.
	Parallelism int
}

// Load fetches the LOTL and the member state lists and builds a Store.
func Load(ctx context.Context, opts Options) (*Store, error) {
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.MaxAge == 0 {
		opts.MaxAge = 24 * time.Hour
	}
	if opts.LOTL == "" {
		opts.LOTL = LOTLURL
	}
	if opts.Parallelism <= 0 {
		opts.Parallelism = 8
	}
	f := fetcher{opts: opts}

	lotlXML, fetchedAt, err := f.get(ctx, opts.LOTL)
	if err != nil {
		return nil, fmt.Errorf("eutl: LOTL: %w", err)
	}
	lotl, err := parseTSL(lotlXML)
	if err != nil {
		return nil, fmt.Errorf("eutl: LOTL: %w", err)
	}

	want := map[string]bool{}
	for _, t := range opts.Territories {
		want[strings.ToUpper(t)] = true
	}
	type pointer struct{ territory, url string }
	var pointers []pointer
	for _, p := range lotl.SchemeInformation.Pointers {
		territory, mime := "", ""
		for _, oi := range p.OtherInformation {
			if oi.SchemeTerritory != "" {
				territory = oi.SchemeTerritory
			}
			if oi.MimeType != "" {
				mime = oi.MimeType
			}
		}
		if mime != tslMime || territory == "" || territory == lotl.SchemeInformation.SchemeTerritory {
			continue
		}
		if len(want) > 0 && !want[territory] {
			continue
		}
		pointers = append(pointers, pointer{territory, p.TSLLocation})
	}
	if len(pointers) == 0 {
		return nil, errors.New("eutl: LOTL contains no matching trusted list pointers")
	}

	store := &Store{Errors: map[string]error{}, FetchedAt: fetchedAt}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, opts.Parallelism)
	for _, p := range pointers {
		wg.Add(1)
		go func(p pointer) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			svcs, err := f.loadTerritory(ctx, p.territory, p.url)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				store.Errors[p.territory] = err
				return
			}
			store.Loaded = append(store.Loaded, p.territory)
			store.Services = append(store.Services, svcs...)
		}(p)
	}
	wg.Wait()
	if len(store.Loaded) == 0 {
		return nil, fmt.Errorf("eutl: no trusted list could be loaded: %v", store.Errors)
	}
	sort.Strings(store.Loaded)
	store.index()
	return store, nil
}

func (s *Store) index() {
	s.byFingerprint = map[string]*Service{}
	s.signer, s.tsa = x509.NewCertPool(), x509.NewCertPool()
	for i := range s.Services {
		svc := &s.Services[i]
		if !svc.Granted() {
			continue
		}
		for _, c := range svc.Certs {
			s.byFingerprint[fingerprint(c)] = svc
			switch svc.Type {
			case SvcCAQC:
				s.signer.AddCert(c)
			case SvcTSAQTST, SvcTSATSSQC, SvcTSATSSAdESQC, SvcTSA:
				s.tsa.AddCert(c)
			}
		}
	}
}

// SignerRoots returns the anchors for qualified certificate services.
func (s *Store) SignerRoots() *x509.CertPool { return s.signer }

// TSARoots returns the anchors for (qualified) timestamp services.
func (s *Store) TSARoots() *x509.CertPool { return s.tsa }

// Describe returns the trusted-list entry behind an anchor certificate.
func (s *Store) Describe(anchor *x509.Certificate) string {
	svc, ok := s.byFingerprint[fingerprint(anchor)]
	if !ok {
		return ""
	}
	return fmt.Sprintf("EU trusted list %s: %s, service %q (%s)", svc.Territory, svc.Provider, svc.Name,
		svc.Type[strings.LastIndex(svc.Type, "Svctype/")+len("Svctype/"):])
}

// Lookup returns the service that lists the given certificate, if any.
func (s *Store) Lookup(c *x509.Certificate) (*Service, bool) {
	svc, ok := s.byFingerprint[fingerprint(c)]
	return svc, ok
}

func fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}

// --- fetching -------------------------------------------------------------

type fetcher struct{ opts Options }

func (f fetcher) loadTerritory(ctx context.Context, territory, url string) ([]Service, error) {
	body, _, err := f.get(ctx, url)
	if err != nil {
		return nil, err
	}
	tsl, err := parseTSL(body)
	if err != nil {
		return nil, err
	}
	var out []Service
	for _, tsp := range tsl.Providers {
		provider := tsp.TSPInformation.TSPName.pick()
		for _, svc := range tsp.TSPServices {
			si := svc.ServiceInformation
			s := Service{
				Territory: territory,
				Provider:  provider,
				Name:      si.ServiceName.pick(),
				Type:      strings.TrimSpace(si.ServiceTypeIdentifier),
				Status:    strings.TrimSpace(si.ServiceStatus),
			}
			for _, id := range si.ServiceDigitalIdentity.DigitalIds {
				if id.X509Certificate == "" {
					continue
				}
				der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(id.X509Certificate), ""))
				if err != nil {
					continue
				}
				c, err := x509.ParseCertificate(der)
				if err != nil {
					continue // some lists carry malformed legacy certs
				}
				s.Certs = append(s.Certs, c)
			}
			if len(s.Certs) > 0 {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// get returns the body of url, from cache when fresh enough, falling back to
// a stale cache entry when the network fails.
func (f fetcher) get(ctx context.Context, url string) ([]byte, time.Time, error) {
	var cachePath string
	if f.opts.CacheDir != "" {
		sum := sha256.Sum256([]byte(url))
		cachePath = filepath.Join(f.opts.CacheDir, hex.EncodeToString(sum[:8])+".xml")
		if st, err := os.Stat(cachePath); err == nil && time.Since(st.ModTime()) < f.opts.MaxAge {
			if b, err := os.ReadFile(cachePath); err == nil {
				return b, st.ModTime(), nil
			}
		}
	}
	body, err := f.download(ctx, url)
	if err != nil {
		if cachePath != "" {
			if st, serr := os.Stat(cachePath); serr == nil {
				if b, rerr := os.ReadFile(cachePath); rerr == nil {
					return b, st.ModTime(), nil // stale but better than nothing
				}
			}
		}
		return nil, time.Time{}, err
	}
	if cachePath != "" {
		if err := os.MkdirAll(f.opts.CacheDir, 0o755); err == nil {
			_ = os.WriteFile(cachePath, body, 0o644)
		}
	}
	return body, time.Now(), nil
}

func (f fetcher) download(ctx context.Context, url string) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("refusing non-HTTPS trusted list URL %s", url)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "diplomacheck/0.1 (+eutl)")
	resp, err := f.opts.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

// --- XML model (ETSI TS 119 612, namespace-agnostic) -----------------------

type tsl struct {
	SchemeInformation struct {
		SchemeTerritory string       `xml:"SchemeTerritory"`
		Pointers        []tslPointer `xml:"PointersToOtherTSL>OtherTSLPointer"`
	} `xml:"SchemeInformation"`
	Providers []struct {
		TSPInformation struct {
			TSPName names `xml:"TSPName"`
		} `xml:"TSPInformation"`
		TSPServices []struct {
			ServiceInformation struct {
				ServiceTypeIdentifier  string `xml:"ServiceTypeIdentifier"`
				ServiceName            names  `xml:"ServiceName"`
				ServiceStatus          string `xml:"ServiceStatus"`
				ServiceDigitalIdentity struct {
					DigitalIds []struct {
						X509Certificate string `xml:"X509Certificate"`
					} `xml:"DigitalId"`
				} `xml:"ServiceDigitalIdentity"`
			} `xml:"ServiceInformation"`
		} `xml:"TSPServices>TSPService"`
	} `xml:"TrustServiceProviderList>TrustServiceProvider"`
}

type tslPointer struct {
	TSLLocation      string `xml:"TSLLocation"`
	OtherInformation []struct {
		SchemeTerritory string `xml:"SchemeTerritory"`
		MimeType        string `xml:"MimeType"`
	} `xml:"AdditionalInformation>OtherInformation"`
}

type names struct {
	Name []struct {
		Lang string `xml:"lang,attr"`
		Text string `xml:",chardata"`
	} `xml:"Name"`
}

// pick prefers the English name, then the first one.
func (n names) pick() string {
	for _, x := range n.Name {
		if strings.EqualFold(x.Lang, "en") {
			return strings.TrimSpace(x.Text)
		}
	}
	if len(n.Name) > 0 {
		return strings.TrimSpace(n.Name[0].Text)
	}
	return ""
}

func parseTSL(b []byte) (*tsl, error) {
	var t tsl
	if err := xml.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
