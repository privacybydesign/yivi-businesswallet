package yivicsc

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/clientip"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/ratelimit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

const (
	// maxRequestBody bounds a CSC or token request: each is a few short fields.
	maxRequestBody = 1 << 20

	headerAuthorization = "Authorization"
	headerAccept        = "Accept"
	mediaTypeJSON       = "application/json"
	mediaTypeHTML       = "text/html"
	headerCacheControl  = "Cache-Control"
	headerRetryAfter    = "Retry-After"
	cacheNoStore        = "no-store"
	bearerPrefix        = "Bearer "
)

// authorizeLimit is how many authorizations one source may start. The endpoint
// takes no credential and each call opens a presentation at the verifier, so it
// is held per client address; a signer starts a handful in a sitting.
var authorizeLimit = ratelimit.Limit{Burst: 30, Per: time.Minute}

// Handler serves the service over HTTP: the OAuth2 endpoints, the CSC API, and
// the authorization page's own two calls.
type Handler struct {
	svc       *Service
	authorize *ratelimit.Limiter
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, authorize: ratelimit.New(authorizeLimit)}
}

func (h *Handler) Register(mux *http.ServeMux) {
	// The CSC API and the OAuth2 endpoints: called by a signing application.
	mux.Handle("POST /yivi-csc/csc/v2/info", respond.HandlerFunc(h.info))
	mux.HandleFunc("GET /yivi-csc/oauth2/authorize", h.oauthAuthorize)
	mux.HandleFunc("POST /yivi-csc/oauth2/token", h.oauthToken)
	mux.HandleFunc("POST /yivi-csc/csc/v2/credentials/list", h.credentialsList)
	mux.HandleFunc("POST /yivi-csc/csc/v2/credentials/info", h.credentialsInfo)
	mux.HandleFunc("POST /yivi-csc/csc/v2/signatures/signHash", h.signHash)

	// The authorization page: called by the signer's browser, which holds
	// nothing but the unguessable authorization id it was redirected to.
	mux.Handle("GET /yivi-csc/authorizations/{id}", respond.HandlerFunc(h.getAuthorization))
	mux.Handle("POST /yivi-csc/authorizations/{id}/cancel", respond.HandlerFunc(h.cancelAuthorization))
}

func (h *Handler) info(w http.ResponseWriter, r *http.Request) error {
	respond.JSON(w, r, http.StatusOK, h.svc.Info())
	return nil
}

// authorizeStarted is what oauthAuthorize answers a caller that asked for JSON:
// the wallet's own page, which shows the authorization itself instead of being
// sent to the authorization page. AuthorizationID is absent when the
// authorization could not be started; RedirectURL is then the client's callback
// carrying the error, and otherwise the authorization page.
type authorizeStarted struct {
	AuthorizationID string `json:"authorizationId,omitempty"`
	RedirectURL     string `json:"redirectUrl"`
}

// wantsJSON reports whether the caller is a page's own request and not a
// browser navigating: it accepts JSON and does not accept a document.
func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get(headerAccept)
	return strings.Contains(accept, mediaTypeJSON) && !strings.Contains(accept, mediaTypeHTML)
}

