// Device binding and handover for the app routes (/api/v1/app/{token}/...).
// The engine decides which device may drive a session: each session has one
// slot per client (session.DeviceRoleNative is the Idem app; the web slot
// is IPS's browser flow, which the wallet does not offer), each slot holds
// at most one device, and every app request is authorized by its
// X-Device-Token. The session token in the path only names the session.
//
//   - Claim: a slot is taken with a short-lived, single-use grant
//     (POST /api/v1/app/handover/{grantToken}/claim): the mailed or shown
//     vcmrtd link's claim token, or a fresh one the wallet asks for
//     (SessionHandover) once the first lapsed.
//   - Handover: the device holding a slot can mint a grant for it
//     (POST /api/v1/app/{token}/handover), and the wallet can once that
//     device is inactive or stale; redeeming it moves the slot to the new
//     device and revokes the old one (403 device_handed_over from then on).
//
// Every write re-checks the caller's slot and the session's state inside its
// own Update (checkAppWrite), so a request authorized before a handover,
// expiry or completion can't write after it.
package proofingengine

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// deviceTokenHeader carries a claimed device's token on every app request.
const deviceTokenHeader = "X-Device-Token"

const (
	eventDeviceClaimed        = "proofing.device.claimed"
	eventSessionHandedOver    = "proofing.session.handed_over"
	eventHandoverIssued       = "proofing.handover.issued"
	errCodeDeviceUnauth       = "device_unauthorized"
	errCodeDeviceHandedOver   = "device_handed_over"
	errCodeDeviceClaimed      = "device_already_claimed"
	errCodeSessionExpired     = "session_expired"
	errCodeSessionComplete    = "session_complete"
	errCodeHandoverInvalid    = "handover_invalid"
	errCodeHandoverExpired    = "handover_expired"
	errCodeHandoverUsed       = "handover_used"
	errCodeDeviceActive       = "device_active"
	errCodeClaimTokenRequired = "claim_token_required"
	errCodeFlowStepsRequired  = "flow_steps_required"
	errCodeStepsIncomplete    = "steps_incomplete"
)

// The rest of the device trail: every grant, state change, connection drop
// and refused request lands in the tenant's audit log too.
const (
	eventClaimTokenIssued    = "proofing.claim_token.issued"
	eventHandoverClaimFailed = "proofing.handover.claim_failed"
	eventDeviceStateChanged  = "proofing.device.state_changed"
	eventDeviceDisconnected  = "proofing.device.disconnected"
	eventDeviceReconnected   = "proofing.device.reconnected"
	eventAccessDenied        = "proofing.access.denied"
	eventStepDuplicate       = "proofing.step.duplicate"
)

// Session lifecycle as the app clients see it (appSessionView.Lifecycle):
// the flow-level state, independent of the outcome in Status.
const (
	lifecycleActive    = "ACTIVE"
	lifecycleComplete  = "COMPLETE"
	lifecycleExpired   = "EXPIRED"
	lifecycleCancelled = "CANCELLED"
)

// writeErrorCode is writeError plus a machine-readable code, for the errors
// a client has to react to differently (show "handed over", wipe its local
// session, ...) rather than just display.
func writeErrorCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// writeAccessError maps session's device/handover errors to their status
// and code.
func writeAccessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrDeviceHandedOver):
		writeErrorCode(w, http.StatusForbidden, errCodeDeviceHandedOver, "this verification session has been handed over to another device")
	case errors.Is(err, session.ErrDeviceUnauthorized):
		writeErrorCode(w, http.StatusUnauthorized, errCodeDeviceUnauth, "this device is not authorized for this session; claim it first ("+deviceTokenHeader+" missing or unknown)")
	case errors.Is(err, session.ErrDeviceAlreadyBound):
		writeErrorCode(w, http.StatusConflict, errCodeDeviceClaimed, err.Error())
	case errors.Is(err, session.ErrHandoverExpired):
		writeErrorCode(w, http.StatusGone, errCodeHandoverExpired, "handover code has expired; ask for a new one")
	case errors.Is(err, session.ErrHandoverUsed):
		writeErrorCode(w, http.StatusConflict, errCodeHandoverUsed, "handover code was already used")
	case errors.Is(err, session.ErrHandoverInvalid):
		writeErrorCode(w, http.StatusNotFound, errCodeHandoverInvalid, "handover code is invalid")
	case errors.Is(err, errSessionExpired):
		writeErrorCode(w, http.StatusGone, errCodeSessionExpired, "session expired")
	case errors.Is(err, errSessionComplete):
		writeErrorCode(w, http.StatusConflict, errCodeSessionComplete, "session already finished")
	case errors.Is(err, session.ErrInvalidDeviceRole), errors.Is(err, session.ErrInvalidDeviceState):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusConflict, err.Error())
	}
}

