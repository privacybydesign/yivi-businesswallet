// Package regula is a client for the Regula Face API endpoints the native
// face step uses: confirm a liveness transaction the app ran with the Regula
// SDK, match it against a reference portrait (taking the live face's crop as
// the selfie), and delete it. Ported from go-passport-issuer's
// face_verification_client.go.
package regula

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
)

// DefaultMatchThreshold is the similarity a match must reach when none is configured.
const DefaultMatchThreshold = 0.75

// DefaultTimeout bounds every request to the Face API.
const DefaultTimeout = 30 * time.Second

// Regula ImageSource types (see the Face SDK OpenAPI ImageSource enum).
const (
	imageSourceDocumentRFID = 2 // portrait read from a document chip (DG2)
	imageSourceLive         = 3 // the live face of a liveness transaction
)

// Image indexes in a match request: the reference first, the live face second.
const (
	referenceImageIndex = 1
	liveImageIndex      = 2
)

// alignType3x4 is FaceImageQualityAlignType ALIGN_3x4: a crop in a passport
// photo's aspect ratio.
const alignType3x4 = 0

// ErrTransactionNotFound is returned when Regula does not know the transaction id.
var ErrTransactionNotFound = errors.New("regula: liveness transaction not found")

// LivenessTransaction is the part of GET /api/v2/liveness's TransactionInfo we use.
type LivenessTransaction struct {
	// Status is 0 when liveness is confirmed.
	Status int `json:"status"`
	// Code is the raw FaceSDKResultCode.
	Code int `json:"code"`
	// Tag is the tag the client set when it started the liveness session.
	Tag string `json:"tag"`
}

// Confirmed reports whether Regula judged the captured face live.
func (t LivenessTransaction) Confirmed() bool { return t.Status == 0 }

// Client talks to one Regula Face API instance.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New returns a client for baseURL (e.g. http://face-api:41101).
func New(baseURL string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: DefaultTimeout},
	}
}

