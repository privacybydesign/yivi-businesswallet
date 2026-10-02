package session

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"slices"
	"time"
)

// DeviceRole names one of a session's device slots. A session has one slot
// per client application - the browser Web App and the native app
// (vcmrtd/IDEM) - and each slot holds at most one active device at a time.
type DeviceRole string

const (
	DeviceRoleWeb    DeviceRole = "web"
	DeviceRoleNative DeviceRole = "native"
)

// DeviceRoles is every slot, in a fixed order.
var DeviceRoles = []DeviceRole{DeviceRoleWeb, DeviceRoleNative}

// Valid reports whether r is a known device role.
func (r DeviceRole) Valid() bool { return r == DeviceRoleWeb || r == DeviceRoleNative }

// Device states a client reports about itself (see Access.SetDeviceState).
const (
	DeviceStateActive   = "active"
	DeviceStateInactive = "inactive"
)

// How a device came to hold its slot (DeviceParticipation.Via): a claim
// token for a slot nobody held yet, or a handover from the device before it.
const (
	DeviceViaClaim    = "claim"
	DeviceViaHandover = "handover"
)

// Errors returned by Access's methods; api maps each to its own HTTP status
// and error code.
var (
	ErrDeviceUnauthorized = errors.New("session: device not authorized for this session")
	ErrDeviceHandedOver   = errors.New("session: this session has been handed over to another device")
	ErrDeviceAlreadyBound = errors.New("session: this device slot is already claimed; use a handover token")
	ErrHandoverInvalid    = errors.New("session: handover token is invalid")
	ErrHandoverExpired    = errors.New("session: handover token has expired")
	ErrHandoverUsed       = errors.New("session: handover token was already used")
	ErrInvalidDeviceRole  = errors.New("session: unknown device role")
	ErrInvalidDeviceState = errors.New("session: unknown device state")
)

// maxRevokedDeviceHashes caps Access.Revoked.
const maxRevokedDeviceHashes = 16

// maxDeviceHistory caps Access.History; the oldest entries go first.
const maxDeviceHistory = 32

// Access is a session's device authorization state: which device currently
// controls each slot, which devices it has already been handed away from,
// each slot's pending claim/handover grant, and every device that ever took
// part. Only token hashes are ever stored - the plaintext device and grant
// tokens exist solely in the response that issued them.
type Access struct {
	Web    *DeviceAccess `json:"web,omitempty"`
	Native *DeviceAccess `json:"native,omitempty"`
	// Revoked are the token hashes of devices that were handed away from,
	// so such a device gets ErrDeviceHandedOver (it can tell the user why)
	// instead of the generic ErrDeviceUnauthorized. Capped at
	// maxRevokedDeviceHashes, oldest dropped first.
	Revoked []string `json:"revoked,omitempty"`
	// WebGrant/NativeGrant are each slot's most recently minted grant: a
	// claim token while the slot is empty, a handover token once a device
	// holds it. Minting replaces that slot's grant only, so a web and a
	// native QR stay valid side by side.
	WebGrant    *HandoverGrant `json:"webGrant,omitempty"`
	NativeGrant *HandoverGrant `json:"nativeGrant,omitempty"`
	// Generation goes up on every claim, so a waiting client (the
	// /events long-poll) notices a handover - see api.appSessionChangeKey.
	Generation int `json:"generation,omitempty"`
	// History is every device that held a slot, including handed-over ones,
	// for the audit/result view - never used for authorization.
	History []DeviceParticipation `json:"history,omitempty"`
}

// DeviceAccess is one slot's current device.
type DeviceAccess struct {
	// ID is a random, non-secret identifier for this device's claim,
	// linking it to its DeviceParticipation entry.
	ID           string    `json:"id,omitempty"`
	TokenHash    string    `json:"tokenHash"`
	State        string    `json:"state"`
	ClaimedAt    time.Time `json:"claimedAt"`
	LastActiveAt time.Time `json:"lastActiveAt"`
	// DisconnectedAt is when this device's /events long-poll was dropped by
	// the client (e.g. the app was killed - iOS kills an app swiped away
	// without it ever reporting itself inactive); cleared by its next request.
	DisconnectedAt *time.Time `json:"disconnectedAt,omitempty"`
}