// accessErrorCode is err's machine-readable code when it's an access or
// session-state refusal, "" otherwise.
func accessErrorCode(err error) string {
	switch {
	case errors.Is(err, session.ErrDeviceHandedOver):
		return errCodeDeviceHandedOver
	case errors.Is(err, session.ErrDeviceUnauthorized):
		return errCodeDeviceUnauth
	case errors.Is(err, session.ErrDeviceAlreadyBound):
		return errCodeDeviceClaimed
	case errors.Is(err, session.ErrHandoverExpired):
		return errCodeHandoverExpired
	case errors.Is(err, session.ErrHandoverUsed):
		return errCodeHandoverUsed
	case errors.Is(err, session.ErrHandoverInvalid):
		return errCodeHandoverInvalid
	case errors.Is(err, errSessionExpired):
		return errCodeSessionExpired
	case errors.Is(err, errSessionComplete):
		return errCodeSessionComplete
	}
	return ""
}

// actorDetails names the device acting in an audit event: its slot and its
// random device id (never a token).
func actorDetails(sess session.Session, role session.DeviceRole) map[string]any {
	details := map[string]any{}
	if role == "" {
		return details
	}
	details["role"] = string(role)
	if d := sess.Access.Slot(role); d != nil {
		details["deviceId"] = d.ID
	}
	return details
}

// denyApp answers an app request refused for access or session state, and
// audits the refusal: which route, why, and as which device.
func (s *Server) denyApp(w http.ResponseWriter, r *http.Request, sess session.Session, caller appCaller, err error) {
	if code := accessErrorCode(err); code != "" {
		details := actorDetails(sess, caller.role)
		details["reason"], details["route"] = code, r.Pattern
		s.auditProofing(sess, eventAccessDenied, details)
	}
	writeAccessError(w, err)
}

var (
	errSessionExpired  = errors.New("session expired")
	errSessionComplete = errors.New("session already finished")
)

// sessionOpenForDevices reports why sess can no longer be claimed or handed
// over: an expired session rejects both, as does one that has an outcome.
func sessionOpenForDevices(sess session.Session) error {
	switch {
	case sess.Status == session.StatusExpired:
		return errSessionExpired
	case sess.Status.Terminal(), sess.Status == session.StatusNeedsReview:
		return errSessionComplete
	}
	return nil
}

// appCaller is who an app request was authorized as. Writes re-check it
// inside their Update (checkAppWrite) rather than trusting this snapshot.
type appCaller struct {
	role        session.DeviceRole
	deviceToken string
}

// appSessionByPathToken is sessionByPathToken plus device authorization:
// the request must carry the X-Device-Token of a device currently holding
// one of the session's slots. biometric_bound_login has no device slots;
// its routes stay session-token-only (caller.role is "").
func (s *Server) appSessionByPathToken(w http.ResponseWriter, r *http.Request) (session.Session, appCaller, bool) {
	sess, ok := s.sessionByPathToken(w, r)
	if !ok {
		return session.Session{}, appCaller{}, false
	}
	if sess.Method == session.MethodBiometricBoundLogin {
		return sess, appCaller{}, true
	}
	caller := appCaller{deviceToken: r.Header.Get(deviceTokenHeader)}
	role, err := sess.Access.Authorize(caller.deviceToken)
	if err != nil {
		s.denyApp(w, r, sess, appCaller{}, err)
		return session.Session{}, appCaller{}, false
	}
	caller.role = role
	return s.touchDevice(sess, role), caller, true
}

