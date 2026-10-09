package proofingengine

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// appSessionView is what the app-facing GET returns: what the app needs to run
// the flow, and the org by display name only.
type appSessionView struct {
	ID           string         `json:"id"`
	Method       session.Method `json:"method"`
	RelyingParty string         `json:"relyingParty"`
	// Status lets the browser tell a finished session from a running one.
	Status session.Status `json:"status"`
	// Language is "en" or "nl": the session's language when shipped, else the
	// caller's Accept-Language, else "en".
	Language            string   `json:"language,omitempty"`
	RequestedAttributes []string `json:"requestedAttributes,omitempty"`
	// Steps is the flow's step order, which tells the app what to do; empty without
	// a flow.
	Steps []flow.Step `json:"steps,omitempty"`
	// SelfieLocation says who runs the face step, the browser or the Idem app
	// ("browser" without a flow). document_capture and nfc_read are always the
	// app's.
	SelfieLocation flow.StepLocation `json:"selfieLocation"`
	ExpiresAt      time.Time         `json:"expiresAt"`
	// AAChallenge is the RND.IFD the app sends to the chip's INTERNAL AUTHENTICATE.
	// Evidence with another nonce is never proof of possession.
	AAChallenge string `json:"aaChallenge,omitempty"`
	// RequiredChecks are the checks the session is scored on; the app runs the ones
	// it performs (Active/Chip Authentication for nfc.chip_auth) because the flow
	// asks for them.
	RequiredChecks []flow.Check `json:"requiredChecks"`
	// CompletedSteps are the steps with evidence, so a reloaded or handed-over
	// client resumes at the right screen.
	CompletedSteps []string `json:"completedSteps,omitempty"`
	// NativeHandoff says the Idem app still has work: the chip read, and the face
	// step when it runs native. The browser mints the app's QR itself (POST
	// .../handover {role: "native"}).
	NativeHandoff *nativeHandoffInfo `json:"nativeHandoff,omitempty"`
	// ResetCount goes up with every reset (POST .../reset): a client that sees it
	// change drops what it collected and starts over.
	ResetCount int `json:"resetCount"`
	// ChangeKey is the ?since= for GET .../events; clients echo it back, so its
	// format may change.
	ChangeKey string `json:"changeKey"`

	// FlowID/FlowVersion name the flow definition governing the session.
	FlowID      string `json:"flowId,omitempty"`
	FlowVersion int    `json:"flowVersion,omitempty"`
	// CurrentStep is the step to continue with, "" once complete; clients resume
	// from it. Absent without a flow, where "" would wrongly read as complete.
	CurrentStep *string `json:"currentStep,omitempty"`
	// ChipAccess is the MRZ-derived chip access key, present only while the chip
	// read is the current step (chipAccessFor).
	ChipAccess *session.ChipAccessKey `json:"chipAccess,omitempty"`
	// FaceReference is the photo the face step is matched against (the chip
	// portrait, or the customer's photo on a flow without nfc_read), present while
	// the face step is current and runs in the Idem app.
	FaceReference *photoInfo `json:"faceReference,omitempty"`
	// FaceVerification tells the Idem app to run the face step as a Regula
	// liveness session, when the flow uses Regula and it is configured.
	FaceVerification *faceVerificationInfo `json:"faceVerification,omitempty"`
	// FaceProvider is the engine the Idem app runs the face step with.
	FaceProvider flow.FaceProvider         `json:"faceProvider,omitempty"`
	StepResults  map[string]stepResultView `json:"stepResults"`
	// Lifecycle is ACTIVE, COMPLETE (every step has a result and Status is
	// decided), EXPIRED or CANCELLED.
	Lifecycle string `json:"lifecycle"`
	// ReadyToSubmit: every step has a result and the session waits for POST
	// .../submit.
	ReadyToSubmit bool `json:"readyToSubmit,omitempty"`
	// Device is the caller's own authorization; Devices every slot's state, so one
	// client sees the other go inactive.
	Device  callerDeviceView      `json:"device"`
	Devices map[string]deviceView `json:"devices"`
}

// faceVerificationInfo is appSessionView.FaceVerification's shape: the
// Face API to run liveness against and the tag to set on the session.
type faceVerificationInfo struct {
	Provider   string `json:"provider"`
	FaceAPIURL string `json:"faceApiUrl"`
	Tag        string `json:"tag"`
}

// nativeHandoffInfo is appSessionView.NativeHandoff's shape: the slot to
// mint a grant for and whether a device already holds it.
type nativeHandoffInfo struct {
	Role    session.DeviceRole `json:"role"`
	Claimed bool               `json:"claimed"`
}

// sessionResultView is the relying party's view of a session's result, already
// minimised by buildResult.
type sessionResultView struct {
	ID          string                    `json:"id"`
	Status      session.Status            `json:"status"`
	ErrorCode   string                    `json:"errorCode,omitempty"`
	Result      map[string]any            `json:"result,omitempty"`
	CompletedAt *time.Time                `json:"completedAt,omitempty"`
	Devices     []deviceParticipationView `json:"devices,omitempty"`
}

