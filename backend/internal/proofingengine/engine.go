// Package proofingengine runs identity proofing inside the wallet: the sessions
// the Idem app (vcmrtd) claims and walks through, chip verification, the result
// with its BSN and image redaction, assurance scoring, Regula face checks and
// the manual review.
//
// The org is the tenant: a session's and a flow's TenantID is the
// organization's id. internal/proofing drives the engine through Engine's
// methods (rp.go), never over HTTP; the Idem app talks to the routes Register
// mounts.
package proofingengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/i18n"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regulasweep"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/ratelimit"
)

// Config tunes the engine.
type Config struct {
	// MaxBodyBytes caps an app request body (base64 images and chip data).
	MaxBodyBytes int64

	SessionCreateTTL time.Duration
	SessionMinTTL    time.Duration
	SessionMaxTTL    time.Duration
	SessionOpenTTL   time.Duration
	// SessionMaxLifetime caps ExpiresAt at CreatedAt plus this, whatever
	// the requested TTL or an opening extends it to.
	SessionMaxLifetime time.Duration
	// HandoverTokenTTL is how long a handover grant stays claimable;
	// ClaimTokenTTL a fresh slot's claim (the mailed QR), never past the
	// session's own expiry.
	HandoverTokenTTL time.Duration
	ClaimTokenTTL    time.Duration
	// DeviceStaleAfter is how long a device may go silent before it counts
	// as away; DeviceDisconnectGrace how soon after dropping its long-poll.
	DeviceStaleAfter      time.Duration
	DeviceDisconnectGrace time.Duration
	// SessionEventsWait/-Poll shape the app's /events long-poll.
	SessionEventsWait time.Duration
	SessionEventsPoll time.Duration
	// SessionRetention is how long a finished session is kept when neither
	// the wallet nor its flow says otherwise; 0 keeps it until deleted.
	SessionRetention time.Duration

	// ClaimLimit bounds the claims of one session's grants (a scanned QR
	// retried, or abuse of a leaked link). Per session rather than per client
	// IP: behind the wallet's reverse proxy every phone has the proxy's address.
	ClaimLimit ratelimit.Limit

	// PublicBaseURL is the origin the Idem app reaches /api/v1/app at: the
	// api= of every vcmrtd deep link.
	PublicBaseURL string

	// Now is the clock a document's expiry date is judged against; nil is
	// time.Now. A test sets it to read a fixture document while it was valid.
	Now func() time.Time

	// Regula verifies a native face step (and the Yivi method's face check)
	// against the Regula Face API; nil leaves both unavailable.
	Regula RegulaClient
	// RegulaSweeps queues each session's Regula tag for deletion once it ends.
	RegulaSweeps regulasweep.Queue
	// RegulaFaceAPIPublicURL is the Face API the app runs liveness against;
	// RegulaFaceMatchThreshold the similarity a match must reach.
	RegulaFaceAPIPublicURL   string
	RegulaFaceMatchThreshold float64

	// BoundLoginTTL is a Yivi-method session's default lifetime;
	// BoundLoginStableFrames the consecutive matching frames that approve
	// it, BoundLoginMaxAttempts the usable frames before it is rejected.
	BoundLoginTTL          time.Duration
	BoundLoginStableFrames int
	BoundLoginMaxAttempts  int

	// DeviceTrail records which devices took part in a session in the org's
	// audit log (deviceTrailEvents); nil keeps the trail in the server log only.
	DeviceTrail DeviceTrail
}

// DeviceTrail is where the engine reports a session's device events: the
// wallet records each in the org's audit log against the request the session
// belongs to. Details carry device ids, roles and reasons, never personal data.
type DeviceTrail interface {
	RecordDeviceEvent(ctx context.Context, tenantID, sessionID string, event DeviceEvent, details map[string]any) error
}

// DeviceEvent names a device event in the audit log.
type DeviceEvent string

// The device events the org's audit log gets: which device claimed the
// session, handovers to another device, and requests refused for access.
const (
	DeviceClaimed        DeviceEvent = "device_claimed"
	DeviceHandedOver     DeviceEvent = "device_handed_over"
	DeviceHandoverIssued DeviceEvent = "handover_issued"
	DeviceHandoverFailed DeviceEvent = "handover_claim_failed"
	DeviceAccessDenied   DeviceEvent = "access_denied"
)

