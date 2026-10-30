package idscan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testKeyID  = "key-1"
	testSecret = "secret"
)

// pngBytes is a 1×1 PNG, a photo as the scan service hands one out.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// checkToken fails t unless r carries an HS256 bearer token with the key id,
// signed with the secret.
func checkToken(t *testing.T, r *http.Request) {
	t.Helper()
	raw, ok := strings.CutPrefix(r.Header.Get(headerAuthorization), "Bearer ")
	if !ok {
		t.Errorf("%s %s: no bearer token", r.Method, r.URL.Path)
		return
	}
	token, err := jwt.Parse(raw, func(token *jwt.Token) (any, error) {
		if token.Header["kid"] != testKeyID {
			return nil, errors.New("wrong kid")
		}
		return []byte(testSecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !token.Valid {
		t.Errorf("%s %s: token: %v", r.Method, r.URL.Path, err)
	}
}

func TestCreateTransaction(t *testing.T) {
	for name, redact := range map[string]bool{"redacts the BSN": true, "keeps the BSN": false} {
		t.Run(name, func(t *testing.T) {
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				checkToken(t, r)
				if r.Method != http.MethodPost || r.URL.Path != "/transactions" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("decode: %v", err)
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":"trx-1","state":"pending","url":"https://scan.example/s/1"}`))
			}))
			t.Cleanup(srv.Close)

			trx, err := New(testKeyID, testSecret, srv.URL+"/", srv.Client()).CreateTransaction(context.Background(), LocaleDutch, redact)
			if err != nil {
				t.Fatal(err)
			}
			if trx != (Transaction{ID: "trx-1", URL: "https://scan.example/s/1"}) {
				t.Errorf("transaction = %+v", trx)
			}
			// Embedded, the page posts its parent when done: no return page.
			if _, hasReturn := got["returnUrl"]; got["locale"] != LocaleDutch || hasReturn {
				t.Errorf("body = %v", got)
			}
			scan := got["settings"].(map[string]any)["documentScan"].(map[string]any)
			if scan["requireBothSides"] != true {
				t.Errorf("documentScan = %v, want both sides", scan)
			}
			if _, has := scan["redactFields"]; has != redact {
				t.Errorf("documentScan = %v, redactFields present %v, want %v", scan, has, redact)
			}
		})
	}
}

func TestCreateTransactionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	if _, err := New(testKeyID, testSecret, srv.URL, srv.Client()).CreateTransaction(context.Background(), LocaleEnglish, true); err == nil {
		t.Fatal("CreateTransaction succeeded on a 401")
	}
}

// resultServer answers a result with body ({base} the server's own URL) and
// serves photo at /downloads/{name}, with the name appended so each file
// reads apart.
func resultServer(t *testing.T, body string, photo []byte) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkToken(t, r)
		switch {
		case r.URL.Path == "/transactions/trx-1/results/res-1":
			_, _ = w.Write([]byte(strings.ReplaceAll(body, "{base}", srv.URL)))
		case strings.HasPrefix(r.URL.Path, "/downloads/"):
			_, _ = w.Write(append(slices.Clone(photo), strings.TrimPrefix(r.URL.Path, "/downloads/")...))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestResult(t *testing.T) {
	photo := pngBytes(t)
	// As ID Scan names them: the raw images unnamed, the second side "Side B".
	const completed = `{"id":"res-1","state":"completed","tasks":[{"type":"document_scan","state":"completed","score":0.97,"files":[
		{"category":"document","type":"raw_document_image","url":"{base}/downloads/raw"},
		{"category":"document","type":"DRIVING_LICENSE","name":"Netherlands - Driving License (2022) Side B","url":"{base}/downloads/back"},
		{"category":"document","type":"DRIVING_LICENSE","name":"Netherlands - Driving License (2022)","url":"{base}/downloads/front"}]}]}`
	srv := resultServer(t, completed, photo)

	res, err := New(testKeyID, testSecret, srv.URL, srv.Client()).Result(context.Background(), "trx-1", "res-1")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Completed || res.Authenticity != Authentic || res.Back == nil {
		t.Fatalf("result = completed %v, %q, back %v; want completed, authentic, with a back", res.Completed, res.Authenticity, res.Back != nil)
	}
	if res.Front.MimeType != "image/png" || !bytes.HasSuffix(res.Front.Data, []byte("front")) {
		t.Errorf("front = %s, %q…", res.Front.MimeType, res.Front.Data[len(res.Front.Data)-5:])
	}
	if !bytes.HasSuffix(res.Back.Data, []byte("back")) {
		t.Error("back is not the Side B file")
	}
}

// ID Scan's score reads as its documented ranges; none, or -1, is doubtful.
func TestAuthenticity(t *testing.T) {
	score := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		score *float64
		want  Authenticity
	}{
		{score(0.97), Authentic},
		{score(0.90), Authentic},
		{score(0.89), Doubtful},
		{score(0.40), Doubtful},
		{score(0.39), Fake},
		{score(-1), Doubtful},
		{nil, Doubtful},
	} {
		if got := authenticity(tc.score); got != tc.want {
			t.Errorf("authenticity(%v) = %q, want %q", tc.score, got, tc.want)
		}
	}
}

func TestResultNotCompleted(t *testing.T) {
	srv := resultServer(t, `{"state":"failed","tasks":[{"type":"document_scan","state":"failed","files":[]}]}`, nil)
	res, err := New(testKeyID, testSecret, srv.URL, srv.Client()).Result(context.Background(), "trx-1", "res-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Completed {
		t.Errorf("result = %+v, want not completed", res)
	}
}

func TestResultWithoutPhoto(t *testing.T) {
	srv := resultServer(t, `{"state":"completed","tasks":[{"type":"document_scan","state":"completed","files":[]}]}`, nil)
	_, err := New(testKeyID, testSecret, srv.URL, srv.Client()).Result(context.Background(), "trx-1", "res-1")
	if !errors.Is(err, ErrNoDocumentImage) {
		t.Fatalf("err = %v, want ErrNoDocumentImage", err)
	}
}

// A file on another host never gets the bearer token.
func TestResultRefusesForeignFileHost(t *testing.T) {
	const foreign = `{"state":"completed","tasks":[{"type":"document_scan","state":"completed","files":[
		{"category":"document","type":"PASSPORT","url":"https://elsewhere.example/downloads/front"}]}]}`
	srv := resultServer(t, foreign, nil)
	if _, err := New(testKeyID, testSecret, srv.URL, srv.Client()).Result(context.Background(), "trx-1", "res-1"); err == nil {
		t.Fatal("Result downloaded from another host")
	}
}

func TestResultRefusesNonPhoto(t *testing.T) {
	const one = `{"state":"completed","tasks":[{"type":"document_scan","state":"completed","files":[
		{"category":"document","type":"PASSPORT","url":"{base}/downloads/front"}]}]}`
	srv := resultServer(t, one, []byte("<html>not a photo</html>"))
	if _, err := New(testKeyID, testSecret, srv.URL, srv.Client()).Result(context.Background(), "trx-1", "res-1"); err == nil {
		t.Fatal("Result took a non-photo")
	}
}