// GetLiveness fetches a liveness transaction via GET /api/v2/liveness.
func (c *Client) GetLiveness(ctx context.Context, transactionID string) (LivenessTransaction, error) {
	var tx LivenessTransaction
	resp, err := c.do(ctx, http.MethodGet, c.livenessURL(transactionID), nil)
	if err != nil {
		return tx, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return tx, ErrTransactionNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return tx, statusError("liveness status", resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&tx); err != nil {
		return tx, fmt.Errorf("regula: decode liveness status: %w", err)
	}
	return tx, nil
}

// MatchResult is a match's similarity (0..1) and LiveCrop, the live face
// Regula detected as an aligned 3:4 crop (plain base64), "" when it found
// none. The crop is the only copy of the live face that outlives the
// transaction's deletion.
type MatchResult struct {
	Similarity float64
	LiveCrop   string
}

// Match compares referenceBase64 (a plain, non-data-URL base64 image read
// from a chip) with the live face of transactionID via POST /api/match,
// asking Regula for the detected faces' crops.
func (c *Client) Match(ctx context.Context, referenceBase64, transactionID string) (MatchResult, error) {
	body, err := json.Marshal(map[string]any{
		"images": []map[string]any{
			{"type": imageSourceDocumentRFID, "data": referenceBase64, "index": referenceImageIndex},
			{"type": imageSourceLive, "livenessTransactionId": transactionID, "index": liveImageIndex},
		},
		"outputImageParams": map[string]any{"crop": map[string]any{"type": alignType3x4}},
	})
	if err != nil {
		return MatchResult{}, fmt.Errorf("regula: marshal match request: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, c.baseURL+"/api/match", body)
	if err != nil {
		return MatchResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return MatchResult{}, statusError("match", resp)
	}
	var out struct {
		Results []struct {
			Similarity float64 `json:"similarity"`
		} `json:"results"`
		Detections []struct {
			ImageIndex int `json:"imageIndex"`
			Faces      []struct {
				Crop string `json:"crop"`
			} `json:"faces"`
		} `json:"detections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return MatchResult{}, fmt.Errorf("regula: decode match response: %w", err)
	}
	var res MatchResult
	for _, d := range out.Detections {
		if d.ImageIndex == liveImageIndex && len(d.Faces) > 0 {
			res.LiveCrop = d.Faces[0].Crop
		}
	}
	// No result pair means no comparable face: a non-match, as in the issuer.
	if len(out.Results) > 0 {
		res.Similarity = out.Results[0].Similarity
	}
	return res, nil
}

// DeleteLiveness removes a liveness transaction (portrait, video, metadata)
// via DELETE /api/v2/liveness.
func (c *Client) DeleteLiveness(ctx context.Context, transactionID string) error {
	resp, err := c.do(ctx, http.MethodDelete, c.livenessURL(transactionID), nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return statusError("liveness delete", resp)
	}
	return nil
}

// DeleteLivenessByTag removes every liveness transaction started with tag
// via DELETE /api/v2/liveness?tag= (Face SDK 7.1+): the attempts a session's
// app ran, including those whose id never reached the server. A tag with no
// transactions left (404) is already clean.
func (c *Client) DeleteLivenessByTag(ctx context.Context, tag string) error {
	if tag == "" {
		return errors.New("regula: delete by tag needs a tag")
	}
	resp, err := c.do(ctx, http.MethodDelete, c.baseURL+"/api/v2/liveness?tag="+url.QueryEscape(tag), nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil
	default:
		return statusError("liveness delete by tag", resp)
	}
}

func (c *Client) livenessURL(transactionID string) string {
	return c.baseURL + "/api/v2/liveness?transactionId=" + url.QueryEscape(transactionID)
}

func (c *Client) do(ctx context.Context, method, target string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, fmt.Errorf("regula: build %s request: %w", method, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// *url.Error prints the full URL, whose query holds the transaction id.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("regula: %s %s: %w", method, req.URL.Path, err)
	}
	return resp, nil
}

func statusError(what string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("regula: %s failed with status %d: %s", what, resp.StatusCode, strings.TrimSpace(string(body)))
}

// ImageMatch is one image-to-image comparison: whether a face was found in
// the live image at all, and how similar it is to the reference.
type ImageMatch struct {
	LiveFaceDetected bool
	Similarity       float64
}

// MatchImages compares two plain base64 images via POST /api/match, the
// reference as a document portrait and the second as a live camera frame:
// the Yivi method's per-frame face check, which has no liveness transaction.
func (c *Client) MatchImages(ctx context.Context, referenceBase64, liveBase64 string) (ImageMatch, error) {
	body, err := json.Marshal(map[string]any{
		"images": []map[string]any{
			{"type": imageSourceDocumentRFID, "data": referenceBase64, "index": referenceImageIndex},
			{"type": imageSourceLive, "data": liveBase64, "index": liveImageIndex},
		},
	})
	if err != nil {
		return ImageMatch{}, fmt.Errorf("regula: marshal match request: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, c.baseURL+"/api/match", body)
	if err != nil {
		return ImageMatch{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ImageMatch{}, statusError("match", resp)
	}
	var out struct {
		Results []struct {
			Similarity float64 `json:"similarity"`
		} `json:"results"`
		Detections []struct {
			ImageIndex int               `json:"imageIndex"`
			Faces      []json.RawMessage `json:"faces"`
		} `json:"detections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ImageMatch{}, fmt.Errorf("regula: decode match response: %w", err)
	}
	var res ImageMatch
	for _, d := range out.Detections {
		if d.ImageIndex == liveImageIndex && len(d.Faces) > 0 {
			res.LiveFaceDetected = true
		}
	}
	if len(out.Results) > 0 {
		res.Similarity = out.Results[0].Similarity
	}
	return res, nil
}