// checkAppWrite is what every app write checks inside its own Update, so it
// holds at the moment of the write: the caller still holds its slot (not
// handed over meanwhile) and the session can still take writes (not
// expired, not finished).
func checkAppWrite(sess *session.Session, caller appCaller) error {
	if caller.role != "" || sess.Access.Bound() {
		if err := sess.Access.AuthorizeAs(caller.deviceToken, caller.role); err != nil {
			return err
		}
	}
	return sessionOpenForDevices(*sess)
}

// deviceTouchInterval throttles touchDevice's write: a device's
// lastActiveAt only needs to be fresh to well within DeviceStaleAfter, not
// rewritten on every long-poll tick.
const deviceTouchInterval = 10 * time.Second

// touchDevice stamps role's lastActiveAt - any authorized request is a sign
// of life, so a client that crashed or was killed (and can't report itself
// inactive) goes stale on its own. A failed write only means a staler
// timestamp, so errors are ignored.
func (s *Server) touchDevice(sess session.Session, role session.DeviceRole) session.Session {
	now := time.Now().UTC()
	d := sess.Access.Slot(role)
	if d == nil || sessionOpenForDevices(sess) != nil || (d.DisconnectedAt == nil && now.Sub(d.LastActiveAt) < deviceTouchInterval) {
		return sess
	}
	var reconnectedAfter time.Duration
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if err := sessionOpenForDevices(*sess); err != nil {
			return err
		}
		if d := sess.Access.Slot(role); d != nil {
			if d.DisconnectedAt != nil {
				reconnectedAfter = now.Sub(*d.DisconnectedAt)
			}
			d.LastActiveAt, d.DisconnectedAt = now, nil
		}
		return nil
	})
	if err != nil {
		return sess
	}
	if reconnectedAfter > 0 {
		details := actorDetails(updated, role)
		details["disconnectedForSeconds"] = int(reconnectedAfter.Seconds())
		s.auditProofing(updated, eventDeviceReconnected, details)
	}
	return updated
}

// claimResponse is what a claim returns. Token is the session token for
// the /api/v1/app/{token} paths - it identifies the session, it doesn't
// authorize anything - and DeviceToken the X-Device-Token to send from now on.
type claimResponse struct {
	Token       string             `json:"token"`
	DeviceToken string             `json:"deviceToken"`
	Role        session.DeviceRole `json:"role"`
	Session     appSessionView     `json:"session"`
}

// handleSessionTokenClaim answers the removed session-token claim: the
// session token no longer authorizes taking a slot, only a claim token does.
func (s *Server) handleSessionTokenClaim(w http.ResponseWriter, r *http.Request) {
	writeErrorCode(w, http.StatusGone, errCodeClaimTokenRequired,
		"claiming with the session token is no longer supported; scan the claim or handover QR instead")
}

