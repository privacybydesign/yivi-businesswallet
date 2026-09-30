package proofing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
)

const SessionChannel = "identity_proofing_sessions"

const (
	ipsEventMaxSkew  = 5 * time.Minute
	deadlineRetry    = 30 * time.Second
	earlyEventWindow = 2 * time.Minute
)

var (
	ErrBadSignature  = errors.New("proofing: bad IPS event signature")
	ErrEventTooEarly = errors.New("proofing: IPS event before its session is stored")
)

type ipsEvent struct {
	Event           string    `json:"event"`
	OccurredAt      time.Time `json:"occurredAt"`
	TenantID        string    `json:"tenantId"`
	SessionID       string    `json:"sessionId"`
	ClientReference string    `json:"clientReference"`
}

// HandleIPSEvent verifies a session change IPS pushed and reconciles that one
// request. An event for a tenant or session the wallet does not know is
// acknowledged and dropped, so IPS stops retrying it; only a recent one naming
// a request (clientReference) is refused, as its session may still be stored.
func (s *Service) HandleIPSEvent(ctx context.Context, body []byte, timestamp, signature string) error {
	var ev ipsEvent
	if err := json.Unmarshal(body, &ev); err != nil || ev.TenantID == "" || ev.SessionID == "" {
		return fmt.Errorf("%w: malformed IPS event", ErrInvalidInput)
	}
	// The IPS tenant id is the org's id.
	orgID, err := uuid.Parse(ev.TenantID)
	var secret string
	if err == nil {
		secret, err = s.settings.WebhookSecret(ctx, orgID)
		if err != nil && !errors.Is(err, ErrNotProvisioned) {
			return err
		}
	}
	if err != nil {
		slog.InfoContext(ctx, "identity proofing: IPS event for an unknown tenant", slog.String("event", ev.Event))
		return nil
	}
	if err := verifyIPSSignature(secret, body, timestamp, signature, s.now()); err != nil {
		return err
	}
	req, err := s.requests.GetBySession(ctx, ev.SessionID)
	if errors.Is(err, ErrRequestNotFound) && ev.early(s.now()) {
		return ErrEventTooEarly
	}
	if errors.Is(err, ErrRequestNotFound) || err == nil && req.OrganizationID != orgID {
		slog.InfoContext(ctx, "identity proofing: IPS event for an unknown session",
			slog.String("event", ev.Event), slog.String("org_id", orgID.String()))
		return nil
	}
	if err != nil {
		return err
	}
	if !req.needsReconcile() {
		return nil
	}
	apiKey, err := s.requestAPIKey(ctx, req)
	if err != nil {
		return err
	}
	_, err = s.tryReconcile(ctx, apiKey, req)
	return err
}

func (ev ipsEvent) early(now time.Time) bool {
	if _, err := uuid.Parse(ev.ClientReference); err != nil {
		return false
	}
	return now.Sub(ev.OccurredAt) < earlyEventWindow
}

func (s *Service) SessionChanged(ctx context.Context, sessionID string) {
	req, err := s.requests.GetBySession(ctx, sessionID)
	if err == nil && req.needsReconcile() {
		var apiKey string
		if apiKey, err = s.requestAPIKey(ctx, req); err == nil {
			s.reconcile(ctx, apiKey, req)
		}
	}
	if err != nil {
		slog.WarnContext(ctx, "identity proofing: session change not reconciled", slog.Any("error", err))
	}
}

func verifyIPSSignature(secret string, body []byte, timestamp, signature string, now time.Time) error {
	if secret == "" {
		return ErrBadSignature
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrBadSignature
	}
	if skew := now.Sub(time.Unix(ts, 0)); skew > ipsEventMaxSkew || skew < -ipsEventMaxSkew {
		return ErrBadSignature
	}
	got, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return ErrBadSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return ErrBadSignature
	}
	return nil
}

// ReconcileDue ends every hosted link that lapsed unstarted, asks IPS about
// every session whose cap has passed without the wallet seeing it end, so its
// expiry (and webhook) lands on time, and returns the next deadline: a
// database.Job, woken early by SessionChannel.
func (s *Service) ReconcileDue(ctx context.Context) (time.Time, error) {
	ctx = audit.WithoutActor(ctx)
	now := s.now()
	lapsed, err := s.requests.LapseLinks(ctx, now, maxReconcilePerRound)
	if err != nil {
		return time.Time{}, err
	}
	if lapsed == maxReconcilePerRound {
		return now, nil
	}
	due, err := s.requests.ListDue(ctx, now, maxReconcilePerRound)
	if err != nil {
		return time.Time{}, err
	}
	type tenantOf struct {
		org  uuid.UUID
		mode Mode
	}
	keys := map[tenantOf]string{}
	open, settled := false, 0
	for _, req := range due {
		tenant := tenantOf{req.OrganizationID, req.mode()}
		apiKey, ok := keys[tenant]
		if !ok {
			if apiKey, err = s.requestAPIKey(ctx, req); err != nil {
				slog.WarnContext(ctx, "identity proofing: deadline skipped an org",
					slog.String("org_id", req.OrganizationID.String()), slog.Any("error", err))
			}
			keys[tenant] = apiKey
		}
		if apiKey == "" {
			continue
		}
		if s.reconcile(ctx, apiKey, req).stillOpen() {
			open = true
		} else {
			settled++
		}
	}
	if len(due) == maxReconcilePerRound && settled > 0 {
		return now, nil
	}
	next, err := s.requests.NextDeadline(ctx, now)
	if err != nil {
		return time.Time{}, err
	}
	if retry := now.Add(deadlineRetry); open && (next.IsZero() || retry.Before(next)) {
		next = retry
	}
	return next, nil
}

func (r Request) stillOpen() bool {
	return (r.Status == StatusPending || r.Status == StatusInProgress) && r.session != nil && r.session.EndedAt == nil
}
