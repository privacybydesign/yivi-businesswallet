package proofing

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/ratelimit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
)

var HostedCallLimit = ratelimit.Limit{Burst: 300, Per: time.Minute}

// hostedPagePrefix is the SPA route of a hosted link's page, /p/:token.
const hostedPagePrefix = "/p/"

// frameNone is the CSP that lets no page frame the hosted page.
const frameNone = "frame-ancestors 'none'"

// PageHeaders sets the hosted page's Content-Security-Policy: only its
// customer's allowed origins may frame it. An unknown or throttled link, or a
// failed read, may be framed by none.
func (h *Handler) PageHeaders(r *http.Request, header http.Header) {
	token, ok := strings.CutPrefix(r.URL.Path, hostedPagePrefix)
	if !ok || token == "" || strings.Contains(token, "/") {
		return
	}
	header.Set("Content-Security-Policy", h.framePolicy(r, token))
}

func (h *Handler) framePolicy(r *http.Request, token string) string {
	hash := sha256.Sum256([]byte(token))
	if ok, _ := h.hostedCalls.Allow(hex.EncodeToString(hash[:])); !ok {
		return frameNone
	}
	origins, err := h.service.HostedEmbedOrigins(r.Context(), token)
	if err != nil {
		if !errors.Is(err, ErrRequestNotFound) && !errors.Is(err, ErrCustomerNotFound) {
			slog.ErrorContext(r.Context(), "proofing: hosted page frame origins", slog.Any("error", err))
		}
		return frameNone
	}
	if len(origins) == 0 {
		return frameNone
	}
	return "frame-ancestors " + strings.Join(origins, " ")
}

func (h *Handler) registerHosted(mux *http.ServeMux) {
	mux.Handle("GET /proof/{token}", h.limitHosted(h.hostedView))
	mux.Handle("GET /proof/{token}/status", h.limitHosted(h.hostedStatus))
	mux.Handle("POST /proof/{token}/start", h.limitHosted(h.hostedStart))
	mux.Handle("POST /proof/{token}/decline", h.limitHosted(h.hostedDecline))
	mux.Handle("GET /proof/{token}/logo", h.limitHosted(h.hostedLogo))
	mux.Handle("POST /proof/{token}/yivi/start", h.limitHosted(h.hostedStartYivi))
	mux.Handle("POST /proof/{token}/claim-link", h.limitHosted(h.hostedClaimLink))
	mux.Handle("GET /proof/{token}/yivi/disclosure", h.limitHosted(h.hostedYiviDisclosure))
	mux.Handle("POST /proof/{token}/yivi/face", h.limitHosted(h.hostedFaceFrame))
}

func (h *Handler) limitHosted(next respond.HandlerFunc) respond.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		hash := sha256.Sum256([]byte(r.PathValue("token")))
		if ok, wait := h.hostedCalls.Allow(hex.EncodeToString(hash[:])); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
			return &respond.APIError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "too many requests for this link; retry after the Retry-After seconds"}
		}
		return next(w, r)
	}
}

type hostedProgressResponse struct {
	Status        Status     `json:"status"`
	ErrorCode     string     `json:"errorCode,omitempty"`
	Method        string     `json:"method,omitempty"`
	LinkExpiresAt time.Time  `json:"linkExpiresAt"`
	Started       bool       `json:"started"`
	CompletedAt   *time.Time `json:"completedAt,omitempty"`
}

func newHostedProgress(req Request, now time.Time) hostedProgressResponse {
	expires := req.LinkExpiresAt
	if at := req.SessionExpiresAt(now); at != nil {
		expires = *at
	}
	return hostedProgressResponse{
		Status: req.EffectiveStatus(now), ErrorCode: req.ErrorCode, Method: string(req.Method),
		LinkExpiresAt: expires, Started: req.session != nil, CompletedAt: req.CompletedAt,
	}
}

type hostedViewResponse struct {
	hostedProgressResponse
	// SessionID is the customer's id for the session, handed back on completion.
	SessionID string `json:"sessionId"`
	// RedirectURL is where the page sends its subject once settled; absent
	// shows the page's own done screen. EmbedOrigins are the origins that may
	// frame the page and receive its completion message.
	RedirectURL  string   `json:"redirectUrl,omitempty"`
	EmbedOrigins []string `json:"embedOrigins"`
	// Language is the page's language (en/nl); absent leaves the browser's.
	// Locales are the languages the flow's page offers; empty is every one.
	Language string         `json:"language,omitempty"`
	Locales  []email.Locale `json:"locales"`
	Customer struct {
		Name              string           `json:"name"`
		Branding          brandingResponse `json:"branding"`
		DataRetentionDays int              `json:"dataRetentionDays"`
	} `json:"customer"`
	Flow struct {
		Name                   string   `json:"name"`
		RequiredAssuranceLevel string   `json:"requiredAssuranceLevel,omitempty"`
		RequestedAttributes    []string `json:"requestedAttributes"`
		YiviAvailable          bool     `json:"yiviAvailable"`
	} `json:"flow"`
}

