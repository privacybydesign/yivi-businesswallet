package eutl

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A self-signed test certificate is enough to exercise the parser.
const testCertPEM = `-----BEGIN CERTIFICATE-----
MIIBhTCCASsCFDuiD5ERbJ/ZrODbC2ZdBD8h6NoxMAoGCCqGSM49BAMCMEUxCzAJ
BgNVBAYTAkFVMRMwEQYDVQQIDApTb21lLVN0YXRlMSEwHwYDVQQKDBhJbnRlcm5l
dCBXaWRnaXRzIFB0eSBMdGQwHhcNMjQwMTAxMDAwMDAwWhcNMzQwMTAxMDAwMDAw
WjBFMQswCQYDVQQGEwJBVTETMBEGA1UECAwKU29tZS1TdGF0ZTEhMB8GA1UECgwY
SW50ZXJuZXQgV2lkZ2l0cyBQdHkgTHRkMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcD
QgAEJ6yMrAcbJdMh3ICSVRK1ow+RmqDRGq0o8Pyn3ogGoUMdSk1O3vlXc7sK9qyt
uPTNzr5EmsMiQ81E7Wm6KskShjAKBggqhkjOPQQDAgNIADBFAiEA5xi3wl3PsUyO
IGGXsphmgXX/gWPAI9j+SwyN4BpUd90CIDPCYoDGOEhbfmHeY/CvJl7yJBNnC5rz
j8zD7r64yAeb
-----END CERTIFICATE-----`

func mustCertB64(t *testing.T) (string, *x509.Certificate) {
	t.Helper()
	blk, _ := pem.Decode([]byte(testCertPEM))
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Skipf("test certificate unusable: %v", err)
	}
	return base64.StdEncoding.EncodeToString(blk.Bytes), c
}

func TestLoadParsesLOTLAndMembers(t *testing.T) {
	certB64, cert := mustCertB64(t)
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/lotl.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<tsl:TrustServiceStatusList xmlns:tsl="http://uri.etsi.org/02231/v2#" xmlns:add="http://uri.etsi.org/02231/v2/additionaltypes#">
 <tsl:SchemeInformation>
  <tsl:SchemeTerritory>EU</tsl:SchemeTerritory>
  <tsl:PointersToOtherTSL>
   <tsl:OtherTSLPointer>
    <tsl:TSLLocation>` + srv.URL + `/nl.xml</tsl:TSLLocation>
    <tsl:AdditionalInformation>
     <tsl:OtherInformation><tsl:SchemeTerritory>NL</tsl:SchemeTerritory></tsl:OtherInformation>
     <tsl:OtherInformation><add:MimeType>application/vnd.etsi.tsl+xml</add:MimeType></tsl:OtherInformation>
    </tsl:AdditionalInformation>
   </tsl:OtherTSLPointer>
   <tsl:OtherTSLPointer>
    <tsl:TSLLocation>` + srv.URL + `/nl.pdf</tsl:TSLLocation>
    <tsl:AdditionalInformation>
     <tsl:OtherInformation><tsl:SchemeTerritory>NL</tsl:SchemeTerritory></tsl:OtherInformation>
     <tsl:OtherInformation><add:MimeType>application/pdf</add:MimeType></tsl:OtherInformation>
    </tsl:AdditionalInformation>
   </tsl:OtherTSLPointer>
  </tsl:PointersToOtherTSL>
 </tsl:SchemeInformation>
</tsl:TrustServiceStatusList>`))
	})
	mux.HandleFunc("/nl.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<TrustServiceStatusList xmlns="http://uri.etsi.org/02231/v2#">
 <SchemeInformation><SchemeTerritory>NL</SchemeTerritory></SchemeInformation>
 <TrustServiceProviderList>
  <TrustServiceProvider>
   <TSPInformation><TSPName><Name xml:lang="nl">KPN B.V.</Name><Name xml:lang="en">KPN B.V.</Name></TSPName></TSPInformation>
   <TSPServices>
    <TSPService><ServiceInformation>
     <ServiceTypeIdentifier>http://uri.etsi.org/TrstSvc/Svctype/CA/QC</ServiceTypeIdentifier>
     <ServiceName><Name xml:lang="en">Test CA</Name></ServiceName>
     <ServiceDigitalIdentity><DigitalId><X509Certificate>` + certB64 + `</X509Certificate></DigitalId></ServiceDigitalIdentity>
     <ServiceStatus>http://uri.etsi.org/TrstSvc/TrustedList/Svcstatus/granted</ServiceStatus>
    </ServiceInformation></TSPService>
    <TSPService><ServiceInformation>
     <ServiceTypeIdentifier>http://uri.etsi.org/TrstSvc/Svctype/TSA/QTST</ServiceTypeIdentifier>
     <ServiceName><Name xml:lang="en">Withdrawn TSA</Name></ServiceName>
     <ServiceDigitalIdentity><DigitalId><X509Certificate>` + certB64 + `</X509Certificate></DigitalId></ServiceDigitalIdentity>
     <ServiceStatus>http://uri.etsi.org/TrstSvc/TrustedList/Svcstatus/withdrawn</ServiceStatus>
    </ServiceInformation></TSPService>
   </TSPServices>
  </TrustServiceProvider>
 </TrustServiceProviderList>
</TrustServiceStatusList>`))
	})
	srv = httptest.NewTLSServer(mux)
	defer srv.Close()

	store, err := Load(context.Background(), Options{LOTL: srv.URL + "/lotl.xml", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Loaded) != 1 || store.Loaded[0] != "NL" {
		t.Errorf("loaded %v, want [NL]", store.Loaded)
	}
	if len(store.Services) != 2 {
		t.Fatalf("got %d services, want 2", len(store.Services))
	}
	svc, ok := store.Lookup(cert)
	if !ok || svc.Provider != "KPN B.V." || svc.Name != "Test CA" || svc.Type != SvcCAQC {
		t.Errorf("lookup: %+v ok=%v", svc, ok)
	}
	if !strings.Contains(store.Describe(cert), "EU trusted list NL: KPN B.V.") {
		t.Errorf("describe: %q", store.Describe(cert))
	}
	// The withdrawn TSA must not become an anchor; the pool should be empty.
	if store.TSARoots().Equal(store.SignerRoots()) {
		t.Error("TSA pool should differ from signer pool")
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: store.TSARoots(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err == nil {
		t.Error("withdrawn TSA service was accepted as an anchor")
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: store.SignerRoots(), CurrentTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		t.Errorf("granted CA service not accepted as anchor: %v", err)
	}
}

func TestCacheIsUsedWhenFresh(t *testing.T) {
	hits := 0
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/lotl.xml", func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`<TrustServiceStatusList xmlns="http://uri.etsi.org/02231/v2#"><SchemeInformation><SchemeTerritory>EU</SchemeTerritory>
<PointersToOtherTSL><OtherTSLPointer><TSLLocation>` + srv.URL + `/x.xml</TSLLocation><AdditionalInformation>
<OtherInformation><SchemeTerritory>XX</SchemeTerritory></OtherInformation><OtherInformation><MimeType>application/vnd.etsi.tsl+xml</MimeType></OtherInformation>
</AdditionalInformation></OtherTSLPointer></PointersToOtherTSL></SchemeInformation></TrustServiceStatusList>`))
	})
	mux.HandleFunc("/x.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<TrustServiceStatusList xmlns="http://uri.etsi.org/02231/v2#"><SchemeInformation><SchemeTerritory>XX</SchemeTerritory></SchemeInformation></TrustServiceStatusList>`))
	})
	srv = httptest.NewTLSServer(mux)
	defer srv.Close()
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		if _, err := Load(context.Background(), Options{LOTL: srv.URL + "/lotl.xml", HTTPClient: srv.Client(), CacheDir: dir}); err != nil {
			t.Fatal(err)
		}
	}
	if hits != 1 {
		t.Errorf("LOTL fetched %d times, want 1 (second run should hit cache)", hits)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("expected 2 cached files, got %d", len(entries))
	}
}

