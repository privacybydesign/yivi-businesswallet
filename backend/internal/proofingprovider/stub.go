package proofingprovider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	// stubStableFrames and stubMaxAttempts mirror IPS's bound-login defaults.
	stubStableFrames = 3
	stubMaxAttempts  = 40
)

// Stub is an in-process IPS for dev/CI and tests. Flows and their versions live
// in memory per API key (a restart empties them). No phone can reach a stub
// session, so it stays created (pending) until it expires, unless a test sets
// Outcome to stand in for the subject finishing the vcmrtd flow, or the face
// check after a Yivi disclosure (which stays pending while Outcome is empty).
type Stub struct {
	Outcome Status

	// onChange stands in for IPS's push: told a session decided (Outcome).
	onChange func(sessionID string)

	mu       sync.Mutex
	tenants  map[string]bool
	keys     map[string]KeyEnvironment
	flows    map[string][]Flow
	sessions map[string]stubSession
}

type stubSession struct {
	apiKey      string
	token       string
	flowID      string
	flowVersion int
	method      Method
	expiresAt   time.Time
	// scripted is the outcome a ScriptedOutcome session resolved to at once.
	scripted *Result
}

// stubDecisionDelay is how long the stub's subject takes to finish, so the
// session is attached before the change is pushed.
const stubDecisionDelay = 2 * time.Second

// OnSessionChange registers the listener told when a stub session decides, as
// IPS would push it. Call before serving.
func (s *Stub) OnSessionChange(fn func(sessionID string)) { s.onChange = fn }

// NewStub builds an empty Stub whose sessions never decide.
func NewStub() *Stub {
	return &Stub{tenants: map[string]bool{}, keys: map[string]KeyEnvironment{}, flows: map[string][]Flow{}, sessions: map[string]stubSession{}}
}

func (*Stub) Ping(context.Context) error { return nil }

