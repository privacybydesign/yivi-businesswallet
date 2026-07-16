package postguard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
)

const (
	sendPath   = "/v1/send"
	healthPath = "/healthz"
	// bodyLimit caps the sidecar response we read.
	bodyLimit = 1 << 20
)

// Client talks to the internal PostGuard sidecar over HTTP. It presents the
// shared secret as a bearer token on every request; the sidecar is never
// reachable by clients directly. baseURL is empty when the feature is not
// configured, in which case Send returns ErrNotConfigured.
type Client struct {
	baseURL string
	secret  string
	http    *http.Client
}

func NewClient(baseURL, secret string, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), secret: secret, http: httpClient}
}

// sendRequest is the wire request to the sidecar.
type sendRequest struct {
	APIKey     string
	Recipients []string
	Files      []FileBlob
	Notify     bool
	Message    string
}

// Send encrypts and uploads the files via the sidecar, returning the cryptify
// UUID. Errors are mapped to package sentinels so the handler can translate them.
func (c *Client) Send(ctx context.Context, req sendRequest) (string, error) {
	if c.baseURL == "" {
		return "", ErrNotConfigured
	}

	body, contentType, err := buildMultipart(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+sendPath, body)
	if err != nil {
		return "", fmt.Errorf("postguard: build send request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Authorization", "Bearer "+c.secret)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrSidecar, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return "", mapSidecarStatus(resp)
	}

	var decoded struct {
		UUID string `json:"uuid"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, bodyLimit)).Decode(&decoded); err != nil {
		return "", fmt.Errorf("postguard: decode sidecar response: %w", err)
	}
	if decoded.UUID == "" {
		return "", fmt.Errorf("%w: sidecar response missing uuid", ErrSidecar)
	}
	return decoded.UUID, nil
}

// Ping checks the sidecar is reachable (health endpoint). Unauthenticated.
func (c *Client) Ping(ctx context.Context) error {
	if c.baseURL == "" {
		return ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+healthPath, nil)
	if err != nil {
		return fmt.Errorf("postguard: build ping request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSidecar, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%w: health status %d", ErrSidecar, resp.StatusCode)
	}
	return nil
}

func buildMultipart(req sendRequest) (*bytes.Buffer, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	recipients, err := json.Marshal(req.Recipients)
	if err != nil {
		return nil, "", fmt.Errorf("postguard: marshal recipients: %w", err)
	}
	fields := map[string]string{
		"apiKey":     req.APIKey,
		"recipients": string(recipients),
		"notify":     strconv.FormatBool(req.Notify),
	}
	if req.Message != "" {
		fields["message"] = req.Message
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return nil, "", fmt.Errorf("postguard: write field %s: %w", k, err)
		}
	}

	for _, f := range req.Files {
		part, err := mw.CreateFormFile("file", f.Name)
		if err != nil {
			return nil, "", fmt.Errorf("postguard: create file part: %w", err)
		}
		if _, err := part.Write(f.Data); err != nil {
			return nil, "", fmt.Errorf("postguard: write file part: %w", err)
		}
	}

	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("postguard: close multipart: %w", err)
	}
	return &buf, mw.FormDataContentType(), nil
}

// mapSidecarStatus turns a non-2xx sidecar response into a package sentinel.
func mapSidecarStatus(resp *http.Response) error {
	var errBody struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, bodyLimit)).Decode(&errBody)

	switch resp.StatusCode {
	case http.StatusRequestEntityTooLarge:
		return ErrPayloadTooLarge
	case http.StatusBadRequest:
		// A 400 from the sidecar means we sent something malformed — treat as a
		// sidecar failure (our bug), not a client error.
		return fmt.Errorf("%w: %s", ErrSidecar, errBody.Message)
	default:
		return fmt.Errorf("%w: status %d: %s", ErrSidecar, resp.StatusCode, errBody.Message)
	}
}