// liveEnv opts in to TestLiveNLAndRO, which downloads the real EU lists.
const liveEnv = "DIPLOMA_LIVE_EUTL"

// TestLiveNLAndRO hits the real EU infrastructure; skipped unless liveEnv is set.
func TestLiveNLAndRO(t *testing.T) {
	if os.Getenv(liveEnv) == "" {
		t.Skipf("downloads the EU trusted lists; set %s to run it", liveEnv)
	}
	store, err := Load(context.Background(), Options{Territories: []string{"NL", "RO"}, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range store.Services {
		if s.Territory == "NL" && strings.Contains(s.Name, "KPN BV PKIoverheid Organisatie Services CA - G3") && s.Type == SvcCAQC && s.Granted() {
			found = true
		}
	}
	if !found {
		t.Error("KPN PKIoverheid Organisatie Services CA - G3 not found as granted CA/QC on the NL list")
	}
}

// A trusted list download never follows a redirect off https: whoever sits on
// the plain-http path would choose the anchors.
func TestLoadRefusesHTTPRedirect(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the plain-http target was fetched")
	}))
	defer plain.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/lotl.xml", http.StatusFound)
	}))
	defer srv.Close()

	_, err := Load(context.Background(), Options{LOTL: srv.URL + "/lotl.xml", HTTPClient: srv.Client()})
	if !errors.Is(err, errInsecureRedirect) {
		t.Errorf("Load = %v, want errInsecureRedirect", err)
	}
}

// A cached list stands in for a failed download only while it is younger
// than maxStale.
func TestStaleCacheIsBounded(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	dir := t.TempDir()
	f := fetcher{opts: Options{HTTPClient: srv.Client(), CacheDir: dir, MaxAge: time.Hour}}
	url := srv.URL + "/lotl.xml"
	sum := sha256.Sum256([]byte(url))
	path := filepath.Join(dir, hex.EncodeToString(sum[:8])+".xml")
	if err := os.WriteFile(path, []byte("<cached/>"), cacheFilePerm); err != nil {
		t.Fatal(err)
	}

	stale := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	if body, _, err := f.get(context.Background(), url); err != nil || string(body) != "<cached/>" {
		t.Errorf("get with a 2h old cache = %q, %v; want the cached list", body, err)
	}

	tooOld := time.Now().Add(-maxStale - time.Hour)
	if err := os.Chtimes(path, tooOld, tooOld); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.get(context.Background(), url); err == nil {
		t.Error("get with a cache past maxStale succeeded, want the download error")
	}
}