// DeviceParticipation is one device's part in a session: when it held which
// slot, how it got there, when it stopped and which steps it submitted.
type DeviceParticipation struct {
	DeviceID  string     `json:"deviceId"`
	Role      DeviceRole `json:"role"`
	Via       string     `json:"via"`
	ClaimedAt time.Time  `json:"claimedAt"`
	// EndedAt/EndReason are set once another device took the slot over.
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	EndReason string     `json:"endReason,omitempty"`
	// LastActiveAt is only kept here once the device is gone; a current
	// device's is on its slot.
	LastActiveAt *time.Time `json:"lastActiveAt,omitempty"`
	Steps        []string   `json:"steps,omitempty"`
}

// HandoverGrant is a short-lived, single-use grant to take Role's slot.
type HandoverGrant struct {
	Role      DeviceRole `json:"role"`
	TokenHash string     `json:"tokenHash"`
	ExpiresAt time.Time  `json:"expiresAt"`
	UsedAt    *time.Time `json:"usedAt,omitempty"`
}

// Clone returns a deep copy of a, so a change made through the copy's
// pointers and slices (touching a slot's LastActiveAt, RecordStep appending
// to a History entry) never reaches a. The in-memory Store needs this:
// its Update hands fn a copy of the stored Session, and without a deep copy
// those writes would land in the stored session - and in snapshots other
// goroutines are reading - before the update is even committed, and survive
// a failed flush's rollback. Time pointers are copied as pointers: callers
// replace them (d.DisconnectedAt = &now), never write through them.
func (a Access) Clone() Access {
	out := a
	if a.Web != nil {
		d := *a.Web
		out.Web = &d
	}
	if a.Native != nil {
		d := *a.Native
		out.Native = &d
	}
	if a.WebGrant != nil {
		g := *a.WebGrant
		out.WebGrant = &g
	}
	if a.NativeGrant != nil {
		g := *a.NativeGrant
		out.NativeGrant = &g
	}
	out.Revoked = slices.Clone(a.Revoked)
	if a.History != nil {
		out.History = make([]DeviceParticipation, len(a.History))
		for i, p := range a.History {
			p.Steps = slices.Clone(p.Steps)
			out.History[i] = p
		}
	}
	return out
}

// Bound reports whether any device has claimed this session yet.
func (a Access) Bound() bool { return a.Web != nil || a.Native != nil }

// Slot returns role's current device, or nil.
func (a Access) Slot(role DeviceRole) *DeviceAccess {
	switch role {
	case DeviceRoleWeb:
		return a.Web
	case DeviceRoleNative:
		return a.Native
	}
	return nil
}

func (a *Access) setSlot(role DeviceRole, d *DeviceAccess) {
	if role == DeviceRoleWeb {
		a.Web = d
	} else {
		a.Native = d
	}
}

// Grant returns role's pending grant, or nil.
func (a Access) Grant(role DeviceRole) *HandoverGrant {
	switch role {
	case DeviceRoleWeb:
		return a.WebGrant
	case DeviceRoleNative:
		return a.NativeGrant
	}
	return nil
}

func (a *Access) setGrant(role DeviceRole, g *HandoverGrant) {
	if role == DeviceRoleWeb {
		a.WebGrant = g
	} else {
		a.NativeGrant = g
	}
}

// GrantByHash returns the grant (of either slot) whose token hashes to
// tokenHash, or nil.
func (a Access) GrantByHash(tokenHash string) *HandoverGrant {
	for _, role := range DeviceRoles {
		if g := a.Grant(role); g != nil && subtle.ConstantTimeCompare([]byte(g.TokenHash), []byte(tokenHash)) == 1 {
			return g
		}
	}
	return nil
}

// Authorize resolves deviceToken to the slot it currently controls.
func (a Access) Authorize(deviceToken string) (DeviceRole, error) {
	if deviceToken == "" {
		return "", ErrDeviceUnauthorized
	}
	hash := HashAccessToken(deviceToken)
	for _, role := range DeviceRoles {
		if d := a.Slot(role); d != nil && subtle.ConstantTimeCompare([]byte(d.TokenHash), []byte(hash)) == 1 {
			return role, nil
		}
	}
	for _, revoked := range a.Revoked {
		if subtle.ConstantTimeCompare([]byte(revoked), []byte(hash)) == 1 {
			return "", ErrDeviceHandedOver
		}
	}
	return "", ErrDeviceUnauthorized
}

