package proofing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

const linkTokenBytes = 32

var (
	ErrLinkStarted = errors.New("proofing: the link was started already")
	// ErrRedirectNotAllowed is a hosted request's redirect off the customer's
	// allowed origins.
	ErrRedirectNotAllowed = errors.New("proofing: the redirect is not on an allowed origin")
)

func newLinkToken() (string, []byte, error) {
	b := make([]byte, linkTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("proofing: link token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(raw))
	return raw, hash[:], nil
}

func (s *Service) SetHostedBaseURL(u string) { s.hostedBaseURL = u }

type Hosted struct {
	Request  Request
	Customer Customer
	Flow     proofingprovider.Flow
	// Settings is the flow's hosted page settings.
	Settings FlowHosted
}

// hostedRequest is the request behind a hosted link; ErrProofingPaused while
// its org's proofing is paused.
func (s *Service) hostedRequest(ctx context.Context, token string) (Request, error) {
	hash := sha256.Sum256([]byte(token))
	req, err := s.requests.GetByLinkToken(ctx, hash[:])
	if err != nil {
		return Request{}, err
	}
	if err := s.checkActive(ctx, req.OrganizationID); err != nil {
		return Request{}, err
	}
	return req, nil
}

// hostedLimitKey is whose rate limit a hosted link's calls count against:
// its customer, or its org for a link sent to a member.
func (s *Service) hostedLimitKey(ctx context.Context, token string) (uuid.UUID, error) {
	hash := sha256.Sum256([]byte(token))
	req, err := s.requests.GetByLinkToken(ctx, hash[:])
	if err != nil {
		return uuid.UUID{}, err
	}
	if req.CustomerID != nil {
		return *req.CustomerID, nil
	}
	return req.OrganizationID, nil
}

func (s *Service) HostedRequest(ctx context.Context, token string) (Hosted, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return Hosted{}, err
	}
	if req.CustomerID == nil {
		return Hosted{}, ErrRequestNotFound
	}
	customer, err := s.customers.Get(ctx, req.OrganizationID, *req.CustomerID)
	if err != nil {
		return Hosted{}, err
	}
	flow, err := s.hostedFlow(ctx, req)
	if err != nil {
		return Hosted{}, err
	}
	settings, err := s.flowHosted(ctx, req.OrganizationID, flow.ID)
	if err != nil {
		return Hosted{}, err
	}
	if req, err = s.readReconciled(ctx, req); err != nil {
		return Hosted{}, err
	}
	return Hosted{Request: req, Customer: customer, Flow: flow, Settings: settings}, nil
}

func (s *Service) hostedFlow(ctx context.Context, req Request) (proofingprovider.Flow, error) {
	tenant := orgTenant(req.OrganizationID)
	flows, err := s.ips.ListFlows(ctx, tenant)
	if err != nil {
		return proofingprovider.Flow{}, fmt.Errorf("proofing: list flows org %s: %w", req.OrganizationID, err)
	}
	i := slices.IndexFunc(flows, func(f proofingprovider.Flow) bool { return f.ID == req.FlowID })
	if i < 0 {
		return proofingprovider.Flow{}, ErrFlowNotFound
	}
	return flows[i], nil
}

func (s *Service) HostedStatus(ctx context.Context, token string) (Request, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return Request{}, err
	}
	return s.readReconciled(ctx, req)
}

func (s *Service) StartHosted(ctx context.Context, token string, method proofingprovider.Method) (Sent, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return Sent{}, err
	}
	return s.startHosted(ctx, req, method)
}

// Headless is a hosted request started from the customer's own UI: the app
// link it shows (AppLink for the Idem app, WalletLink for the Yivi app).
type Headless struct {
	Request    Request
	AppLink    string
	WalletLink string
	ExpiresAt  time.Time
}

// StartHeadless starts a customer's hosted request in method, as the subject
// picking an app on the hosted page would, for a customer building its own UI.
// A Yivi one's disclosure starts at once; its face check runs through the
// hosted link's routes. A request not created hosted is ErrNotHosted.
func (s *Service) StartHeadless(ctx context.Context, scope CustomerScope, id uuid.UUID, method proofingprovider.Method) (Headless, error) {
	req, err := s.requests.GetForCustomer(ctx, scope, id)
	if err != nil {
		return Headless{}, err
	}
	if !req.Hosted {
		return Headless{}, ErrNotHosted
	}
	sent, err := s.startHosted(ctx, req, method)
	if err != nil {
		return Headless{}, err
	}
	out := Headless{Request: sent.Request, AppLink: sent.DeepLink, ExpiresAt: sent.Request.session.ExpiresAt}
	if method == proofingprovider.MethodYivi {
		started, err := s.startYivi(ctx, sent.Request, sent.Request.session)
		if err != nil {
			return Headless{}, err
		}
		out.WalletLink = started.WalletLink
	}
	return out, nil
}