// handleClaimHandover redeems a grant token (from a claim or handover
// QR/deep link): the grant's slot moves to this device and the previous one,
// if any, is revoked. It is the same session - nothing but the slot's device
// changes.
//
//	@Summary	Claim a device slot, or continue a session on this device, with a grant token
//	@Tags		proofing-app
//	@Produce	json
//	@Param		handoverToken	path		string	true	"Claim or handover token from the QR"
//	@Success	200				{object}	api.claimResponse
//	@Failure	404				{object}	map[string]string
//	@Failure	409				{object}	map[string]string
//	@Failure	410				{object}	map[string]string
//	@Failure	429				{object}	map[string]string
//	@Router		/api/v1/app/handover/{handoverToken}/claim [post]
func (s *Server) handleClaimHandover(w http.ResponseWriter, r *http.Request) {
	handoverToken := r.PathValue("handoverToken")
	sess, err := s.sessions.FindByHandover(session.HashAccessToken(handoverToken))
	if err != nil {
		writeAccessError(w, session.ErrHandoverInvalid)
		return
	}
	if ok, retry := s.claimLimit.Allow(sess.ID); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
		writeError(w, http.StatusTooManyRequests, "too many claims for this session, try again later")
		return
	}
	var role session.DeviceRole
	var deviceToken, via, previousDeviceID string
	opened := false
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if err := sessionOpenForDevices(*sess); err != nil {
			return err
		}
		now := time.Now().UTC()
		if g := sess.Access.GrantByHash(session.HashAccessToken(handoverToken)); g != nil {
			if prev := sess.Access.Slot(g.Role); prev != nil {
				previousDeviceID = prev.ID
			}
		}
		var err error
		role, deviceToken, via, err = sess.Access.ClaimHandover(handoverToken, now)
		if err != nil {
			return err
		}
		opened, err = s.openLocked(sess, now)
		return err
	})
	if err != nil {
		// A scanned code that no longer works (used, expired, the session
		// over) is part of the session's story too.
		details := map[string]any{"reason": accessErrorCode(err)}
		if g := sess.Access.GrantByHash(session.HashAccessToken(handoverToken)); g != nil {
			details["role"] = string(g.Role)
		}
		s.auditProofing(sess, eventHandoverClaimFailed, details)
		writeAccessError(w, err)
		return
	}
	if opened {
		s.auditProofing(updated, eventSessionOpened, nil)
	}
	event := eventDeviceClaimed
	details := map[string]any{"role": string(role), "via": via, "deviceId": updated.Access.Slot(role).ID}
	if via == session.DeviceViaHandover {
		event = eventSessionHandedOver
		details["previousDeviceId"] = previousDeviceID
	}
	s.auditProofing(updated, event, details)
	s.writeClaimResponse(w, r, updated, appCaller{role: role, deviceToken: deviceToken}, deviceToken)
}

func (s *Server) writeClaimResponse(w http.ResponseWriter, r *http.Request, sess session.Session, caller appCaller, deviceToken string) {
	view, err := s.buildAppSessionView(r, sess, caller.role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not resolve flow: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, claimResponse{Token: sess.Token, DeviceToken: deviceToken, Role: caller.role, Session: view})
}

// mintHandoverRequest is POST .../handover's body. Role defaults to the
// caller's own slot.
type mintHandoverRequest struct {
	Role session.DeviceRole `json:"role,omitempty"`
}

// handoverResponse is a freshly minted handover. QR is what to render: the
// Web App URL for the web slot, the vcmrtd deep link for the native one.
// Neither carries the session token or any session data - only the
// handover token.
type handoverResponse struct {
	HandoverToken string             `json:"handoverToken"`
	Role          session.DeviceRole `json:"role"`
	ExpiresAt     time.Time          `json:"expiresAt"`
	QR            string             `json:"qr"`
	URL           string             `json:"url,omitempty"`
	DeepLink      string             `json:"deepLink,omitempty"`
	// SlotClaimed is true when a device already held the slot, so redeeming
	// this moves the session to another device rather than joining it first.
	SlotClaimed bool `json:"slotClaimed,omitempty"`
}

