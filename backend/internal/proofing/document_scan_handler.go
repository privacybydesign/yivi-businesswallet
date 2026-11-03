package proofing

import (
	"net/http"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// documentScanStartRequest is the language the scan service's page speaks.
type documentScanStartRequest struct {
	Locale string `json:"locale"`
}

type documentScanStartResponse struct {
	// URL is the scan service's page the person photographs the document on,
	// embedded in the wallet's page.
	URL string `json:"url"`
}

// documentScanFinishRequest is what the embedded scan service's page posted
// its parent when done: the transaction's id and its result's.
type documentScanFinishRequest struct {
	TransactionID string `json:"transactionId"`
	ResultID      string `json:"resultId"`
}

func (h *Handler) startDocumentScan(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)
	var body documentScanStartRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	url, err := h.service.StartDocumentScan(r.Context(), orgFromRequest(r).ID, id, requestedBy, body.Locale)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, documentScanStartResponse{URL: url})
	return nil
}

func (h *Handler) finishDocumentScan(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)
	var body documentScanFinishRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	if err := h.service.FinishDocumentScan(r.Context(), orgFromRequest(r).ID, id, requestedBy, body.TransactionID, body.ResultID); err != nil {
		return mapError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) hostedStartDocumentScan(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)
	var body documentScanStartRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	url, err := h.service.HostedStartDocumentScan(r.Context(), r.PathValue("token"), body.Locale)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, documentScanStartResponse{URL: url})
	return nil
}

func (h *Handler) hostedFinishDocumentScan(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)
	var body documentScanFinishRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	if err := h.service.HostedFinishDocumentScan(r.Context(), r.PathValue("token"), body.TransactionID, body.ResultID); err != nil {
		return mapError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// documentScanState is a request's document photo as its page reads it:
// pending until the photos reached the engine, done after; empty when its
// flow takes none.
func documentScanState(req Request) string {
	switch {
	case !req.DocumentScan:
		return ""
	case req.DocumentScannedAt != nil:
		return documentScanDone
	}
	return documentScanPending
}

const (
	documentScanPending = "pending"
	documentScanDone    = "done"
)

// chooseMethodRequest is the app the subject picked once the document photo
// is in.
type chooseMethodRequest struct {
	Method proofingprovider.Method `json:"method"`
}

func (h *Handler) chooseMethod(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)
	var body chooseMethodRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	sent, err := h.service.ChooseMethod(r.Context(), orgFromRequest(r).ID, id, requestedBy, body.Method)
	if err != nil {
		return mapError(err)
	}
	out := createRequestResponse{requestResponse: newRequestResponse(sent.Request, time.Now())}
	if sent.DeepLink != "" {
		out.DeepLink, out.DeepLinkExpiresAt = sent.DeepLink, &sent.DeepLinkExpiresAt
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *Handler) hostedChooseMethod(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)
	var body chooseMethodRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	sent, err := h.service.HostedChooseMethod(r.Context(), r.PathValue("token"), body.Method)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, hostedStartResponse{
		hostedProgressResponse: newHostedProgress(sent.Request, time.Now()), DeepLink: sent.DeepLink,
	})
	return nil
}