// sessionStatusView is a session's outcome without personal data: status,
// the assurance summary and which devices took part. It is what a relying
// party reconciles on, so reading it is not audited as a personal-data read.
type sessionStatusView struct {
	ID          string         `json:"id"`
	Status      session.Status `json:"status"`
	ErrorCode   string         `json:"errorCode,omitempty"`
	CompletedAt *time.Time     `json:"completedAt,omitempty"`
	FlowVersion int            `json:"flowVersion,omitempty"`
	Assurance   any            `json:"assurance,omitempty"`
	// Disclosure is whether a Yivi disclosure is part of the result.
	Disclosure bool                      `json:"disclosure,omitempty"`
	Devices    []deviceParticipationView `json:"devices,omitempty"`
}

// handleAppSession is the app's first call after opening the link: what to
// collect, who asks, how long it has. Opening it marks a created session
// opened; it is idempotent.
func (s *Server) handleAppSession(w http.ResponseWriter, r *http.Request) {
	sess, caller, ok := s.appSessionByPathToken(w, r)
	if !ok {
		return
	}
	if sess.Status == session.StatusCreated {
		opened := false
		updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
			var err error
			opened, err = s.openLocked(sess, time.Now().UTC())
			return err
		})
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		sess = updated
		if opened {
			s.auditProofing(sess, eventSessionOpened, nil)
		}
	}
	view, err := s.buildAppSessionView(r, sess, caller.role)
	if err != nil {
		writeInternalError(w, r, "could not resolve flow", err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// chipAccessFor is the chip access key a device is handed: only while the
// chip read is the current step, so a device taking the session over later
// never gets the key to the document.
func chipAccessFor(sess session.Session, current *string) *session.ChipAccessKey {
	if current == nil || *current != string(flow.StepNFCRead) || sess.Steps.Document == nil {
		return nil
	}
	return sess.Steps.Document.ChipAccess
}

// buildAppSessionView is the app-facing view of sess for the device holding
// role's slot ("" before anyone claimed the session).
func (s *Server) buildAppSessionView(r *http.Request, sess session.Session, role session.DeviceRole) (appSessionView, error) {
	resolvedFlow, err := s.resolveSessionFlow(r.Context(), sess)
	if err != nil {
		return appSessionView{}, err
	}
	var steps []flow.Step
	if resolvedFlow != nil {
		steps = resolvedFlow.Steps
	}
	selfieLocation := flow.LocationBrowser
	if resolvedFlow != nil {
		selfieLocation = resolvedFlow.EffectiveSelfieLocation()
	}
	var nativeHandoff *nativeHandoffInfo
	if nativeHandoffPending(sess, resolvedFlow) {
		nativeHandoff = &nativeHandoffInfo{Role: session.DeviceRoleNative, Claimed: sess.Access.Native != nil}
	}
	current := currentStep(sess, resolvedFlow)
	chipAccess := chipAccessFor(sess, current)
	var faceVerification *faceVerificationInfo
	var faceProvider flow.FaceProvider
	if resolvedFlow != nil && selfieLocation == flow.LocationNative && slices.ContainsFunc(steps, isFaceStep) {
		faceProvider = s.faceProviderFor(resolvedFlow)
	}
	if s.cfg.Regula != nil && faceProvider == flow.FaceProviderRegula {
		faceVerification = &faceVerificationInfo{Provider: faceProviderRegula, FaceAPIURL: s.cfg.RegulaFaceAPIPublicURL, Tag: regulaTag(sess)}
		s.queueRegulaSweep(r.Context(), sess)
	}
	var faceReference *photoInfo
	if current != nil && isFaceStep(flow.Step(*current)) && selfieLocation == flow.LocationNative {
		if image, mime, ok := s.faceMatchReference(sess); ok {
			faceReference = &photoInfo{ImageBase64: image, MimeType: mime}
		}
	}
	device := callerDeviceView{Authorized: role != "", Role: role}
	if role != "" {
		device.deviceView = s.toDeviceView(sess.Access.Slot(role))
	}
	return appSessionView{
		ID: sess.ID, Method: sess.Method, RelyingParty: s.tenantDisplayName(r.Context(), sess.TenantID), Status: sess.Status, Language: requestLanguage(r, sess.Language),
		RequestedAttributes: sess.RequestedAttributes, Steps: steps, ExpiresAt: sess.ExpiresAt,
		SelfieLocation: selfieLocation,
		AAChallenge:    sess.AAChallenge, RequiredChecks: assuranceChecksFor(resolvedFlow),
		CompletedSteps: sess.CompletedSteps(), NativeHandoff: nativeHandoff,
		ResetCount: sess.ResetCount, ChangeKey: s.appSessionChangeKey(sess, time.Now()),
		FlowID: sess.Flow, FlowVersion: sess.FlowVersion,
		CurrentStep: current, ChipAccess: chipAccess, FaceReference: faceReference, FaceVerification: faceVerification, FaceProvider: faceProvider,
		StepResults: stepResults(sess, resolvedFlow), Lifecycle: sessionLifecycle(sess), ReadyToSubmit: readyToSubmit(sess, resolvedFlow), Device: device,
		Devices: map[string]deviceView{
			string(session.DeviceRoleWeb):    s.toDeviceView(sess.Access.Web),
			string(session.DeviceRoleNative): s.toDeviceView(sess.Access.Native),
		},
	}, nil
}

// handleAppSessionEvents long-polls until the app-facing session view changes
// in status or completed steps. This lets browser clients notice an NFC step
// without repeatedly polling from JavaScript.
func (s *Server) handleAppSessionEvents(w http.ResponseWriter, r *http.Request) {
	sess, caller, ok := s.appSessionByPathToken(w, r)
	if !ok {
		return
	}
	if !s.waiters.acquire(sess.ID) {
		w.Header().Set(headerRetryAfter, strconv.Itoa(int(s.cfg.SessionEventsPoll.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "too many clients waiting on this session")
		return
	}
	defer s.waiters.release(sess.ID)
	baseline := r.URL.Query().Get("since")
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.SessionEventsWait)
	defer cancel()
	ticker := time.NewTicker(s.cfg.SessionEventsPoll)
	defer ticker.Stop()

	for s.appSessionChangeKey(sess, time.Now()) == baseline {
		select {
		case <-ctx.Done():
			if r.Context().Err() != nil {
				// The device hung up mid-wait - a killed app does exactly that.
				if d := sess.Access.Slot(caller.role); d != nil {
					s.markDisconnected(sess, caller.role, d.ID)
				}
				return
			}
			s.handleAppSession(w, r)
			return
		case <-ticker.C:
			updated, err := s.sessions.Get(sess.TenantID, sess.ID)
			if err != nil {
				writeError(w, http.StatusNotFound, "session not found")
				return
			}
			sess = updated
		}
	}
	s.handleAppSession(w, r)
}

// Bounds on the /events long polls: each waiter re-reads (and unseals) its
// session every SessionEventsPoll, so they are capped per session (a web and a
// native device, with room for a reconnect overlapping its old poll) and in
// the process.
const (
	maxEventWaitersPerSession = 4
	maxEventWaiters           = 512
)

// eventWaiters counts the /events long polls in flight.
type eventWaiters struct {
	mu        sync.Mutex
	total     int
	bySession map[string]int
}

// acquire takes a waiter slot for sessionID; false when either cap is full.
func (e *eventWaiters) acquire(sessionID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.total >= maxEventWaiters || e.bySession[sessionID] >= maxEventWaitersPerSession {
		return false
	}
	e.total++
	e.bySession[sessionID]++
	return true
}

func (e *eventWaiters) release(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.total--
	if e.bySession[sessionID]--; e.bySession[sessionID] <= 0 {
		delete(e.bySession, sessionID)
	}
}

// appSessionChangeKey is what handleAppSessionEvents waits on: status,
// completed steps, resets (a reset from opened changes nothing else), and
// the device slots, so a handover or an inactive device wakes every client.
func (s *Server) appSessionChangeKey(sess session.Session, now time.Time) string {
	states := ""
	for _, d := range []*session.DeviceAccess{sess.Access.Web, sess.Access.Native} {
		if d != nil {
			states += d.State
			if s.deviceStale(d, now) {
				states += ":stale"
			}
		}
		states += ","
	}
	return string(sess.Status) + "|" + strings.Join(sess.CompletedSteps(), ",") + "|" + strconv.Itoa(sess.ResetCount) +
		"|" + strconv.Itoa(sess.Access.Generation) + "|" + states
}

// nativeHandoffPending reports whether the browser should offer the Idem
// app's QR: while nfc_read (and document_capture with it) has not landed, or a
// native face step has not. Without a flow there is no step model, so it is
// offered for an nfc_passport session and never for a bound login.
func nativeHandoffPending(sess session.Session, fd *flow.FlowDefinition) bool {
	if fd == nil {
		return sess.Method == session.MethodNFCPassport
	}
	steps := fd.Steps
	selfieLocation := fd.EffectiveSelfieLocation()
	hasNFC := slices.Contains(steps, flow.StepNFCRead)
	hasDocumentPhoto := slices.Contains(steps, flow.StepDocumentPhoto)
	hasSelfieCluster := hasFaceStep(fd)
	selfieNative := hasSelfieCluster && selfieLocation == flow.LocationNative

	return (hasNFC && sess.Steps.NFC == nil) || (hasDocumentPhoto && sess.Steps.DocumentPhoto == nil) ||
		(selfieNative && sess.Steps.Selfie == nil)
}

// sessionByPathToken resolves the session of an app-facing route, where the
// path token is the only credential.
func (s *Server) sessionByPathToken(w http.ResponseWriter, r *http.Request) (session.Session, bool) {
	sess, err := s.sessions.Authenticate(r.PathValue("token"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return session.Session{}, false
	}
	return sess, true
}

// handleAppSessionResult refuses the single-shot result: every session runs a
// flow, whose evidence arrives step by step.
func (s *Server) handleAppSessionResult(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.appSessionByPathToken(w, r); !ok {
		return
	}
	writeErrorCode(w, http.StatusConflict, errCodeFlowStepsRequired,
		"this session runs a flow: submit each step through POST .../steps/... and then POST .../submit")
}