// handleMintHandover issues a short-lived, single-use grant for one of the
// session's slots, replacing that slot's pending one: a handover token for
// an occupied slot, the claim token for an empty one. Only a device that
// currently controls the session may do this.
//
//	@Summary	Create a handover QR for continuing on another device
//	@Tags		proofing-app
//	@Accept		json
//	@Produce	json
//	@Param		token	path		string					true	"Session token"
//	@Param		request	body		api.mintHandoverRequest	false	"Slot to hand over"
//	@Success	200		{object}	api.handoverResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	401		{object}	map[string]string
//	@Failure	403		{object}	map[string]string
//	@Failure	409		{object}	map[string]string
//	@Failure	410		{object}	map[string]string
//	@Router		/api/v1/app/{token}/handover [post]
func (s *Server) handleMintHandover(w http.ResponseWriter, r *http.Request) {
	sess, caller, ok := s.appSessionByPathToken(w, r)
	if !ok {
		return
	}
	if caller.role == "" {
		writeAccessError(w, session.ErrDeviceUnauthorized)
		return
	}
	var req mintHandoverRequest
	if r.ContentLength != 0 && !s.decode(w, r, &req) {
		return
	}
	role := req.Role
	if role == "" {
		role = caller.role
	}
	var token string
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if err := checkAppWrite(sess, caller); err != nil {
			return err
		}
		var err error
		token, err = sess.Access.MintHandover(role, s.grantExpiry(*sess, s.cfg.HandoverTokenTTL))
		return err
	})
	if err != nil {
		s.denyApp(w, r, sess, caller, err)
		return
	}
	expiresAt := updated.Access.Grant(role).ExpiresAt
	slotClaimed := updated.Access.Slot(role) != nil
	details := map[string]any{"role": string(role), "expiresAt": expiresAt, "slotClaimed": slotClaimed}
	if d := updated.Access.Slot(caller.role); d != nil {
		details["byRole"], details["byDeviceId"] = string(caller.role), d.ID
	}
	s.auditProofing(updated, eventHandoverIssued, details)
	resp := s.grantResponse(role, token, expiresAt)
	resp.SlotClaimed = slotClaimed
	writeJSON(w, http.StatusOK, resp)
}

// grantExpiry is when a grant minted now expires: after ttl, but never
// after the session itself.
func (s *Server) grantExpiry(sess session.Session, ttl time.Duration) time.Time {
	expiresAt := time.Now().UTC().Add(ttl)
	if sess.ExpiresAt.Before(expiresAt) {
		return sess.ExpiresAt
	}
	return expiresAt
}

// grantResponse is the vcmrtd deep link a phone scans to redeem a grant for
// the native slot. It carries no session token or data, only the grant.
func (s *Server) grantResponse(role session.DeviceRole, token string, expiresAt time.Time) handoverResponse {
	link := "vcmrtd://verify?handover=" + url.QueryEscape(token) + "&api=" + url.QueryEscape(s.apiBaseURL())
	return handoverResponse{HandoverToken: token, Role: role, ExpiresAt: expiresAt, QR: link, DeepLink: link}
}

// sessionClaims is createSessionResponse.Claims: each empty slot's claim
// token, as the QR/link to hand to that client.
type sessionClaims struct {
	Web    *handoverResponse `json:"web,omitempty"`
	Native *handoverResponse `json:"native,omitempty"`
}

// mintClaimTokens mints a fresh claim token for each of roles whose slot
// is still empty (a claimed slot moves only by handover).
func (s *Server) mintClaimTokens(sess session.Session, via string, roles ...session.DeviceRole) (session.Session, sessionClaims, error) {
	tokens := map[session.DeviceRole]string{}
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if err := sessionOpenForDevices(*sess); err != nil {
			return err
		}
		for _, role := range roles {
			if sess.Access.Slot(role) != nil {
				continue
			}
			token, err := sess.Access.MintHandover(role, s.grantExpiry(*sess, s.cfg.ClaimTokenTTL))
			if err != nil {
				return err
			}
			tokens[role] = token
		}
		return nil
	})
	if err != nil {
		return session.Session{}, sessionClaims{}, err
	}
	var claims sessionClaims
	for _, role := range roles {
		token, ok := tokens[role]
		if !ok {
			continue
		}
		resp := s.grantResponse(role, token, updated.Access.Grant(role).ExpiresAt)
		s.auditProofing(updated, eventClaimTokenIssued, map[string]any{"role": string(role), "via": via, "expiresAt": resp.ExpiresAt})
		if role == session.DeviceRoleWeb {
			claims.Web = &resp
		} else {
			claims.Native = &resp
		}
	}
	return updated, claims, nil
}

// deviceStateRequest is POST .../device/state's body.
type deviceStateRequest struct {
	State string `json:"state"`
}