func (h *Handler) hostedView(w http.ResponseWriter, r *http.Request) error {
	token := r.PathValue("token")
	hosted, err := h.service.HostedRequest(r.Context(), token)
	if err != nil {
		return mapError(err)
	}
	var out hostedViewResponse
	out.hostedProgressResponse = newHostedProgress(hosted.Request, time.Now())
	out.SessionID = PublicSessionID(hosted.Request.ID)
	out.RedirectURL, out.Language = hosted.Request.RedirectURL, string(hosted.Request.Language)
	c := hosted.Customer
	out.EmbedOrigins = append([]string{}, c.RedirectOrigins...)
	out.Locales = append([]email.Locale{}, hosted.Settings.Locales...)
	out.Customer.Name = c.SignedAs()
	out.Customer.Branding = brandingResponse{
		DisplayName: c.Branding.DisplayName, PrimaryColor: c.Branding.PrimaryColor,
		SupportContact: c.Branding.SupportContact, PrivacyURL: c.Branding.PrivacyURL,
		HidePoweredBy: c.Branding.HidePoweredBy,
	}
	if c.Branding.HasLogo {
		out.Customer.Branding.LogoURI = fmt.Sprintf("/api/v1/proof/%s/logo?v=%d", url.PathEscape(token), c.UpdatedAt.Unix())
	}
	out.Customer.DataRetentionDays = c.Settings.DataRetentionDays
	f := hosted.Flow
	out.Flow.Name, out.Flow.RequiredAssuranceLevel = f.Name, f.RequiredAssuranceLevel
	out.Flow.RequestedAttributes = f.RequestedAttributes
	if out.Flow.RequestedAttributes == nil {
		out.Flow.RequestedAttributes = []string{}
	}
	out.Flow.YiviAvailable = YiviAppAvailable(f)
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *Handler) hostedStatus(w http.ResponseWriter, r *http.Request) error {
	req, err := h.service.HostedStatus(r.Context(), r.PathValue("token"))
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newHostedProgress(req, time.Now()))
	return nil
}

type hostedStartRequest struct {
	Method proofingprovider.Method `json:"method"`
}

// hostedStartResponse is the started session, with an Idem session's deep link.
type hostedStartResponse struct {
	hostedProgressResponse
	DeepLink string `json:"deepLink,omitempty"`
}

func (h *Handler) hostedStart(w http.ResponseWriter, r *http.Request) error {
	var body hostedStartRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	sent, err := h.service.StartHosted(r.Context(), r.PathValue("token"), body.Method)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusCreated, hostedStartResponse{
		hostedProgressResponse: newHostedProgress(sent.Request, time.Now()), DeepLink: sent.DeepLink,
	})
	return nil
}

// hostedDecline cancels the link: its subject declined what is collected.
func (h *Handler) hostedDecline(w http.ResponseWriter, r *http.Request) error {
	req, err := h.service.DeclineHosted(r.Context(), r.PathValue("token"))
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newHostedProgress(req, time.Now()))
	return nil
}

func (h *Handler) hostedLogo(w http.ResponseWriter, r *http.Request) error {
	logo, err := h.service.HostedLogo(r.Context(), r.PathValue("token"))
	if err != nil {
		return mapError(err)
	}
	themesettings.SetLogoResponseHeaders(w.Header(), logo.ContentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(logo.Bytes); err != nil {
		// The status is committed; a failed write can only be logged.
		slog.ErrorContext(r.Context(), "proofing: write hosted logo", slog.Any("error", err))
	}
	return nil
}

func (h *Handler) hostedStartYivi(w http.ResponseWriter, r *http.Request) error {
	started, err := h.service.HostedStartYivi(r.Context(), r.PathValue("token"))
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, yiviStartResponse(started))
	return nil
}

func (h *Handler) hostedClaimLink(w http.ResponseWriter, r *http.Request) error {
	claim, err := h.service.HostedClaimLink(r.Context(), r.PathValue("token"))
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, claimLinkResponse{DeepLink: claim.DeepLink, ExpiresAt: claim.ExpiresAt})
	return nil
}

func (h *Handler) hostedYiviDisclosure(w http.ResponseWriter, r *http.Request) error {
	disclosure, err := h.service.HostedYiviDisclosure(r.Context(), r.PathValue("token"))
	return writeYiviDisclosure(w, r, disclosure, err)
}

func (h *Handler) hostedFaceFrame(w http.ResponseWriter, r *http.Request) error {
	image, err := decodeFaceFrame(w, r)
	if err != nil {
		return err
	}
	verdict, err := h.service.HostedFaceFrame(r.Context(), r.PathValue("token"), image)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, newFaceVerdictResponse(verdict))
	return nil
}