// deviceTrailEvents maps the engine's session events to the device events
// the audit log records. The others (a reconnect, a device's state) stay in
// the server log: they say how a device behaved, not which one took part.
var deviceTrailEvents = map[string]DeviceEvent{
	eventDeviceClaimed:       DeviceClaimed,
	eventSessionHandedOver:   DeviceHandedOver,
	eventHandoverIssued:      DeviceHandoverIssued,
	eventHandoverClaimFailed: DeviceHandoverFailed,
	eventAccessDenied:        DeviceAccessDenied,
}

// Event detail keys. deviceTrailDetails and auditProofing's log line pick
// details by these exact keys, so every call site uses the constant.
const (
	detailRole             = "role"
	detailVia              = "via"
	detailDeviceID         = "deviceId"
	detailPreviousDeviceID = "previousDeviceId"
	detailByRole           = "byRole"
	detailByDeviceID       = "byDeviceId"
	detailReason           = "reason"
	detailRoute            = "route"
	detailExpiresAt        = "expiresAt"
	detailStage            = "stage"
	detailErrorCode        = "errorCode"
)

// deviceTrailDetails are the detail keys a device event may carry into the
// audit log: none of them is personal data.
var deviceTrailDetails = []string{detailRole, detailVia, detailDeviceID, detailPreviousDeviceID, detailByRole, detailByDeviceID, detailReason, detailRoute, detailExpiresAt}

// deviceTrailTimeout bounds one device event's write to the audit log.
const deviceTrailTimeout = 5 * time.Second

// DefaultConfig is the engine's default timing and limits.
func DefaultConfig() Config {
	return Config{
		MaxBodyBytes:             12 << 20,
		SessionCreateTTL:         10 * time.Minute,
		SessionMinTTL:            time.Minute,
		SessionMaxTTL:            60 * time.Minute,
		SessionOpenTTL:           10 * time.Minute,
		SessionMaxLifetime:       15 * time.Minute,
		HandoverTokenTTL:         2 * time.Minute,
		ClaimTokenTTL:            10 * time.Minute,
		DeviceStaleAfter:         45 * time.Second,
		DeviceDisconnectGrace:    5 * time.Second,
		SessionEventsWait:        25 * time.Second,
		SessionEventsPoll:        500 * time.Millisecond,
		SessionRetention:         defaultSessionRetention,
		ClaimLimit:               ratelimit.Limit{Burst: 20, Per: time.Minute},
		RegulaFaceMatchThreshold: regula.DefaultMatchThreshold,
		BoundLoginTTL:            5 * time.Minute,
		BoundLoginStableFrames:   3,
		BoundLoginMaxAttempts:    40,
	}
}

// defaultSessionRetention: a finished session's evidence goes 90 days after it
// ended.
const defaultSessionRetention = 90 * 24 * time.Hour

// RegulaClient is the subset of *regula.Client the engine uses.
type RegulaClient interface {
	GetLiveness(ctx context.Context, transactionID string) (regula.LivenessTransaction, error)
	Match(ctx context.Context, referenceBase64, transactionID string) (regula.MatchResult, error)
	MatchImages(ctx context.Context, referenceBase64, liveBase64 string) (regula.ImageMatch, error)
	DeleteLiveness(ctx context.Context, transactionID string) error
}

// TenantLookup names a tenant (an organization) for the app's
// "who is asking" line.
type TenantLookup interface {
	DisplayName(ctx context.Context, tenantID string) (string, error)
}

// Notifier is told, off the request path, that a session changed in a way
// the wallet reconciles on: it opened, a step started, or it settled.
type Notifier func(ctx context.Context, sessionID string)

// Server is the engine: the app-facing handlers and the relying-party
// methods share it.
type Server struct {
	cfg      Config
	sessions session.Interface
	flows    flow.Store
	tenants  TenantLookup
	notify   Notifier

	claimLimit *ratelimit.Limiter
	waiters    eventWaiters

	// changed queues the sessions to tell notify about; pending holds the
	// ones queued, so a burst of events for one session is one notice.
	changed   chan string
	pendingMu sync.Mutex
	pending   map[string]bool
}

// Engine is the name internal/proofing and cmd/api know the engine by.
type Engine = Server