// handleDeviceState records the calling device's self-reported state
// (active/inactive, e.g. the app went to the background) and stamps its
// lastActiveAt, so the other client can offer a handover when this one
// dropped out.
//
//	@Summary	Report this device as active or inactive
//	@Tags		proofing-app
//	@Accept		json
//	@Produce	json
//	@Param		token	path		string					true	"Session token"
//	@Param		request	body		api.deviceStateRequest	true	"active or inactive"
//	@Success	200		{object}	api.appSessionView
//	@Failure	400		{object}	map[string]string
//	@Failure	401		{object}	map[string]string
//	@Failure	403		{object}	map[string]string
//	@Router		/api/v1/app/{token}/device/state [post]
func (s *Server) handleDeviceState(w http.ResponseWriter, r *http.Request) {
	sess, caller, ok := s.appSessionByPathToken(w, r)
	if !ok {
		return
	}
	if caller.role == "" {
		writeAccessError(w, session.ErrDeviceUnauthorized)
		return
	}
	var req deviceStateRequest
	if !s.decode(w, r, &req) {
		return
	}
	// A finished or expired session takes no more writes of any kind - not
	// even a presence change - so it's refused without an audit row: the
	// audit trail of a completed session ends with its outcome.
	if err := sessionOpenForDevices(sess); err != nil {
		writeAccessError(w, err)
		return
	}
	previous, cancelled := "", false
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		if err := checkAppWrite(sess, caller); err != nil {
			return err
		}
		if d := sess.Access.Slot(caller.role); d != nil {
			previous = d.State
		}
		if err := sess.Access.SetDeviceState(caller.role, req.State, time.Now().UTC()); err != nil {
			return err
		}
		// Back again: the handover code left for this device's leaving is off.
		if req.State == session.DeviceStateActive {
			cancelled = sess.Access.CancelPendingHandover(caller.role)
		}
		return nil
	})
	if errors.Is(err, errSessionComplete) || errors.Is(err, errSessionExpired) {
		// Finished while this request was on its way - same as above.
		writeAccessError(w, err)
		return
	}
	if err != nil {
		s.denyApp(w, r, sess, caller, err)
		return
	}
	details := actorDetails(updated, caller.role)
	details["state"], details["previousState"] = req.State, previous
	if cancelled {
		details["pendingHandoverCancelled"] = true
	}
	s.auditProofing(updated, eventDeviceStateChanged, details)
	view, err := s.buildAppSessionView(r, updated, caller.role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not resolve flow: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// openLocked is handleAppSession's created -> opened move, for use inside
// an Update: it reports whether it transitioned. The expiry gets a fresh
// SessionOpenTTL window, capped by the hard maximum lifetime.
func (s *Server) openLocked(sess *session.Session, now time.Time) (bool, error) {
	if sess.Status != session.StatusCreated {
		return false, nil
	}
	if err := sess.SetStatus(session.StatusOpened, now); err != nil {
		return false, err
	}
	s.extendExpiry(sess, now.Add(s.cfg.SessionOpenTTL))
	return true, nil
}

// extendExpiry moves sess.ExpiresAt to until if that's later, but never
// past the hard maximum lifetime (Config.SessionMaxLifetime) counted from
// creation.
func (s *Server) extendExpiry(sess *session.Session, until time.Time) {
	until = s.capExpiry(sess.CreatedAt, until)
	if until.After(sess.ExpiresAt) {
		sess.ExpiresAt = until
	}
}

// capExpiry clamps t to createdAt + Config.SessionMaxLifetime (when set).
func (s *Server) capExpiry(createdAt, t time.Time) time.Time {
	if s.cfg.SessionMaxLifetime <= 0 || createdAt.IsZero() {
		return t
	}
	if hard := createdAt.Add(s.cfg.SessionMaxLifetime); t.After(hard) {
		return hard
	}
	return t
}

// deviceView is one slot as appSessionView shows it - never the token hash.
type deviceView struct {
	Claimed bool   `json:"claimed"`
	State   string `json:"state,omitempty"`
	// Stale is set when the device made no request for
	// Config.DeviceStaleAfter - it may have crashed or been killed without
	// reporting itself inactive. Treat it like state "inactive".
	Stale        bool       `json:"stale,omitempty"`
	ClaimedAt    *time.Time `json:"claimedAt,omitempty"`
	LastActiveAt *time.Time `json:"lastActiveAt,omitempty"`
}

// callerDeviceView is appSessionView.Device: the calling device's own
// authorization.
type callerDeviceView struct {
	Authorized bool               `json:"authorized"`
	Role       session.DeviceRole `json:"role,omitempty"`
	deviceView
}

func (s *Server) toDeviceView(d *session.DeviceAccess) deviceView {
	if d == nil {
		return deviceView{}
	}
	claimed, active := d.ClaimedAt, d.LastActiveAt
	return deviceView{Claimed: true, State: d.State, Stale: s.deviceStale(d, time.Now()), ClaimedAt: &claimed, LastActiveAt: &active}
}

// deviceAway reports whether d left its slot: inactive by its own report,
// or stale - what lets the relying party hand the slot over.
func (s *Server) deviceAway(d *session.DeviceAccess, now time.Time) bool {
	return d.State == session.DeviceStateInactive || s.deviceStale(d, now)
}

// deviceStale reports whether d looks gone: no request for DeviceStaleAfter,
// or its long-poll dropped and no new request within DeviceDisconnectGrace.
func (s *Server) deviceStale(d *session.DeviceAccess, now time.Time) bool {
	if d.DisconnectedAt != nil && s.cfg.DeviceDisconnectGrace > 0 && now.Sub(*d.DisconnectedAt) > s.cfg.DeviceDisconnectGrace {
		return true
	}
	return s.cfg.DeviceStaleAfter > 0 && now.Sub(d.LastActiveAt) > s.cfg.DeviceStaleAfter
}

// markDisconnected records that role's device (still deviceID) dropped its
// long-poll - see DeviceAccess.DisconnectedAt. Errors only mean a later
// stale, so they're ignored.
func (s *Server) markDisconnected(sess session.Session, role session.DeviceRole, deviceID string) {
	updated, err := s.sessions.Update(sess.TenantID, sess.ID, func(sess *session.Session) error {
		d := sess.Access.Slot(role)
		if d == nil || d.ID != deviceID || sessionOpenForDevices(*sess) != nil {
			return errNothingToMark
		}
		now := time.Now().UTC()
		d.DisconnectedAt = &now
		return nil
	})
	if err == nil {
		s.auditProofing(updated, eventDeviceDisconnected, actorDetails(updated, role))
	}
}

var errNothingToMark = errors.New("device changed")

// stepResultView is one completed step's server-side result, as the app
// clients see it: whether it's done and its verdicts - never the evidence
// itself (images, chip data, document identity).
type stepResultView struct {
	Step        string         `json:"step"`
	Completed   bool           `json:"completed"`
	CompletedAt *time.Time     `json:"completedAt,omitempty"`
	Summary     map[string]any `json:"summary,omitempty"`
}

func stepResultFor(sess session.Session, step flow.Step) stepResultView {
	out := stepResultView{Step: string(step)}
	at := func(t session.StepTiming) *time.Time {
		if t.SubmittedAt.IsZero() {
			return nil
		}
		return &t.SubmittedAt
	}
	switch {
	case step == flow.StepDocumentCapture && sess.Steps.Document != nil:
		d := sess.Steps.Document
		out.Completed, out.CompletedAt = true, at(d.Timing)
		out.Summary = map[string]any{"documentType": d.Parsed.DocumentType, "allChecksValid": d.Parsed.AllChecksValid}
	case step == flow.StepNFCRead && sess.Steps.NFC != nil:
		out.Completed, out.CompletedAt = true, at(sess.Steps.NFC.Timing)
	case step == flow.StepDocumentPhoto && sess.Steps.DocumentPhoto != nil:
		out.Completed, out.CompletedAt = true, at(sess.Steps.DocumentPhoto.Timing)
	case isFaceStep(step) && sess.Steps.Selfie != nil:
		sel := sess.Steps.Selfie
		out.Completed, out.CompletedAt = true, at(sel.Timing)
		out.Summary = map[string]any{"livenessPassed": sel.LivenessPassed}
		if sel.FaceVerified != nil {
			out.Summary["faceVerified"] = *sel.FaceVerified
		}
	}
	return out
}

// stepResults is appSessionView.StepResults: every completed step of fd.
func stepResults(sess session.Session, fd *flow.FlowDefinition) map[string]stepResultView {
	out := map[string]stepResultView{}
	if fd == nil {
		return out
	}
	for _, step := range fd.Steps {
		if res := stepResultFor(sess, step); res.Completed {
			out[string(step)] = res
		}
	}
	return out
}

// currentStep is the server-defined step the user is on: the first of fd's
// steps without a result yet, "" once the session is no longer active. nil
// when no flow governs the session - there is no step model to say which
// step is current, and "" would tell a client (vcmrtd reads it that way)
// there's nothing left to do on a session that hasn't even started.
func currentStep(sess session.Session, fd *flow.FlowDefinition) *string {
	if fd == nil {
		return nil
	}
	step := ""
	if sessionLifecycle(sess) == lifecycleActive {
		if _, remaining := flowStepProgress(sess, fd); len(remaining) > 0 {
			step = remaining[0]
		}
	}
	return &step
}

// sessionLifecycle is appSessionView.Lifecycle. COMPLETE means every step
// has a result and the server has decided the outcome (approved, rejected
// or needs_review, in Status).
func sessionLifecycle(sess session.Session) string {
	switch {
	case sess.Status == session.StatusExpired:
		return lifecycleExpired
	case sess.Status == session.StatusCancelled:
		return lifecycleCancelled
	case sess.Status.Terminal(), sess.Status == session.StatusNeedsReview:
		return lifecycleComplete
	}
	return lifecycleActive
}

// deviceParticipationView is one device's part in the session, as the
// relying party sees it (sessionView.Devices): which slot it held, how it
// got there, when it stopped and which steps it submitted. Only the random
// device id and timestamps - no device details, never a token hash.
type deviceParticipationView struct {
	DeviceID     string             `json:"deviceId"`
	Role         session.DeviceRole `json:"role"`
	Via          string             `json:"via"`
	Current      bool               `json:"current"`
	ClaimedAt    time.Time          `json:"claimedAt"`
	LastActiveAt *time.Time         `json:"lastActiveAt,omitempty"`
	// Away is set on a current device that reported itself inactive or went
	// stale (sessionStatusView only): the relying party may hand its slot over.
	Away      bool       `json:"away,omitempty"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	EndReason string     `json:"endReason,omitempty"`
	Steps     []string   `json:"steps,omitempty"`
}

func deviceParticipation(a session.Access) []deviceParticipationView {
	var out []deviceParticipationView
	for _, p := range a.History {
		v := deviceParticipationView{
			DeviceID: p.DeviceID, Role: p.Role, Via: p.Via, ClaimedAt: p.ClaimedAt,
			LastActiveAt: p.LastActiveAt, EndedAt: p.EndedAt, EndReason: p.EndReason, Steps: p.Steps,
		}
		if d := a.Slot(p.Role); d != nil && d.ID == p.DeviceID {
			last := d.LastActiveAt
			v.Current, v.LastActiveAt = true, &last
		}
		out = append(out, v)
	}
	return out
}

// statusDevices is deviceParticipation with each current device's Away set.
func (s *Server) statusDevices(a session.Access, now time.Time) []deviceParticipationView {
	out := deviceParticipation(a)
	for i := range out {
		if d := a.Slot(out[i].Role); out[i].Current && d != nil {
			out[i].Away = s.deviceAway(d, now)
		}
	}
	return out
}
