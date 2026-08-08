package signingprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoverParsesOAuthIssuer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/csc/v2/info" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"name":"Demo QTSP","specs":"2.2.0.0","oauth2":"https://as.example/oauth2"}`))
	}))
	defer srv.Close()

	info, err := NewClient().Discover(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if info.OAuth2 != "https://as.example/oauth2" || info.Name != "Demo QTSP" {
		t.Fatalf("unexpected info: %+v", info)
	}
}

// TestErrorRedactsURLAndBody ensures a non-2xx failure repeats only the status
// code — never the base URL or any byte the far side wrote.
func TestErrorRedactsURLAndBody(t *testing.T) {
	const secret = "SECRET-TOKEN-LEAK"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"` + secret + `"}`))
	}))
	defer srv.Close()

	_, err := NewClient().ListCredentials(context.Background(), srv.URL, "bearer-"+secret)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "500") {
		t.Fatalf("reason should name the status code, got %q", msg)
	}
	if strings.Contains(msg, secret) || strings.Contains(msg, srv.URL) {
		t.Fatalf("error leaked the URL or a far-side byte: %q", msg)
	}
}

func TestErrorRedactsUnreachableURL(t *testing.T) {
	// A server we immediately close, so the request fails at transport (where
	// net/http would otherwise embed the URL in the error).
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	_, err := NewClient().Discover(context.Background(), url)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), url) {
		t.Fatalf("error leaked the URL: %q", err.Error())
	}
}

func TestAuthorizeURLBindsHashesForCredentialScope(t *testing.T) {
	c := NewClient()
	got := c.AuthorizeURL("https://as.example", AuthorizeParams{
		ClientID: "cid", RedirectURI: "https://rp/cb", State: "st", CodeChallenge: "ch",
		Scope: ScopeCredential, CredentialID: "cred1", NumSignatures: 1,
		Hashes: []string{"aGFzaA=="}, HashAlgorithmOID: HashAlgoSHA256OID,
	})
	for _, want := range []string{"response_type=code", "scope=credential", "code_challenge_method=S256", "credentialID=cred1", "hashAlgorithmOID=", "hashes=aGFzaA"} {
		if !strings.Contains(got, want) {
			t.Errorf("authorize URL missing %q: %s", want, got)
		}
	}
}