// New builds the engine on its stores. tenants names an org for the app; a
// nil notify tells nobody.
func New(cfg Config, sessions session.Interface, flows flow.Store, tenants TenantLookup, notify Notifier) *Server {
	if cfg.RegulaFaceMatchThreshold <= 0 {
		cfg.RegulaFaceMatchThreshold = regula.DefaultMatchThreshold
	}
	if cfg.BoundLoginStableFrames <= 0 {
		cfg.BoundLoginStableFrames = 1
	}
	if cfg.BoundLoginMaxAttempts < cfg.BoundLoginStableFrames {
		cfg.BoundLoginMaxAttempts = cfg.BoundLoginStableFrames
	}
	if cfg.BoundLoginTTL <= 0 {
		cfg.BoundLoginTTL = cfg.SessionCreateTTL
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	s := &Server{
		cfg: cfg, sessions: sessions, flows: flows, tenants: tenants, notify: notify,
		claimLimit: ratelimit.New(cfg.ClaimLimit),
		changed:    make(chan string, notifyQueue),
		pending:    map[string]bool{},
		waiters:    eventWaiters{bySession: map[string]int{}},
	}
	// A session expiring is noticed lazily, by whichever read finds its
	// deadline passed; this is how that reaches the wallet.
	sessions.SetOnExpire(func(sess session.Session) {
		s.auditProofing(sess, eventSessionExpired, nil)
	})
	return s
}

// Register mounts the Idem app's routes under the /api/v1 sub-mux. They
// carry no wallet login: the path token names the session and the
// X-Device-Token header authorizes the device holding it.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /app/{token}", s.handleAppSession)
	mux.HandleFunc("GET /app/{token}/events", s.handleAppSessionEvents)
	mux.HandleFunc("POST /app/{token}/result", s.handleAppSessionResult)
	mux.HandleFunc("POST /app/{token}/steps/selfie", s.handleSubmitSelfieStep)
	mux.HandleFunc("POST /app/{token}/steps/nfc", s.handleSubmitNFCStep)
	mux.HandleFunc("POST /app/{token}/steps/{step}/start", s.handleStartStep)
	mux.HandleFunc("POST /app/{token}/steps/document_capture", s.handleSubmitDocumentStep)
	mux.HandleFunc("POST /app/{token}/steps/document_photo", s.handleSubmitDocumentPhotoStep)
	mux.HandleFunc("GET /app/{token}/steps/{step}/result", s.handleStepResult)
	mux.HandleFunc("POST /app/{token}/submit", s.handleSubmitSession)
	mux.HandleFunc("POST /app/{token}/claim", s.handleSessionTokenClaim)
	mux.HandleFunc("POST /app/{token}/handover", s.handleMintHandover)
	mux.HandleFunc("POST /app/{token}/device/state", s.handleDeviceState)
	mux.HandleFunc("POST /app/handover/{handoverToken}/claim", s.handleClaimHandover)
}

// Purge expires every session past its deadline and removes finished ones
// past their retention (the session's own, else SessionRetention); it
// returns how many it removed.
func (s *Server) Purge(context.Context) (int64, error) {
	removed, err := s.sessions.Purge(s.cfg.SessionRetention, time.Now().UTC())
	for _, sess := range removed {
		s.auditProofing(sess, eventSessionPurged, map[string]any{"priorStatus": string(sess.Status), detailReason: "retention"})
	}
	if err != nil {
		return int64(len(removed)), fmt.Errorf("proofingengine: purge: %w", err)
	}
	return int64(len(removed)), nil
}

// SweepRegula deletes the Regula liveness transactions of ended sessions
// that are due, returning how many tags it swept.
func (s *Server) SweepRegula(ctx context.Context) (int64, error) {
	deleter, ok := s.cfg.Regula.(regulasweep.Deleter)
	if s.cfg.RegulaSweeps == nil || !ok {
		return 0, nil
	}
	n, err := regulasweep.Sweeper{Queue: s.cfg.RegulaSweeps, Deleter: deleter}.Sweep(ctx, time.Now().UTC())
	if err != nil {
		return int64(n), fmt.Errorf("proofingengine: regula sweep: %w", err)
	}
	return int64(n), nil
}

