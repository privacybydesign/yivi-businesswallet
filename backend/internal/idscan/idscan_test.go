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

// testMinimumAge is a flow's age check, which ID Scan enforces.
const testMinimumAge = 18

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

			trx, err := New(testKeyID, testSecret, srv.URL+"/", srv.Client()).CreateTransaction(context.Background(), LocaleDutch, ScanOptions{RedactBSN: redact, MinimumAge: testMinimumAge})
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
			// A Dutch passport has its BSN on the back: only a desired
			// personal number makes ID Scan ask for it.
			desired, _ := scan["desiredFields"].([]any)
			if scan["requireBothSides"] != true || len(desired) != 1 || desired[0] != fieldPersonalNumber {
				t.Errorf("documentScan = %v, want both sides and the personal number desired", scan)
			}
			// The BSN is printed as the personal number, and on an older
			// Dutch document in the MRZ too. The signature goes with it.
			redacted, has := scan["redactFields"].([]any)
			if has != redact || has && !slices.Equal(redacted, []any{fieldPersonalNumber, fieldMRZ, fieldSignature}) {
				t.Errorf("documentScan = %v, want redactFields %v", scan, redact)
			}
			// ID Scan enforces the flow's minimum age. allowedExpiration and
			// validateFields stay out: with them it stops recognising documents.
			_, expiration := scan["allowedExpiration"]
			_, validate := scan["validateFields"]
			if scan["minimumAge"] != float64(testMinimumAge) || expiration || validate {
				t.Errorf("documentScan = %v, want minimum age %d, no allowedExpiration or validateFields", scan, testMinimumAge)
			}
		})
	}
}

func TestCreateTransactionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	if _, err := New(testKeyID, testSecret, srv.URL, srv.Client()).CreateTransaction(context.Background(), LocaleEnglish, ScanOptions{RedactBSN: true}); err == nil {
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
	const completed = `{"id":"res-1","state":"completed","tasks":[{"type":"document_scan","state":"completed","score":0.97,
		"result":[
			{"field":"document_number","validity":"valid","comparison":"match","values":[{"source":"visual","value":"XR1234567"},{"source":"mrz","value":"XR1234567"}]},
			{"field":"surname","validity":"not_checked","comparison":"not_performed","values":[{"source":"visual","value":"De Bruijn"}]},
			{"field":"given_names","validity":"not_checked","comparison":"match","values":[{"source":"mrz","value":"WILLEKE LISELOTTE"},{"source":"visual","value":"Willeke Liselotte"}]},
			{"field":"date_of_birth","validity":"valid","comparison":"match","values":[{"source":"mrz","value":"1990-03-10"}]},
			{"field":"date_of_expiry","validity":"valid","comparison":"no_match","values":[{"source":"visual","value":"14.07.2031"}]},
			{"field":"issuing_state_code","validity":"valid","comparison":"not_performed","values":[{"source":"mrz","value":"NLD"}]}],
		"files":[
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
	// The MRZ's value first, dates as YYYY-MM-DD; one zone disagreeing is
	// the zones not matching, every validated field valid is the fields valid.
	want := Document{
		Type: "driving_license", IssuingState: "NLD", Number: "XR1234567", Surname: "De Bruijn", GivenNames: "WILLEKE LISELOTTE",
		DateOfBirth: "1990-03-10", DateOfExpiry: "2031-07-14",
	}
	if res.Document != want {
		t.Errorf("document = %+v, want %+v", res.Document, want)
	}
	if res.FieldsValid == nil || !*res.FieldsValid || res.ZonesMatch == nil || *res.ZonesMatch {
		t.Errorf("fields valid %v, zones match %v; want valid, not matching", res.FieldsValid, res.ZonesMatch)
	}
	if res.Front.MimeType != "image/png" || !bytes.HasSuffix(res.Front.Data, []byte("front")) {
		t.Errorf("front = %s, %q…", res.Front.MimeType, res.Front.Data[len(res.Front.Data)-5:])
	}
	if !bytes.HasSuffix(res.Back.Data, []byte("back")) {
		t.Error("back is not the Side B file")
	}
}

// A passport photographed open comes as its data page and the facing page:
// the facing page is no back.
func TestResultPassportFacingPageIsNoBack(t *testing.T) {
	const spread = `{"state":"completed","tasks":[{"type":"document_scan","state":"completed","files":[
		{"category":"document","type":"PASSPORT","name":"Netherlands - Passport (2021)","url":"{base}/downloads/front"},
		{"category":"document","type":"PASSPORT","name":"Netherlands - Passport (2021)","url":"{base}/downloads/facing"}]}]}`
	srv := resultServer(t, spread, pngBytes(t))
	res, err := New(testKeyID, testSecret, srv.URL, srv.Client()).Result(context.Background(), "trx-1", "res-1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(res.Front.Data, []byte("front")) || res.Back != nil {
		t.Errorf("front %q…, back %v; want the data page and no back", res.Front.Data[len(res.Front.Data)-5:], res.Back != nil)
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

// An invalid field is the fields not valid; without any validated or
// compared field neither is known.
func TestFieldsAgree(t *testing.T) {
	validity := func(f resultField) string { return f.Validity }
	if got := fieldsAgree([]resultField{{Validity: validityValid}, {Validity: validityInvalid}}, validity, validityValid, validityInvalid); got == nil || *got {
		t.Errorf("with an invalid field = %v, want false", got)
	}
	if got := fieldsAgree([]resultField{{Validity: "not_checked"}}, validity, validityValid, validityInvalid); got != nil {
		t.Errorf("with nothing validated = %v, want nil", *got)
	}
	if isoDate("31/12/2030") != "2030-12-31" || isoDate("next year") != "" {
		t.Error("isoDate does not read a slashed date or refuse an unknown form")
	}
}
