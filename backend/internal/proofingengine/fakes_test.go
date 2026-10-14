package proofingengine

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regulasweep"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// memSessions is an in-memory session.Interface: Update works on a copy and
// stores it only when fn succeeds, as the Postgres store's transaction does.
// The Yivi state is copied too, so a caller's snapshot never shares it with
// the stored row, as a row read from Postgres does not.
type memSessions struct {
	mu   sync.Mutex
	byID map[string]session.Session
}

func newMemSessions(sessions ...session.Session) *memSessions {
	m := &memSessions{byID: map[string]session.Session{}}
	for _, s := range sessions {
		m.byID[s.ID] = copyYivi(s)
	}
	return m
}

func (m *memSessions) Create(sess session.Session) (session.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byID[sess.ID] = copyYivi(sess)
	return sess, nil
}

func (m *memSessions) Get(_, id string) (session.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.byID[id]
	if !ok {
		return session.Session{}, session.ErrNotFound
	}
	return copyYivi(sess), nil
}

// copyYivi is sess with its own copy of the Yivi state.
func copyYivi(sess session.Session) session.Session {
	if sess.Yivi != nil {
		st := *sess.Yivi
		st.FrameHashes = slices.Clone(st.FrameHashes)
		sess.Yivi = &st
	}
	return sess
}

func (m *memSessions) Authenticate(token string) (session.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sess := range m.byID {
		if sess.Token == token {
			return sess, nil
		}
	}
	return session.Session{}, session.ErrNotFound
}

func (m *memSessions) AuthenticateSession(tenantID, id, token string) (session.Session, error) {
	sess, err := m.Get(tenantID, id)
	if err != nil || sess.Token != token {
		return session.Session{}, session.ErrNotFound
	}
	return sess, nil
}

func (m *memSessions) FindByHandover(string) (session.Session, error) {
	return session.Session{}, session.ErrNotFound
}

func (m *memSessions) Update(_, id string, fn func(*session.Session) error) (session.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.byID[id]
	if !ok {
		return session.Session{}, session.ErrNotFound
	}
	sess := copyYivi(stored)
	if err := fn(&sess); err != nil {
		return session.Session{}, err
	}
	m.byID[id] = sess
	return copyYivi(sess), nil
}

func (m *memSessions) Delete(_, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.byID, id)
	return nil
}

func (m *memSessions) Purge(time.Duration, time.Time) ([]session.Session, error) { return nil, nil }

func (m *memSessions) SetOnExpire(func(session.Session)) {}

// settledQueue records Settle; the rest of regulasweep.Queue is unused here.
type settledQueue struct {
	regulasweep.Queue
	tag   string
	dueAt time.Time
}

func (q *settledQueue) Settle(_ context.Context, tag string, dueAt time.Time) error {
	q.tag, q.dueAt = tag, dueAt
	return nil
}

// facelessRegula finds no face in any frame and counts its calls; it is safe
// for concurrent frames. hold keeps each call in flight that long.
type facelessRegula struct {
	calls atomic.Int64
	hold  time.Duration
}

func (*facelessRegula) GetLiveness(context.Context, string) (regula.LivenessTransaction, error) {
	return regula.LivenessTransaction{}, regula.ErrTransactionNotFound
}

func (*facelessRegula) Match(context.Context, string, string) (regula.MatchResult, error) {
	return regula.MatchResult{}, nil
}

func (f *facelessRegula) MatchImages(context.Context, string, string) (regula.ImageMatch, error) {
	f.calls.Add(1)
	time.Sleep(f.hold)
	return regula.ImageMatch{}, nil
}

func (*facelessRegula) DeleteLiveness(context.Context, string) error { return nil }

func inProgress(id string) session.Session {
	return session.Session{ID: id, TenantID: "org", Token: "tok-" + id, Status: session.StatusInProgress, TenantReference: "ref-" + id}
}
