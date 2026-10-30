// Package idscan is a client of CM.com's ID Scan: a hosted page where a person
// photographs their identity document in the browser. The wallet creates a
// transaction, sends the person to its page, and reads the photos back once
// the page returns them to the wallet.
package idscan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DefaultBaseURL is CM.com's ID Scan API.
const DefaultBaseURL = "https://api.cm.com/id-scan/v1"

const (
	// tokenLifetime is how long one call's bearer token holds.
	tokenLifetime = 5 * time.Minute
	// maxResponseBytes caps a JSON answer; maxImageBytes one document photo.
	maxResponseBytes = 1 << 20
	maxImageBytes    = 10 << 20

	taskDocumentScan = "document_scan"
	captureCamera    = "camera"
	// fieldPersonalNumber is the field the BSN is printed in on a Dutch
	// document, as redactFields names it.
	fieldPersonalNumber = "personal_number"

	// stateCompleted is a transaction's and a task's state once it is done.
	stateCompleted = "completed"
	// fileCategoryDocument is a photo of the document as ID Scan hands it out:
	// cut to the document, and with the redactFields covered. The raw
	// camera images (fileTypeRaw) come only with includeRawImages, which is
	// never asked, and are skipped anyway, as they show the BSN.
	fileCategoryDocument = "document"
	fileTypeRaw          = "raw_document_image"
	// fileSideB is how ID Scan names a document's second side ("Netherlands -
	// Driving License (2022) Side B"); fileSideBack is kept for a name that
	// says back.
	fileSideB    = "side b"
	fileSideBack = "back"

	headerAuthorization = "Authorization"
	headerContentType   = "Content-Type"
	mediaTypeJSON       = "application/json"
)

// ID Scan's recommended reading of a document_scan task's score, its
// confidence the document is genuine: below scoreDoubtful fake, from
// scoreAuthentic authentic, doubtful (manual review) between. scoreUnknown is
// ID Scan unable to tell.
const (
	scoreDoubtful  = 0.40
	scoreAuthentic = 0.90
	scoreUnknown   = -1
)

// Authenticity is ID Scan's judgement of the document, from its score.
type Authenticity string

const (
	Authentic Authenticity = "authentic"
	// Doubtful is a score between fake and authentic, or none ID Scan could
	// give: a person decides.
	Doubtful Authenticity = "doubtful"
	Fake     Authenticity = "fake"
)

// authenticity reads a document_scan task's score; no score is doubtful.
func authenticity(score *float64) Authenticity {
	switch {
	case score == nil || *score == scoreUnknown:
		return Doubtful
	case *score >= scoreAuthentic:
		return Authentic
	case *score < scoreDoubtful:
		return Fake
	}
	return Doubtful
}

// Locales ID Scan's page speaks; LocaleEnglish is its default.
const (
	LocaleDutch   = "nl-NL"
	LocaleEnglish = "en-US"
)

// ErrNoDocumentImage is a completed scan whose result holds no document photo.
var ErrNoDocumentImage = errors.New("idscan: the result holds no document photo")

// imageTypes are the photo types a document image may be, sniffed from its
// bytes.
var imageTypes = []string{"image/jpeg", "image/png"}

// Client calls ID Scan with an API key: its id (the token's kid) and secret.
type Client struct {
	keyID   string
	secret  []byte
	baseURL string
	http    *http.Client
	now     func() time.Time
}

// New builds a client of the ID Scan API at baseURL.
func New(keyID, secret, baseURL string, httpClient *http.Client) *Client {
	return &Client{keyID: keyID, secret: []byte(secret), baseURL: strings.TrimSuffix(baseURL, "/"), http: httpClient, now: time.Now}
}

// Transaction is a started scan: its id and the page the person scans on.
type Transaction struct {
	ID  string
	URL string
}

// Image is one photo of the document.
type Image struct {
	MimeType string
	Data     []byte
}

// Result is a scan's outcome: whether the person finished it, ID Scan's
// judgement of the document, and the document's front and, for a document
// that has one, its back.
type Result struct {
	Completed    bool
	Authenticity Authenticity
	// Score is ID Scan's confidence the document is genuine, 0 to 1 (-1 when
	// it could not tell); nil when it gave none.
	Score *float64
	Front Image
	Back  *Image
}

type createRequest struct {
	Tasks    []string       `json:"tasks"`
	Locale   string         `json:"locale"`
	Settings createSettings `json:"settings"`
}

type createSettings struct {
	DocumentScan documentScanSettings `json:"documentScan"`
}

type documentScanSettings struct {
	CaptureSources   []string `json:"captureSources"`
	RequireBothSides bool     `json:"requireBothSides"`
	RedactFields     []string `json:"redactFields,omitempty"`
}

