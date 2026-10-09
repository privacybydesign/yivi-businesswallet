package diploma

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/verify"
)

// listServer serves a List of Trusted Lists pointing at one NL list, or 503
// while failing is set, and counts the LOTL downloads.
type listServer struct {
	*httptest.Server
	failing atomic.Bool
	hits    atomic.Int32
}

func newListServer(t *testing.T) *listServer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	s := &listServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/lotl.xml", func(w http.ResponseWriter, _ *http.Request) {
		s.hits.Add(1)
		if s.failing.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`<TrustServiceStatusList xmlns="http://uri.etsi.org/02231/v2#"><SchemeInformation><SchemeTerritory>EU</SchemeTerritory>
<PointersToOtherTSL><OtherTSLPointer><TSLLocation>` + s.URL + `/nl.xml</TSLLocation><AdditionalInformation>
<OtherInformation><SchemeTerritory>NL</SchemeTerritory></OtherInformation><OtherInformation><MimeType>application/vnd.etsi.tsl+xml</MimeType></OtherInformation>
</AdditionalInformation></OtherTSLPointer></PointersToOtherTSL></SchemeInformation></TrustServiceStatusList>`))
	})
	mux.HandleFunc("/nl.xml", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<TrustServiceStatusList xmlns="http://uri.etsi.org/02231/v2#"><SchemeInformation><SchemeTerritory>NL</SchemeTerritory></SchemeInformation>
<TrustServiceProviderList><TrustServiceProvider><TSPInformation><TSPName><Name xml:lang="en">Test TSP</Name></TSPName></TSPInformation>
<TSPServices><TSPService><ServiceInformation>
<ServiceTypeIdentifier>http://uri.etsi.org/TrstSvc/Svctype/CA/QC</ServiceTypeIdentifier>
<ServiceName><Name xml:lang="en">Test CA</Name></ServiceName>
<ServiceDigitalIdentity><DigitalId><X509Certificate>` + base64.StdEncoding.EncodeToString(der) + `</X509Certificate></DigitalId></ServiceDigitalIdentity>
<ServiceStatus>http://uri.etsi.org/TrstSvc/TrustedList/Svcstatus/granted</ServiceStatus>
</ServiceInformation></TSPService></TSPServices></TrustServiceProvider></TrustServiceProviderList></TrustServiceStatusList>`))
	})
	s.Server = httptest.NewTLSServer(mux)
	t.Cleanup(s.Close)
	return s
}

// fakeClock is a settable now.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func trustStoreOn(t *testing.T, srv *listServer, clock *fakeClock, fallback bool) (*TrustStore, error) {
	t.Helper()
	store, err := newTrustStore(TrustConfig{Source: TrustSourceEUTL, FallbackToPinned: fallback})
	if err != nil {
		t.Fatal(err)
	}
	store.httpClient, store.lotl, store.now = srv.Client(), srv.URL+"/lotl.xml", clock.Now
	return store, store.init(context.Background())
}

func TestTrustStoreBoot(t *testing.T) {
	srv := newListServer(t)
	srv.failing.Store(true)
	clock := &fakeClock{now: time.Now()}

	if _, err := trustStoreOn(t, srv, clock, false); !errors.Is(err, ErrTrustUnavailable) {
		t.Errorf("boot without the lists = %v, want ErrTrustUnavailable", err)
	}

	store, err := trustStoreOn(t, srv, clock, true)
	if err != nil {
		t.Fatalf("boot with the pinned fallback: %v", err)
	}
	if store.description != "pinned roots (EU trusted lists unavailable)" {
		t.Errorf("description = %q, want the pinned fallback", store.description)
	}

	// On the fallback the EU lists are retried after trustRetry, not trustRefresh.
	srv.failing.Store(false)
	clock.now = clock.now.Add(trustRetry)
	store.Current(context.Background())
	store.reloads.Wait()
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.loadedAt.IsZero() || store.description == "pinned roots (EU trusted lists unavailable)" {
		t.Errorf("after a successful retry: loadedAt = %v, description %q", store.loadedAt, store.description)
	}
}

// A failed reload keeps the anchors already loaded and is retried after
// trustRetry, not on every call.
func TestTrustStoreReload(t *testing.T) {
	srv := newListServer(t)
	clock := &fakeClock{now: time.Now()}
	store, err := trustStoreOn(t, srv, clock, false)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	loaded := store.Current(context.Background())

	srv.failing.Store(true)
	clock.now = clock.now.Add(trustRefresh)
	if got := store.Current(context.Background()); got != loaded {
		t.Error("Current waited for or swapped in a reload")
	}
	store.reloads.Wait()
	hits := srv.hits.Load()
	if got := store.Current(context.Background()); got != loaded {
		t.Error("a failed reload replaced the loaded anchors")
	}
	store.reloads.Wait()
	if srv.hits.Load() != hits {
		t.Error("a failed reload was retried before trustRetry")
	}

	srv.failing.Store(false)
	clock.now = clock.now.Add(trustRetry)
	store.Current(context.Background())
	store.reloads.Wait()
	if got := store.Current(context.Background()); got == loaded {
		t.Error("the retry after trustRetry did not load the lists anew")
	}
}

func TestPadesValidator(t *testing.T) {
	store, err := NewTrustStore(context.Background(), TrustConfig{Source: TrustSourcePinned})
	if err != nil {
		t.Fatal(err)
	}
	validator := NewPadesValidator(store, RevocationOffline)
	if err := validator.Ping(context.Background()); err != nil {
		t.Errorf("Ping = %v, want nil with anchors loaded", err)
	}
	verification, err := validator.Validate(context.Background(), []byte("%PDF-1.7\n"))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if verification.Valid || verification.Key() != verify.CheckSignaturePresent {
		t.Errorf("Validate(unsigned) = %+v, want a %s rejection", verification, verify.CheckSignaturePresent)
	}
}
