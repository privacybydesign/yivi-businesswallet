package signing

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// The decline path: a pending signer refuses to sign a request outright, instead
// of the only prior outcome being to eventually sign it. It mirrors StartSign /
// StartExternalSign (a member and an external signee differ only in how they are
// identified), but runs no ceremony — it stops the request rather than starting
// one, so there is no authorize URL to return.

// DeclineSign lets an org member who is a pending signer of a request refuse to
// sign it. Unlike StartSign, decline does not require it to be the signer's turn
// in sequential mode: refusing up front is more useful than being forced to wait
// for a turn the signer already intends to reject. reason is optional free text
// shown to the requester.
func (s *Service) DeclineSign(ctx context.Context, orgID, userID, requestID uuid.UUID, reason string) error {
	req, err := s.store.GetRequest(ctx, orgID, requestID)
	if err != nil {
		return err
	}
	me := signerByUser(req, userID)
	if me == nil {
		return ErrNotSigner
	}
	return s.decline(ctx, orgID, req, me.ID, reason)
}

// DeclineExternalSign is DeclineSign's mirror for an external signee, resolved
// from their invitation token exactly as StartExternalSign is.
func (s *Service) DeclineExternalSign(ctx context.Context, token, reason string) error {
	ext, req, err := s.externalSigner(ctx, token)
	if err != nil {
		return err
	}
	return s.decline(ctx, ext.OrgID, req, ext.SignerID, reason)
}

// decline is the shared decline path. Guards mirror startSign's, minus the turn
// check: not a signer (ErrNotSigner), already signed (ErrAlreadySigned), the
// request no longer awaiting signatures (ErrInvalidRequest — including an
// already-declined request, since decline is terminal), and a ceremony in flight
// for this request (ErrSignInProgress) — a decline must not race the parked
// pdfsign pass that ceremony holds. Any signatures already applied stay on the
// stored document; the request just stops asking for more.
func (s *Service) decline(ctx context.Context, orgID uuid.UUID, req Request, signerID uuid.UUID, reason string) error {
	me := signerByID(req, signerID)
	if me == nil {
		return ErrNotSigner
	}
	if me.Status == SignerSigned {
		return ErrAlreadySigned
	}
	if req.Status != StatusAwaitingSignatures {
		return ErrInvalidRequest
	}
	if s.isActive(req.ID) {
		return ErrSignInProgress
	}
	reason = strings.TrimSpace(reason)
	if err := s.store.DeclineSigner(ctx, orgID, req.ID, signerID, reason); err != nil {
		return err
	}
	s.notifyRequesterDeclined(ctx, orgID, req, *me, reason)
	return nil
}

// isActive reports whether a signing ceremony currently holds the per-request
// in-flight lock, so decline can refuse to race it instead of stopping a request
// out from under a parked pdfsign pass.
func (s *Service) isActive(requestID uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[requestID] != nil
}

// notifyRequesterDeclined tells the request's creator that a signer refused.
// Best-effort: a nil notifier, an unresolved address, or a send failure is
// logged, never fatal — the decline itself is already recorded by the time this
// runs. by's Name/Email are only ever populated here for an external signee (the
// store leaves an internal member's blank, same as everywhere else in this
// package), so an internal decliner's display name is resolved from the member
// directory alongside the creator's address.
func (s *Service) notifyRequesterDeclined(ctx context.Context, orgID uuid.UUID, req Request, by Signer, reason string) {
	if s.notifier == nil {
		return
	}
	signerName, signerEmail := by.Name, by.Email
	creatorEmail := ""
	if s.members != nil {
		members, err := s.members.ListMembers(ctx, orgID)
		if err != nil {
			slog.WarnContext(ctx, "signing: list members to notify requester of decline", slog.String("error", err.Error()))
		} else {
			for _, m := range members {
				if m.UserID == req.CreatedBy {
					creatorEmail = m.Email
				}
				if by.UserID != nil && m.UserID == *by.UserID {
					signerName, signerEmail = m.Name, m.Email
				}
			}
		}
	}
	if creatorEmail == "" {
		return
	}
	if signerName == "" {
		signerName = signerEmail
	}
	if err := s.notifier.NotifyRequesterDeclined(ctx, orgID, creatorEmail, req.Filename, signerName, reason); err != nil {
		slog.WarnContext(ctx, "signing: notify requester of decline", slog.String("error", err.Error()))
	}
}