// oauthAuthorize starts an authorization and sends the browser to its page, or
// back to the client with an error. Only a client or callback the service does
// not know is answered here, since there is nowhere safe to send that.
func (h *Handler) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	if ok, wait := h.authorize.Allow(clientip.SourceKey(r)); !ok {
		w.Header().Set(headerRetryAfter, strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		respond.Error(w, r, http.StatusTooManyRequests, "rate_limited", "too many authorizations; retry after the Retry-After seconds")
		return
	}
	q := r.URL.Query()
	location, err := h.svc.Authorize(r.Context(), AuthorizeRequest{
		ResponseType:        q.Get("response_type"),
		ClientID:            q.Get("client_id"),
		RedirectURI:         q.Get("redirect_uri"),
		State:               q.Get("state"),
		CodeChallenge:       q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"),
		Scope:               q.Get("scope"),
		CredentialID:        q.Get("credentialID"),
		NumSignatures:       q.Get("numSignatures"),
		Hashes:              q.Get("hashes"),
		HashAlgorithmOID:    q.Get("hashAlgorithmOID"),
		Description:         q.Get("description"),
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set(headerCacheControl, cacheNoStore)
	if wantsJSON(r) {
		respond.JSON(w, r, http.StatusOK, authorizeStarted{AuthorizationID: h.svc.pageAuthorizationID(location), RedirectURL: location})
		return
	}
	http.Redirect(w, r, location, http.StatusFound)
}

func (h *Handler) oauthToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	if err := r.ParseForm(); err != nil {
		h.writeError(w, r, badRequest(errInvalidRequest, "the token request is not a form"))
		return
	}
	req := TokenRequest{
		GrantType:    r.PostForm.Get("grant_type"),
		Code:         r.PostForm.Get("code"),
		RedirectURI:  r.PostForm.Get("redirect_uri"),
		CodeVerifier: r.PostForm.Get("code_verifier"),
		ClientID:     r.PostForm.Get("client_id"),
		ClientSecret: r.PostForm.Get("client_secret"),
	}
	// client_secret_basic carries both parts form-urlencoded (RFC 6749 §2.3.1).
	if id, secret, ok := r.BasicAuth(); ok {
		req.ClientID, req.ClientSecret = unescape(id), unescape(secret)
	}
	token, err := h.svc.Token(req)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set(headerCacheControl, cacheNoStore)
	respond.JSON(w, r, http.StatusOK, token)
}

func unescape(s string) string {
	if out, err := url.QueryUnescape(s); err == nil {
		return out
	}
	return s
}

type credentialsListResponse struct {
	CredentialIDs []string `json:"credentialIDs"`
}

func (h *Handler) credentialsList(w http.ResponseWriter, r *http.Request) {
	// The body's filters (credentialInfo, onlyValid) change nothing here: a
	// subject has one credential and an expired one is replaced, not listed.
	ids, err := h.svc.ListCredentials(r.Context(), bearer(r))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	respond.JSON(w, r, http.StatusOK, credentialsListResponse{CredentialIDs: ids})
}

type credentialsInfoRequest struct {
	CredentialID string `json:"credentialID"`
	Certificates string `json:"certificates"`
}

func (h *Handler) credentialsInfo(w http.ResponseWriter, r *http.Request) {
	var body credentialsInfoRequest
	if err := decodeBody(w, r, &body); err != nil {
		h.writeError(w, r, err)
		return
	}
	info, err := h.svc.CredentialInfo(r.Context(), bearer(r), body.CredentialID, body.Certificates)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	respond.JSON(w, r, http.StatusOK, info)
}

type signHashResponse struct {
	Signatures []string `json:"signatures"`
}

func (h *Handler) signHash(w http.ResponseWriter, r *http.Request) {
	var body SignHashRequest
	if err := decodeBody(w, r, &body); err != nil {
		h.writeError(w, r, err)
		return
	}
	signatures, err := h.svc.SignHash(r.Context(), bearer(r), body)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	respond.JSON(w, r, http.StatusOK, signHashResponse{Signatures: signatures})
}

func (h *Handler) getAuthorization(w http.ResponseWriter, r *http.Request) error {
	view, err := h.svc.Authorization(r.Context(), r.PathValue("id"))
	if err != nil {
		return authorizationError(err)
	}
	w.Header().Set(headerCacheControl, cacheNoStore)
	respond.JSON(w, r, http.StatusOK, view)
	return nil
}

func (h *Handler) cancelAuthorization(w http.ResponseWriter, r *http.Request) error {
	view, err := h.svc.Cancel(r.PathValue("id"))
	if err != nil {
		return authorizationError(err)
	}
	w.Header().Set(headerCacheControl, cacheNoStore)
	respond.JSON(w, r, http.StatusOK, view)
	return nil
}

func authorizationError(err error) error {
	if errors.Is(err, ErrAuthorizationNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "authorization_not_found", Message: "this authorization is unknown or has expired"}
	}
	return err
}

// oauthErrorBody is how OAuth2 (RFC 6749 §5.2) and CSC write an error.
type oauthErrorBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// writeError answers a signing application in the shape its protocol defines.
// Anything that is not an *Error is ours: logged, and answered as server_error
// with nothing of the cause in it.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	oauthErr, ok := errors.AsType[*Error](err)
	if !ok {
		slog.ErrorContext(r.Context(), "yivicsc: request failed", slog.Any("error", err))
		oauthErr = &Error{Status: http.StatusInternalServerError, Code: errServerError, Description: "the signing service failed"}
	}
	w.Header().Set(headerCacheControl, cacheNoStore)
	respond.JSON(w, r, oauthErr.Status, oauthErrorBody{Error: oauthErr.Code, ErrorDescription: oauthErr.Description})
}

func bearer(r *http.Request) string {
	token, _ := strings.CutPrefix(r.Header.Get(headerAuthorization), bearerPrefix)
	return strings.TrimSpace(token)
}

func decodeBody(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	if err := json.NewDecoder(r.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return badRequest(errInvalidRequest, "the request body is not valid JSON")
	}
	return nil
}