func (s *Stub) CreateTenant(_ context.Context, id, _ string) (Tenant, error) {
	secret, err := stubID("whsec")
	if err != nil {
		return Tenant{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tenants[id] {
		return Tenant{}, fmt.Errorf("proofingprovider: create tenant: %w", ErrTenantExists)
	}
	s.tenants[id] = true
	return Tenant{ID: id, WebhookSecret: secret}, nil
}

func (s *Stub) RotateWebhookSecret(_ context.Context, id string) (string, error) {
	s.mu.Lock()
	known := s.tenants[id]
	s.mu.Unlock()
	if !known {
		return "", ErrNotFound
	}
	return stubID("whsec")
}

func (s *Stub) CreateAPIKey(_ context.Context, _ string, env KeyEnvironment, _ []string) (string, error) {
	key, err := stubID("sk_" + string(env))
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[key] = env
	return key, nil
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
	// As at IPS: a test key only runs scripted outcomes, a live key never.
	switch env, known := s.keys[apiKey]; {
	case known && env == KeyTest && in.ScriptedOutcome == "":
		return Session{}, &RejectedError{Status: http.StatusBadRequest, Message: "a test API key only creates sandbox sessions: set scriptedOutcome"}
	case known && env == KeyLive && in.ScriptedOutcome != "":
		return Session{}, &RejectedError{Status: http.StatusBadRequest, Message: "scriptedOutcome is only available for sandbox tenants"}
	}
	if in.ScriptedOutcome != "" {
		return s.createScriptedLocked(apiKey, in)
	}
	version := 0
	for _, f := range s.flows[apiKey] {
		if f.ID == in.FlowID && f.Active {
			version = f.Version
		}
	}
	if version == 0 {
		return Session{}, &RejectedError{Status: http.StatusBadRequest, Message: "unknown flow"}
	}
	if _, err := ipsMethod(in.Method); err != nil {
		return Session{}, err
	}
	ttl := stubSessionTTL
	if in.TTL > 0 {
		ttl = min(in.TTL, stubSessionTTL)
	}
	return s.createLocked(apiKey, in.FlowID, version, ttl, in.Method)
}

func (s *Stub) createLocked(apiKey, flowID string, version int, ttl time.Duration, method Method) (Session, error) {
	id, err := stubID("ses")
	if err != nil {
		return Session{}, err
	}
	token, err := stubID("tok")
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	s.sessions[id] = stubSession{
		apiKey: apiKey, token: token, flowID: flowID, flowVersion: version, method: method, expiresAt: now.Add(ttl),
	}
	sess := Session{ID: id, Token: token, ExpiresAt: now.Add(ttl), FlowVersion: version}
	if s.Outcome != "" && s.onChange != nil {
		notify := s.onChange
		time.AfterFunc(stubDecisionDelay, func() { notify(id) })
	}
	if method != MethodYivi {
		sess.Claim = stubClaim(id, now)
	}
	return sess, nil
}

// createScriptedLocked resolves a ScriptedOutcome session at once, as an IPS
// sandbox tenant does: no flow, no subject, no claim.
func (s *Stub) createScriptedLocked(apiKey string, in SessionInput) (Session, error) {
	res, err := scriptedResult(in.ScriptedOutcome)
	if err != nil {
		return Session{}, err
	}
	id, err := stubID("ses")
	if err != nil {
		return Session{}, err
	}
	token, err := stubID("tok")
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	s.sessions[id] = stubSession{apiKey: apiKey, token: token, method: in.Method, expiresAt: now.Add(stubSessionTTL), scripted: &res}
	return Session{ID: id, Token: token, ExpiresAt: now.Add(stubSessionTTL)}, nil
}

// scriptedResult is what an IPS sandbox resolves a scripted outcome to.
func scriptedResult(outcome string) (Result, error) {
	now := time.Now().UTC()
	switch {
	case outcome == "approve":
		return Result{
			Status: StatusApproved, CompletedAt: &now, AssuranceLevel: stubAssuranceLevel,
			EIDASLevel: stubAssuranceLevel, Name: stubProofedName,
		}, nil
	case outcome == "needs_review":
		return Result{Status: StatusNeedsReview, CompletedAt: &now}, nil
	case outcome == "expire":
		return Result{Status: StatusExpired}, nil
	case strings.HasPrefix(outcome, "reject:") && len(outcome) > len("reject:"):
		return Result{Status: StatusRejected, ErrorCode: strings.TrimPrefix(outcome, "reject:"), CompletedAt: &now}, nil
	}
	return Result{}, &RejectedError{Status: http.StatusBadRequest, Message: "invalid scriptedOutcome"}
}

func (s *Stub) SessionResult(_ context.Context, apiKey, sessionID, sessionToken string) (Result, error) {
	sess, err := s.session(apiKey, sessionID, sessionToken)
	if err != nil {
		return Result{}, err
	}
	if sess.scripted != nil {
		return *sess.scripted, nil
	}
	if time.Now().After(sess.expiresAt) {
		return Result{Status: StatusExpired}, nil
	}
	if s.Outcome == "" {
		return Result{Status: StatusCreated}, nil
	}
	now := time.Now().UTC()
	// The stub's subject finished in the app the session was created for.
	method := MethodIdem
	if sess.method == MethodYivi {
		method = MethodYivi
	}
	res := Result{Status: s.Outcome, CompletedAt: &now, Method: method}
	if s.Outcome == StatusApproved {
		res.AssuranceLevel, res.EIDASLevel = stubAssuranceLevel, stubAssuranceLevel
		res.Name = stubProofedName
	}
	return res, nil
}

// SessionIdentity is SessionResult with the stub subject's identity and a
// valid chip read, for an approved session.
func (s *Stub) SessionIdentity(ctx context.Context, apiKey, sessionID, sessionToken string) (Identity, error) {
	res, err := s.SessionResult(ctx, apiKey, sessionID, sessionToken)
	if err != nil {
		return Identity{}, err
	}
	id := Identity{Result: res}
	if res.Status == StatusApproved {
		id.GivenName, id.FamilyName, id.BirthDate, id.Nationality = "Anna", "Jansen", "1990-04-12", "NLD"
		id.Evidence = &Evidence{
			Type: EvidenceEMRTD, DocumentType: "P", IssuingState: "NLD", ExpiryDate: "2031-02-01",
			PassiveAuth: CheckValid, ActiveAuth: CheckValid, Liveness: "passed",
		}
		if res.Method == MethodYivi {
			id.Evidence.Type = EvidenceYivi
		}
	}
	return id, nil
}

// DecideReview settles a session in needs_review, as IPS's decision route does.
func (s *Stub) DecideReview(ctx context.Context, apiKey, sessionID, sessionToken string, d ReviewDecision) error {
	current, err := s.SessionResult(ctx, apiKey, sessionID, sessionToken)
	if err != nil {
		return err
	}
	if current.Status != StatusNeedsReview {
		return &RejectedError{Status: http.StatusConflict, Message: "the session is not under review"}
	}
	now := time.Now().UTC()
	res := Result{Status: StatusRejected, ErrorCode: d.ErrorCode, CompletedAt: &now, Method: current.Method}
	if d.Approve {
		res = Result{
			Status: StatusApproved, CompletedAt: &now, Method: current.Method,
			AssuranceLevel: stubAssuranceLevel, EIDASLevel: stubAssuranceLevel, Name: stubProofedName,
		}
	} else if res.ErrorCode == "" {
		res.ErrorCode = "MANUAL_REVIEW_REJECTED"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[sessionID]
	sess.scripted = &res
	s.sessions[sessionID] = sess
	return nil
}

// SessionStatus is SessionResult without the name, like IPS's status route.
func (s *Stub) SessionStatus(ctx context.Context, apiKey, sessionID, sessionToken string) (Result, error) {
	res, err := s.SessionResult(ctx, apiKey, sessionID, sessionToken)
	res.Name = ""
	return res, err
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

// yiviSession finds a live MethodYivi session by its token, the way IPS's
// subject-facing routes do.
func (s *Stub) yiviSession(sessionToken string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		if sess.token != sessionToken {
			continue
		}
		if sess.method != MethodYivi || time.Now().After(sess.expiresAt) {
			return "", &RejectedError{Status: http.StatusConflict, Message: "not a running Yivi session"}
		}
		return id, nil
	}
	return "", ErrNotFound
}

// SubmitReference takes any photo: the stub has no face to find in it.
func (s *Stub) SubmitReference(_ context.Context, apiKey, sessionID, sessionToken string, _ Reference) (YiviDisclosure, error) {
	if _, err := s.session(apiKey, sessionID, sessionToken); err != nil {
		return YiviDisclosure{}, err
	}
	if _, err := s.yiviSession(sessionToken); err != nil {
		return YiviDisclosure{}, err
	}
	return YiviDisclosure{OK: true, StableFrames: stubStableFrames, MaxAttempts: stubMaxAttempts}, nil
}

// SubmitFaceFrame decides as Outcome says: approved or rejected at once,
// pending otherwise.
func (s *Stub) SubmitFaceFrame(_ context.Context, sessionToken, _ string) (FaceVerdict, error) {
	if _, err := s.yiviSession(sessionToken); err != nil {
		return FaceVerdict{}, err
	}
	verdict := FaceVerdict{FaceDetected: true, StableFrames: stubStableFrames, MaxAttempts: stubMaxAttempts, Attempts: 1}
	switch s.Outcome {
	case StatusApproved:
		verdict.Matched, verdict.Consecutive, verdict.Decision = true, stubStableFrames, FaceDecisionApproved
	case StatusRejected:
		verdict.Decision = FaceDecisionRejected
	default:
		verdict.Decision = FaceDecisionPending
	}
	return verdict, nil
}

// CancelSession ends a stub session that has not decided, as IPS does.
func (s *Stub) CancelSession(ctx context.Context, apiKey, sessionID, sessionToken string) error {
	current, err := s.SessionResult(ctx, apiKey, sessionID, sessionToken)
	if err != nil {
		return err
	}
	if current.Status == StatusApproved || current.Status == StatusRejected {
		return &RejectedError{Status: http.StatusConflict, Message: "session already finished"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[sessionID]
	sess.expiresAt = time.Now()
	s.sessions[sessionID] = sess
	return nil
}

// DeleteSession forgets a stub session.
func (s *Stub) DeleteSession(_ context.Context, apiKey, sessionID, sessionToken string) error {
	if _, err := s.session(apiKey, sessionID, sessionToken); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
	return nil
}

// SessionHandover hands out a fresh claim link while an Idem session runs; the
// stub has no devices, so its app is never still active.
func (s *Stub) SessionHandover(_ context.Context, apiKey, sessionID, sessionToken string) (Claim, error) {
	sess, err := s.session(apiKey, sessionID, sessionToken)
	if err != nil {
		return Claim{}, err
	}
	now := time.Now()
	if sess.method != MethodIdem {
		return Claim{}, &RejectedError{Status: http.StatusConflict, Message: "only nfc_passport sessions have device slots"}
	}
	if !now.Before(sess.expiresAt) {
		return Claim{}, &RejectedError{Status: http.StatusGone, Message: "session expired", Code: "session_expired"}
	}
	return *stubClaim(sessionID, now), nil
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
