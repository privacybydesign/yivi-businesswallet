package proofingprovider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const (
	// stubSessionTTL and stubClaimTTL mirror IPS's own defaults: a claim lasts
	// as long as a default session, so a mailed QR works for the whole session.
	stubSessionTTL = 10 * time.Minute
	stubClaimTTL   = 10 * time.Minute

	stubAssuranceLevel = "substantial"
	// stubProofedName stands in for the name an approved session read off the
	// subject's document.
	stubProofedName = "Anna Jansen"
	stubIDBytes     = 8
	// stubAPIBaseURL stands in for IPS's PUBLIC_BASE_URL in a stub deep link.
	// .invalid never resolves (RFC 2606).
	stubAPIBaseURL = "http://ips.stub.invalid"
)

// Stub is an in-process IPS for dev/CI and tests. Flows and their versions live
// in memory per API key (a restart empties them). No phone can reach a stub
// session, so it stays created (pending) until it expires, unless a test sets
// Outcome to stand in for the subject finishing the vcmrtd flow.
type Stub struct {
	Outcome Status

	mu       sync.Mutex
	flows    map[string][]Flow
	sessions map[string]stubSession
}

type stubSession struct {
	apiKey      string
	token       string
	flowID      string
	flowVersion int
	expiresAt   time.Time
}

// NewStub builds an empty Stub whose sessions never decide.
func NewStub() *Stub {
	return &Stub{flows: map[string][]Flow{}, sessions: map[string]stubSession{}}
}

func (*Stub) Ping(context.Context) error { return nil }

func (*Stub) CreateTenant(context.Context, string) (Tenant, error) {
	id, err := stubID("tenant")
	if err != nil {
		return Tenant{}, err
	}
	secret, err := stubID("whsec")
	if err != nil {
		return Tenant{}, err
	}
	return Tenant{ID: id, WebhookSecret: secret}, nil
}

func (*Stub) CreateAPIKey(_ context.Context, _ string, _ []string) (string, error) {
	return stubID("sk_live")
}

func (s *Stub) ListFlows(_ context.Context, apiKey string) ([]Flow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Flow{}
	for _, f := range s.flows[apiKey] {
		if f.Active {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *Stub) CreateFlow(_ context.Context, apiKey string, in FlowSpec) (Flow, error) {
	if err := stubValidate(in); err != nil {
		return Flow{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := map[string]bool{}
	for _, f := range s.flows[apiKey] {
		ids[f.ID] = true
	}
	f := Flow{FlowSpec: in, ID: "flow_" + strconv.Itoa(len(ids)+1), Version: 1, Active: true, CreatedAt: time.Now().UTC()}
	s.flows[apiKey] = append(s.flows[apiKey], f)
	return f, nil
}

func (s *Stub) CreateFlowVersion(_ context.Context, apiKey, id string, in FlowSpec) (Flow, error) {
	if err := stubValidate(in); err != nil {
		return Flow{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	latest := 0
	for i, f := range s.flows[apiKey] {
		if f.ID == id {
			latest = max(latest, f.Version)
			s.flows[apiKey][i].Active = false
		}
	}
	if latest == 0 {
		return Flow{}, ErrNotFound
	}
	f := Flow{FlowSpec: in, ID: id, Version: latest + 1, Active: true, CreatedAt: time.Now().UTC()}
	s.flows[apiKey] = append(s.flows[apiKey], f)
	return f, nil
}

func (s *Stub) ListFlowVersions(_ context.Context, apiKey, id string) ([]Flow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Flow
	for _, f := range s.flows[apiKey] {
		if f.ID == id {
			out = append(out, f)
		}
	}
	if out == nil {
		return nil, ErrNotFound
	}
	return out, nil
}

func (s *Stub) ActivateFlowVersion(_ context.Context, apiKey, id string, version int) (Flow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := -1
	for i, f := range s.flows[apiKey] {
		if f.ID == id && f.Version == version {
			target = i
		}
	}
	if target < 0 {
		return Flow{}, ErrNotFound
	}
	for i, f := range s.flows[apiKey] {
		if f.ID == id {
			s.flows[apiKey][i].Active = i == target
		}
	}
	return s.flows[apiKey][target], nil
}

// stubValidate is the one IPS rule the stub enforces; the full rule set lives at IPS.
func stubValidate(in FlowSpec) error {
	if in.Name == "" || len(in.Steps) == 0 {
		return &RejectedError{Status: http.StatusBadRequest, Message: "a flow needs a name and at least one step"}
	}
	return nil
}

func (s *Stub) CreateSession(_ context.Context, apiKey string, in SessionInput) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	version := 0
	for _, f := range s.flows[apiKey] {
		if f.ID == in.FlowID && f.Active {
			version = f.Version
		}
	}
	if version == 0 {
		return Session{}, &RejectedError{Status: http.StatusBadRequest, Message: "unknown flow"}
	}
	ttl := stubSessionTTL
	if in.TTL > 0 {
		ttl = min(in.TTL, stubSessionTTL)
	}
	return s.createLocked(apiKey, in.FlowID, version, ttl)
}

func (s *Stub) createLocked(apiKey, flowID string, version int, ttl time.Duration) (Session, error) {
	id, err := stubID("ses")
	if err != nil {
		return Session{}, err
	}
	token, err := stubID("tok")
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	s.sessions[id] = stubSession{apiKey: apiKey, token: token, flowID: flowID, flowVersion: version, expiresAt: now.Add(ttl)}
	return Session{ID: id, Token: token, ExpiresAt: now.Add(ttl), FlowVersion: version, Claim: stubClaim(id, now)}, nil
}

func (s *Stub) SessionResult(_ context.Context, apiKey, sessionID, sessionToken string) (Result, error) {
	sess, err := s.session(apiKey, sessionID, sessionToken)
	if err != nil {
		return Result{}, err
	}
	if time.Now().After(sess.expiresAt) {
		return Result{Status: StatusExpired}, nil
	}
	if s.Outcome == "" {
		return Result{Status: StatusCreated}, nil
	}
	now := time.Now().UTC()
	res := Result{Status: s.Outcome, CompletedAt: &now}
	if s.Outcome == StatusApproved {
		res.AssuranceLevel, res.EIDASLevel = stubAssuranceLevel, stubAssuranceLevel
		res.Name = stubProofedName
	}
	return res, nil
}

func (s *Stub) session(apiKey, sessionID, sessionToken string) (stubSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok || sess.apiKey != apiKey || sess.token != sessionToken {
		return stubSession{}, ErrNotFound
	}
	return sess, nil
}

// stubClaim has IPS's deep link shape (device_access.go grantResponse), the
// one the vcmrtd app accepts: a handover token and the IPS base URL as api.
// The stub serves no API, so its api is a host that never resolves.
func stubClaim(sessionID string, now time.Time) *Claim {
	link := "vcmrtd://verify?handover=" + url.QueryEscape("stub-"+sessionID) + "&api=" + url.QueryEscape(stubAPIBaseURL)
	return &Claim{DeepLink: link, ExpiresAt: now.Add(stubClaimTTL)}
}

func stubID(prefix string) (string, error) {
	b := make([]byte, stubIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("proofingprovider: stub id: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(b), nil
}