func (s *Service) startHosted(ctx context.Context, req Request, method proofingprovider.Method) (Sent, error) {
	if req.CancelledAt != nil || req.PurgedAt != nil {
		return Sent{}, ErrSessionOver
	}
	if !req.awaitingStart(s.now()) {
		if req.session != nil {
			return Sent{}, ErrLinkStarted
		}
		return Sent{}, ErrSessionOver
	}
	if method != proofingprovider.MethodIdem && method != proofingprovider.MethodYivi {
		return Sent{}, fmt.Errorf("%w: choose the Idem app or the Yivi app", ErrInvalidInput)
	}
	customer, err := s.customers.Get(ctx, req.OrganizationID, *req.CustomerID)
	if err != nil {
		return Sent{}, err
	}
	if customer.Paused() {
		return Sent{}, ErrCustomerPaused
	}
	flow, err := s.hostedFlow(ctx, req)
	if err != nil {
		return Sent{}, err
	}
	if !customerCompletable(flow) {
		return Sent{}, ErrFlowNotCompletable
	}
	if method == proofingprovider.MethodYivi && !yiviAppAvailable(flow) {
		return Sent{}, fmt.Errorf("%w: this flow's face provider only runs in the Idem app", ErrInvalidInput)
	}
	// The reference photo was held for this start; a flow that needs one
	// it lacks (the flow changed since the send) cannot run.
	var photo *proofingprovider.Image
	if flowNeedsReferencePhoto(flow) {
		if photo, err = s.requests.ReferencePhoto(ctx, req); err != nil {
			return Sent{}, err
		}
		if photo == nil {
			return Sent{}, ErrFlowNotCompletable
		}
	}
	ttl := SessionTTL
	if customer.Settings.SessionTTL != 0 {
		ttl = customer.Settings.SessionTTL
	}
	tenant := orgTenant(req.OrganizationID)
	sess, err := s.ips.CreateSession(ctx, tenant, proofingprovider.SessionInput{
		FlowID: flow.ID, ClientReference: req.ID.String(), TTL: ttl, Method: method,
		Language: string(req.Language), Retention: engineRetention(&customer, req.RetentionOverride), ReferencePhoto: photo,
	})
	if err != nil {
		return Sent{}, fmt.Errorf("proofing: create session request %s: %w", req.ID, err)
	}
	if method == proofingprovider.MethodIdem && sess.Claim == nil {
		return Sent{}, s.discardSession(ctx, tenant, sess,
			fmt.Errorf("proofing: create session request %s: IPS offered no vcmrtd link", req.ID))
	}
	req.Method = method
	attached, err := s.requests.AttachSession(ctx, req, sess)
	if err != nil {
		return Sent{}, s.discardSession(ctx, tenant, sess, err)
	}
	if !attached {
		// A concurrent start won (or the request ended meanwhile): this
		// session, holding the reference photo, is erased again.
		return Sent{}, s.discardSession(ctx, tenant, sess, ErrLinkStarted)
	}
	if sess.FlowVersion != 0 {
		req.FlowVersion = sess.FlowVersion
	}
	req.session = &ipsSession{ID: sess.ID, Token: sess.Token, ExpiresAt: sess.ExpiresAt}
	out := Sent{Request: req}
	if sess.Claim != nil {
		out.DeepLink = sess.Claim.DeepLink
	}
	return out, nil
}

// hostedYiviSession is the running Yivi session of a hosted request.
func (s *Service) hostedYiviSession(ctx context.Context, token string) (Request, *ipsSession, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return Request{}, nil, err
	}
	return s.yiviSessionOf(req)
}

// HostedClaimLink is ClaimLink for a hosted request.
func (s *Service) HostedClaimLink(ctx context.Context, token string) (proofingprovider.Claim, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return proofingprovider.Claim{}, err
	}
	return s.claimLink(audit.ContextWithActor(ctx, hostedSubjectActor), req)
}

// HostedStartYivi is StartYivi for a hosted request.
func (s *Service) HostedStartYivi(ctx context.Context, token string) (YiviStart, error) {
	req, sess, err := s.hostedYiviSession(ctx, token)
	if err != nil {
		return YiviStart{}, err
	}
	return s.startYivi(ctx, req, sess)
}

// HostedYiviDisclosure is YiviDisclosure for a hosted request.
func (s *Service) HostedYiviDisclosure(ctx context.Context, token string) (proofingprovider.YiviDisclosure, error) {
	req, sess, err := s.hostedYiviSession(ctx, token)
	if err != nil {
		return proofingprovider.YiviDisclosure{}, err
	}
	return s.yiviDisclosure(ctx, req, sess)
}

