package signing

import (
	"context"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// The signing API's side of the service: what the organisation's own software
// does with one of its API keys. It makes a request as a member would, and then
// reads only the requests made that way: a request a member made in the wallet is
// not one a key can reach, the same split the identity proofing API keeps between
// external sessions and members' own.

// maxReturnURLLength bounds the return page a caller names.
const maxReturnURLLength = 2000

// Query parameters the return page is opened with, so the caller's site knows
// which request the signee came back from and how it went for them.
const (
	returnParamRequest = "request_id"
	returnParamStatus  = "status"
)

// APIRequestInput is a signing request as the API takes it.
type APIRequestInput struct {
	Filename  string
	PDF       []byte
	Signers   []SignerInput
	Mode      string
	Recipient RecipientInput
	// SendMail false mails no signee: the caller hands each their signing link.
	SendMail bool
	// ReturnURL is where a signee's signing page sends them back to; empty for none.
	ReturnURL string
}

// CreateAPIRequest makes a signing request for the organisation's own software. It
// returns the request and, when no mail was asked for, the signing link of every
// external signee who may sign now, keyed by signer id.
func (s *Service) CreateAPIRequest(ctx context.Context, orgID, apiKeyID uuid.UUID, slug string, in APIRequestInput) (Request, map[uuid.UUID]string, error) {
	returnURL, err := normalizeReturnURL(in.ReturnURL)
	if err != nil {
		return Request{}, nil, err
	}
	origin := APIOrigin(apiKeyID, in.SendMail, returnURL)
	id, links, err := s.createRequest(ctx, orgID, origin, slug, in.Filename, in.PDF, in.Signers, in.Mode, in.Recipient)
	if err != nil {
		return Request{}, nil, err
	}
	req, err := s.GetAPIRequest(ctx, orgID, id)
	if err != nil {
		return Request{}, nil, err
	}
	return req, links, nil
}

// GetAPIRequest returns a request made over the API; any other is ErrNotFound.
func (s *Service) GetAPIRequest(ctx context.Context, orgID, id uuid.UUID) (Request, error) {
	req, err := s.store.GetRequest(ctx, orgID, id)
	if err != nil {
		return Request{}, err
	}
	if req.CreatedVia != CreatedViaAPI {
		return Request{}, ErrNotFound
	}
	s.enrich(ctx, orgID, []*Request{&req})
	return req, nil
}

// ListAPIRequests pages the requests made over the API, newest first.
func (s *Service) ListAPIRequests(ctx context.Context, orgID uuid.UUID, cursor string, limit int) ([]Request, string, error) {
	reqs, next, err := s.store.ListAPIRequests(ctx, orgID, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	s.enrichSlice(ctx, orgID, reqs)
	return reqs, next, nil
}

// GetAPIDocument returns the signed PDF of a completed request made over the API.
func (s *Service) GetAPIDocument(ctx context.Context, orgID, id uuid.UUID) ([]byte, string, error) {
	if _, err := s.GetAPIRequest(ctx, orgID, id); err != nil {
		return nil, "", err
	}
	return s.store.GetSignedDocument(ctx, orgID, id)
}

// IssueSignerLink makes a fresh signing link for an external signee of a request
// made over the API, for the caller to hand to them. It is refused for a signee
// who cannot sign now (signed already, not their turn, the request over), and it
// replaces the signee's earlier link.
func (s *Service) IssueSignerLink(ctx context.Context, orgID, requestID, signerID uuid.UUID) (string, error) {
	req, err := s.GetAPIRequest(ctx, orgID, requestID)
	if err != nil {
		return "", err
	}
	signer := signerByID(req, signerID)
	if signer == nil || signer.Kind != KindExternal {
		return "", ErrNotSigner
	}
	if err := checkTurn(req, signerID); err != nil {
		return "", err
	}
	if req.Status != StatusAwaitingSignatures {
		return "", ErrInvalidRequest
	}
	return s.externalLink(ctx, requestID, signerID)
}

// normalizeReturnURL checks the page a caller wants signees sent back to: an
// absolute https URL (http only for localhost, a developer's own machine) with a
// host and no credentials in it. The page shows it as a button the signee
// presses, never a redirect they did not ask for.
func normalizeReturnURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > maxReturnURLLength {
		return "", ErrInvalidRequest
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return "", ErrInvalidRequest
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && isLoopbackHost(u.Hostname()):
	default:
		return "", ErrInvalidRequest
	}
	return u.String(), nil
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// returnLink is a request's return page for one signee: the caller's URL with
// the request and how it went for this signee added to its query. Empty when the
// request names no return page.
func returnLink(req Request, signer Signer) string {
	if req.ReturnURL == "" {
		return ""
	}
	u, err := url.Parse(req.ReturnURL)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set(returnParamRequest, req.ID.String())
	q.Set(returnParamStatus, signer.Status)
	u.RawQuery = q.Encode()
	return u.String()
}
