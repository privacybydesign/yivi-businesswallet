package vog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"
)

// ResponseCode is validatie.nl's own outcome code for one submitted PDF
// ({"response_code": n} in its JSON reply). 0 means authentic; the rest are
// go-vog-issuer's empirically confirmed mapping (#242) - the published
// API-specificatie GAAV v1.0 code table is garbled, so this is not re-derived
// independently here.
type ResponseCode int

// ResponseAuthentic is the one code that means the submitted PDF is a genuine,
// current VOG.
const ResponseAuthentic ResponseCode = 0

// finalRejectionCodes are terminal: the document is not a genuine, current VOG,
// and resubmitting the same bytes will not change the answer. The remaining
// non-zero codes (3, 4, 5, 7) are retryable, alongside any transport/5xx error.
var finalRejectionCodes = map[ResponseCode]bool{1: true, 2: true, 6: true}

// Authentic reports whether the document validated as a genuine, current VOG.
func (c ResponseCode) Authentic() bool { return c == ResponseAuthentic }

// Final reports a terminal rejection: the document is not a valid VOG, and
// retrying will not change that.
func (c ResponseCode) Final() bool { return c != ResponseAuthentic && finalRejectionCodes[c] }

// Retryable reports a transient answer worth another attempt.
func (c ResponseCode) Retryable() bool { return c != ResponseAuthentic && !finalRejectionCodes[c] }

// Validator checks one PDF against a real-time document-authenticity service
// (validatie.nl / GAAV, run by Justis). The concrete implementation is chosen by
// config: HTTPClient in production, a stub in dev/CI - the same external-provider
// seam as internal/registryprovider and internal/qerdsprovider.
type Validator interface {
	Validate(ctx context.Context, pdf []byte) (ResponseCode, error)
}

const (
	maxAttempts      = 3
	attemptTimeout   = 30 * time.Second
	maxResponseBytes = 1 << 16
)

// retryBackoff is the delay before each retry (go-vog-issuer's confirmed
// 1s/2s cadence for a 3-attempt total).
var retryBackoff = []time.Duration{1 * time.Second, 2 * time.Second}

// HTTPClient is the real validatie.nl driver: an anonymous, unauthenticated
// POST with no session, cookie or captcha (confirmed by go-vog-issuer, #242).
type HTTPClient struct {
	baseURL string
	http    *http.Client
}

// NewHTTPClient builds a validatie.nl client. baseURL is the full "valideer"
// endpoint (https://validatie.nl/api/valideer/ in production).
func NewHTTPClient(baseURL string, httpClient *http.Client) *HTTPClient {
	return &HTTPClient{baseURL: baseURL, http: httpClient}
}

// Ping is a boot-time reachability check. validatie.nl exposes no health
// endpoint - it is a single anonymous POST-only API - so this GETs the same URL
// and accepts 405 Method Not Allowed as healthy (it proves the route exists and
// the service answered) alongside a plain 2xx; anything else (unreachable, 404,
// 5xx) fails the boot the same way every other provider's Ping does.
func (c *HTTPClient) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL, nil)
	if err != nil {
		return fmt.Errorf("vog: build ping request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("vog: ping validatie.nl: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusMethodNotAllowed {
		return nil
	}
	return fmt.Errorf("vog: ping validatie.nl: unexpected status %d", resp.StatusCode)
}

// Validate submits pdf and returns validatie.nl's response code, retrying a
// transient answer (a retryable code, or a transport/5xx error) up to
// maxAttempts times with retryBackoff between attempts. A decisive answer -
// authentic or a final rejection - returns immediately without retrying.
func (c *HTTPClient) Validate(ctx context.Context, pdf []byte) (ResponseCode, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(retryBackoff[attempt-1]):
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}

		code, err := c.post(ctx, pdf)
		switch {
		case err != nil:
			lastErr = err
		case code.Retryable():
			lastErr = fmt.Errorf("vog: validatie.nl retryable response code %d", code)
		default:
			return code, nil
		}
	}
	return 0, fmt.Errorf("vog: validatie.nl validate: %w", lastErr)
}

// post makes one attempt. The part's Content-Type must be explicitly
// "application/pdf" - go-vog-issuer's confirmed gotcha (#242): with Go's default
// "application/octet-stream", GAAV answers a genuine VOG with code 2 (unknown
// document).
func (c *HTTPClient) post(ctx context.Context, pdf []byte) (ResponseCode, error) {
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="document.pdf"`)
	header.Set("Content-Type", "application/pdf")
	part, err := w.CreatePart(header)
	if err != nil {
		return 0, fmt.Errorf("vog: build request: %w", err)
	}
	if _, err := part.Write(pdf); err != nil {
		return 0, fmt.Errorf("vog: build request: %w", err)
	}
	if err := w.Close(); err != nil {
		return 0, fmt.Errorf("vog: build request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, &body)
	if err != nil {
		return 0, fmt.Errorf("vog: build request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("vog: validatie.nl request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 == 5 {
		return 0, fmt.Errorf("vog: validatie.nl status %d", resp.StatusCode)
	}

	var out struct {
		ResponseCode int `json:"response_code"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return 0, fmt.Errorf("vog: decode validatie.nl response: %w", err)
	}
	return ResponseCode(out.ResponseCode), nil
}

// StubValidator is the dev/CI default: no outbound call, a canned answer set by
// whoever constructs it (seed data, tests). Code defaults to ResponseAuthentic
// (the zero value), so a bare StubValidator{} accepts every upload.
type StubValidator struct {
	Code ResponseCode
	Err  error
}

func (s StubValidator) Validate(context.Context, []byte) (ResponseCode, error) {
	return s.Code, s.Err
}

func (StubValidator) Ping(context.Context) error { return nil }