// HostedFaceFrame is FaceFrame for a hosted request.
func (s *Service) HostedFaceFrame(ctx context.Context, token, image string) (proofingprovider.FaceVerdict, error) {
	req, sess, err := s.hostedYiviSession(ctx, token)
	if err != nil {
		return proofingprovider.FaceVerdict{}, err
	}
	return s.faceFrame(ctx, req, sess, image)
}

// HostedEmbedOrigins are the origins that may frame a hosted link's page: its
// customer's allowed redirect origins. It reads no the engine state.
func (s *Service) HostedEmbedOrigins(ctx context.Context, token string) ([]string, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return nil, err
	}
	if req.CustomerID == nil {
		return nil, ErrRequestNotFound
	}
	customer, err := s.customers.Get(ctx, req.OrganizationID, *req.CustomerID)
	if err != nil {
		return nil, err
	}
	return customer.RedirectOrigins, nil
}

// HostedLogo is the logo of a hosted request's customer, for its page.
func (s *Service) HostedLogo(ctx context.Context, token string) (CustomerLogo, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return CustomerLogo{}, err
	}
	if req.CustomerID == nil {
		return CustomerLogo{}, ErrRequestNotFound
	}
	return s.customers.Logo(ctx, req.OrganizationID, *req.CustomerID)
}

// hostedSubjectActor names who acts through a hosted link in the audit log:
// its subject, who has no account.
var hostedSubjectActor = audit.Actor{Label: "hosted_link"}

// DeclineHosted cancels a hosted request whose subject declined on its page,
// before starting, audited as identity_proofing.session_cancelled by the link.
// A started or settled link is ErrLinkStarted or ErrSessionOver.
func (s *Service) DeclineHosted(ctx context.Context, token string) (Request, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return Request{}, err
	}
	if req.CancelledAt != nil || req.PurgedAt != nil {
		return Request{}, ErrSessionOver
	}
	if !req.awaitingStart(s.now()) {
		if req.session != nil {
			return Request{}, ErrLinkStarted
		}
		return Request{}, ErrSessionOver
	}
	cancelled, err := s.requests.Cancel(audit.ContextWithActor(ctx, hostedSubjectActor), req)
	if errors.Is(err, errCancelSessionMoved) {
		return Request{}, ErrLinkStarted
	}
	if err != nil {
		return Request{}, err
	}
	if !cancelled {
		return Request{}, ErrSessionOver
	}
	return s.hostedRequest(ctx, token)
}

// maxRedirectOrigins and maxRedirectLength bound what a customer lists and
// what a hosted request redirects to.
const (
	maxRedirectOrigins = 10
	maxRedirectLength  = 2048
)

// loopbackHosts may use plain http in a redirect origin, for a customer
// developing against its own machine.
var loopbackHosts = []string{"localhost", "127.0.0.1", "[::1]"}

// parseRedirectOrigin reads raw as an origin: https (or http on a loopback
// host), a host, an optional port, and nothing else. It returns the origin
// lowercased, as a browser serialises it.
func parseRedirectOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("%w: %q is not an origin (scheme://host[:port])", ErrInvalidInput, raw)
	}
	origin := strings.ToLower(u.Scheme + "://" + u.Host)
	if u.Scheme != "https" && (u.Scheme != "http" || !slices.Contains(loopbackHosts, strings.ToLower(u.Hostname()))) {
		return "", fmt.Errorf("%w: %q must use https", ErrInvalidInput, raw)
	}
	return origin, nil
}

// SaveCustomerRedirectOrigins replaces the origins a customer's hosted pages
// may redirect to and be embedded on: each an origin, kept once, in the order
// given.
func (s *Service) SaveCustomerRedirectOrigins(ctx context.Context, orgID, id uuid.UUID, origins []string) (Customer, error) {
	if len(origins) > maxRedirectOrigins {
		return Customer{}, fmt.Errorf("%w: at most %d redirect origins", ErrInvalidInput, maxRedirectOrigins)
	}
	out := make([]string, 0, len(origins))
	for _, raw := range origins {
		origin, err := parseRedirectOrigin(raw)
		if err != nil {
			return Customer{}, err
		}
		if !slices.Contains(out, origin) {
			out = append(out, origin)
		}
	}
	return s.customers.SaveRedirectOrigins(ctx, orgID, id, out)
}

// checkRedirect accepts a hosted request's redirect: an absolute URL on one of
// the customer's origins.
func checkRedirect(raw string, customer Customer) error {
	if len(raw) > maxRedirectLength {
		return fmt.Errorf("%w: redirectUrl is too long", ErrInvalidInput)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return fmt.Errorf("%w: redirectUrl must be an absolute URL", ErrInvalidInput)
	}
	if !slices.Contains(customer.RedirectOrigins, strings.ToLower(u.Scheme+"://"+u.Host)) {
		return ErrRedirectNotAllowed
	}
	return nil
}