type createResponse struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// CreateTransaction starts a document scan in locale, whose page the wallet
// embeds: it posts the parent page the transaction's id, state and result id
// when done. Only the camera counts, so no stored picture stands in for the
// document; both sides are asked of a document that has two. redactBSN
// covers the personal number (a Dutch document's BSN) in the photos.
func (c *Client) CreateTransaction(ctx context.Context, locale string, redactBSN bool) (Transaction, error) {
	settings := documentScanSettings{CaptureSources: []string{captureCamera}, RequireBothSides: true}
	if redactBSN {
		settings.RedactFields = []string{fieldPersonalNumber}
	}
	body, err := json.Marshal(createRequest{
		Tasks: []string{taskDocumentScan}, Locale: locale,
		Settings: createSettings{DocumentScan: settings},
	})
	if err != nil {
		return Transaction{}, fmt.Errorf("idscan: encode transaction: %w", err)
	}
	var out createResponse
	if err := c.call(ctx, http.MethodPost, c.baseURL+"/transactions", body, http.StatusCreated, &out); err != nil {
		return Transaction{}, fmt.Errorf("idscan: create transaction: %w", err)
	}
	if out.ID == "" || out.URL == "" {
		return Transaction{}, errors.New("idscan: create transaction: no id or url in the answer")
	}
	return Transaction(out), nil
}

type resultResponse struct {
	State string `json:"state"`
	Tasks []struct {
		Type  string   `json:"type"`
		State string   `json:"state"`
		Score *float64 `json:"score"`
		Files []file   `json:"files"`
	} `json:"tasks"`
}

type file struct {
	Category string `json:"category"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	URL      string `json:"url"`
}

// Result reads transactionID's result resultID, the ids the page's return URL
// carries, and downloads the document's photos once it completed.
func (c *Client) Result(ctx context.Context, transactionID, resultID string) (Result, error) {
	var out resultResponse
	target := c.baseURL + "/transactions/" + url.PathEscape(transactionID) + "/results/" + url.PathEscape(resultID)
	if err := c.call(ctx, http.MethodGet, target, nil, http.StatusOK, &out); err != nil {
		return Result{}, fmt.Errorf("idscan: result of transaction %s: %w", transactionID, err)
	}
	var files []file
	completed, scanned := out.State == stateCompleted, Authenticity("")
	var score *float64
	for _, task := range out.Tasks {
		if task.Type != taskDocumentScan {
			continue
		}
		completed = completed && task.State == stateCompleted
		scanned, score = authenticity(task.Score), task.Score
		for _, f := range task.Files {
			if f.Category == fileCategoryDocument && f.Type != fileTypeRaw {
				files = append(files, f)
			}
		}
	}
	if !completed {
		return Result{}, nil
	}
	front, back := sides(files)
	if front == nil {
		return Result{}, ErrNoDocumentImage
	}
	res := Result{Completed: true, Authenticity: scanned, Score: score}
	var err error
	if res.Front, err = c.download(ctx, *front); err != nil {
		return Result{}, err
	}
	if back != nil {
		img, err := c.download(ctx, *back)
		if err != nil {
			return Result{}, err
		}
		res.Back = &img
	}
	return res, nil
}

// sides picks the front and back photo out of a result's document files: the
// one named as side B (or the back) is the back, else they come front first.
func sides(files []file) (front, back *file) {
	var rest []file
	for _, f := range files {
		name := strings.ToLower(f.Name)
		if back == nil && (strings.Contains(name, fileSideB) || strings.Contains(name, fileSideBack)) {
			back = &f
			continue
		}
		rest = append(rest, f)
	}
	if len(rest) > 0 {
		front = &rest[0]
	}
	if back == nil && len(rest) > 1 {
		back = &rest[1]
	}
	return front, back
}

// download fetches one file. Its url is the API's own: the bearer token never
// goes to another host.
func (c *Client) download(ctx context.Context, f file) (Image, error) {
	if !c.sameHost(f.URL) {
		return Image{}, fmt.Errorf("idscan: file url %q is not on the API's host", f.URL)
	}
	resp, err := c.do(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return Image{}, fmt.Errorf("idscan: download file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Image{}, fmt.Errorf("idscan: download file: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return Image{}, fmt.Errorf("idscan: download file: %w", err)
	}
	if len(data) > maxImageBytes {
		return Image{}, fmt.Errorf("idscan: download file: over %d bytes", maxImageBytes)
	}
	mime := http.DetectContentType(data)
	for _, t := range imageTypes {
		if mime == t {
			return Image{MimeType: mime, Data: data}, nil
		}
	}
	return Image{}, fmt.Errorf("idscan: download file: %s is not a photo", mime)
}

func (c *Client) sameHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	base, err := url.Parse(c.baseURL)
	return err == nil && u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host)
}

// call sends one JSON request and decodes its answer into out.
func (c *Client) call(ctx context.Context, method, target string, body []byte, wantStatus int, out any) error {
	resp, err := c.do(ctx, method, target, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return err
	}
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("status %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode answer: %w", err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, target string, body []byte) (*http.Response, error) {
	token, err := c.token()
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set(headerAuthorization, "Bearer "+token)
	if body != nil {
		req.Header.Set(headerContentType, mediaTypeJSON)
	}
	return c.http.Do(req)
}

// token is a fresh bearer token: an HS256 JWT, kid the key id, signed with
// the secret.
func (c *Client) token() (string, error) {
	now := c.now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(tokenLifetime)),
	})
	token.Header["kid"] = c.keyID
	signed, err := token.SignedString(c.secret)
	if err != nil {
		return "", fmt.Errorf("idscan: sign token: %w", err)
	}
	return signed, nil
}