// notifyEventTypes are the events the wallet reconciles a request on: an
// outcome, and the session opening or a step starting (for its progress).
var notifyEventTypes = map[string]bool{
	eventResultVerified:    true,
	eventResultRejected:    true,
	eventResultNeedsReview: true,
	eventSessionCancelled:  true,
	eventSessionExpired:    true,
	eventSessionOpened:     true,
	eventSessionInProgress: true,
}

// notifyTimeout bounds one notification's reconcile; notifyQueue is how
// many sessions may wait to be told about.
const (
	notifyTimeout = 30 * time.Second
	notifyQueue   = 256
)

// Run tells the notifier about changed sessions, one at a time, until ctx
// ends. Start it once, before serving.
func (s *Server) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.changed:
			s.pendingMu.Lock()
			delete(s.pending, id)
			s.pendingMu.Unlock()
			if s.notify == nil {
				continue
			}
			nctx, cancel := context.WithTimeout(ctx, notifyTimeout)
			s.notify(nctx, id)
			cancel()
		}
	}
}

// queueChanged queues sessionID for Run, once while it waits. A full queue
// drops it: the wallet's deadline job reconciles the session anyway.
func (s *Server) queueChanged(sessionID string) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.pending[sessionID] {
		return
	}
	select {
	case s.changed <- sessionID:
		s.pending[sessionID] = true
	default:
		slog.Warn("identity proofing: change notice dropped, queue full", slog.String("session_id", sessionID))
	}
}

// auditProofing logs one session event (ids and status only: the wallet's
// audit log records what matters for the org, from its own reconcile) and
// tells the wallet when it is one it reconciles on. Details stay out of the
// log line: they can carry document fields.
func (s *Server) auditProofing(sess session.Session, eventType string, extra map[string]any) {
	if eventType == "" {
		return
	}
	attrs := []slog.Attr{
		slog.String("event", eventType), slog.String("session_id", sess.ID),
		slog.String("org_id", sess.TenantID), slog.String("status", string(sess.Status)),
	}
	for _, k := range []string{detailStage, detailRole, detailReason, detailErrorCode, detailVia} {
		if v, ok := extra[k].(string); ok && v != "" {
			attrs = append(attrs, slog.String(k, v))
		}
	}
	slog.LogAttrs(context.Background(), slog.LevelInfo, "identity proofing: session event", attrs...)
	if notifyEventTypes[eventType] {
		s.queueChanged(sess.ID)
	}
	s.recordDeviceEvent(sess, eventType, extra)
}

// recordDeviceEvent passes a device event on to the org's audit log. A failed
// write is logged: the device's request has been answered either way.
func (s *Server) recordDeviceEvent(sess session.Session, eventType string, extra map[string]any) {
	event, ok := deviceTrailEvents[eventType]
	if !ok || s.cfg.DeviceTrail == nil {
		return
	}
	details := map[string]any{}
	for _, k := range deviceTrailDetails {
		if v, ok := extra[k]; ok && v != "" {
			details[k] = v
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), deviceTrailTimeout)
	defer cancel()
	if err := s.cfg.DeviceTrail.RecordDeviceEvent(ctx, sess.TenantID, sess.ID, event, details); err != nil {
		slog.Warn("identity proofing: record device event", slog.String("session_id", sess.ID),
			slog.String("event", string(event)), slog.Any("error", err))
	}
}

// tenantDisplayName is who the app tells the subject is asking: the org's
// name, else its id.
func (s *Server) tenantDisplayName(ctx context.Context, tenantID string) string {
	if s.tenants == nil {
		return tenantID
	}
	name, err := s.tenants.DisplayName(ctx, tenantID)
	if err != nil || name == "" {
		return tenantID
	}
	return name
}

// apiBaseURL is the origin a deep link's api= names.
func (s *Server) apiBaseURL() string {
	return strings.TrimRight(s.cfg.PublicBaseURL, "/")
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		slog.Warn("identity proofing: write response", slog.Any("error", err))
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// requestLanguage is the language the app's flow is in: the session's when
// shipped, else the device's Accept-Language, else English.
func requestLanguage(r *http.Request, sessionLanguage string) string {
	return i18n.Resolve(sessionLanguage, r.Header.Get(headerAcceptLang))
}

func round3(v float64) float64 { return float64(int(v*1000+0.5)) / 1000 }

// SetNotifier sets who is told about session changes; call before serving.
func (s *Server) SetNotifier(n Notifier) { s.notify = n }