// AuthorizeAs is Authorize plus the check that deviceToken still controls
// role - what every write re-checks inside its own Update, so a device that
// was handed away from mid-request can't write.
func (a Access) AuthorizeAs(deviceToken string, role DeviceRole) error {
	got, err := a.Authorize(deviceToken)
	if err != nil {
		return err
	}
	if got != role {
		return ErrDeviceUnauthorized
	}
	return nil
}

// MintHandover issues a new grant for role, replacing that slot's pending
// one (the other slot's is untouched). The grant expires at expiresAt. It
// returns the plaintext token.
func (a *Access) MintHandover(role DeviceRole, expiresAt time.Time) (string, error) {
	if !role.Valid() {
		return "", ErrInvalidDeviceRole
	}
	token := newAccessToken()
	a.setGrant(role, &HandoverGrant{Role: role, TokenHash: HashAccessToken(token), ExpiresAt: expiresAt})
	return token, nil
}

// ClaimHandover redeems a grant token: the grant's slot is bound to a new
// device and whichever device held it before is revoked. The session
// itself is unchanged. It returns the role, the new device token and how
// the device got the slot (DeviceViaClaim for an empty slot).
func (a *Access) ClaimHandover(grantToken string, now time.Time) (DeviceRole, string, string, error) {
	if grantToken == "" {
		return "", "", "", ErrHandoverInvalid
	}
	g := a.GrantByHash(HashAccessToken(grantToken))
	if g == nil {
		return "", "", "", ErrHandoverInvalid
	}
	if g.UsedAt != nil {
		return "", "", "", ErrHandoverUsed
	}
	if !now.Before(g.ExpiresAt) {
		return "", "", "", ErrHandoverExpired
	}
	used := now
	g.UsedAt = &used
	via := DeviceViaClaim
	if prev := a.Slot(g.Role); prev != nil {
		via = DeviceViaHandover
		a.Revoked = append(a.Revoked, prev.TokenHash)
		if len(a.Revoked) > maxRevokedDeviceHashes {
			a.Revoked = a.Revoked[len(a.Revoked)-maxRevokedDeviceHashes:]
		}
		if p := a.participation(prev.ID); p != nil {
			ended, last := now, prev.LastActiveAt
			p.EndedAt, p.EndReason, p.LastActiveAt = &ended, "handed_over", &last
		}
	}
	return g.Role, a.bind(g.Role, via, now), via, nil
}

// SetDeviceState records role's self-reported active/inactive state.
func (a *Access) SetDeviceState(role DeviceRole, state string, now time.Time) error {
	if state != DeviceStateActive && state != DeviceStateInactive {
		return ErrInvalidDeviceState
	}
	d := a.Slot(role)
	if d == nil {
		return ErrDeviceUnauthorized
	}
	d.State = state
	d.LastActiveAt = now
	return nil
}

// CancelPendingHandover drops role's unused grant while a device holds that
// slot - that device came back, so the handover it left for is off. It
// reports whether there was one.
func (a *Access) CancelPendingHandover(role DeviceRole) bool {
	g := a.Grant(role)
	if g == nil || g.UsedAt != nil || a.Slot(role) == nil {
		return false
	}
	a.setGrant(role, nil)
	return true
}

// RecordStep notes on role's current device that it submitted step.
func (a *Access) RecordStep(role DeviceRole, step string) {
	d := a.Slot(role)
	if d == nil {
		return
	}
	if p := a.participation(d.ID); p != nil && !slices.Contains(p.Steps, step) {
		p.Steps = append(p.Steps, step)
	}
}

func (a *Access) participation(deviceID string) *DeviceParticipation {
	if deviceID == "" {
		return nil
	}
	for i := range a.History {
		if a.History[i].DeviceID == deviceID {
			return &a.History[i]
		}
	}
	return nil
}

func (a *Access) bind(role DeviceRole, via string, now time.Time) string {
	token := newAccessToken()
	id := newID()
	a.setSlot(role, &DeviceAccess{ID: id, TokenHash: HashAccessToken(token), State: DeviceStateActive, ClaimedAt: now, LastActiveAt: now})
	a.History = append(a.History, DeviceParticipation{DeviceID: id, Role: role, Via: via, ClaimedAt: now})
	if len(a.History) > maxDeviceHistory {
		a.History = a.History[len(a.History)-maxDeviceHistory:]
	}
	a.Generation++
	return token
}

// HashAccessToken is how device and grant tokens are stored and looked up
// (see FindByHandover): SHA-256, hex. The tokens carry 256 random bits, so
// an unsalted fast hash is enough.
func HashAccessToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newAccessToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
