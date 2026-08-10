package signing

import (
	"context"
	"errors"
	"net/url"
)

// The external-signee flow. An external signee is a party outside the organisation
// who was added as a signer of a co-signing request by name + e-mail. They have no
// membership and no session, so their only key is the one-time invitation token
// mailed to them: every call here starts by resolving that token to a signer row,
// and the org context comes from the request, never from the caller. Beyond that
// they run exactly the ceremonies a member runs — link a QTSP credential with their
// own EUDI wallet, then sign the current document — which is what makes the finished
// PDF carry one incremental PAdES signature per signer regardless of kind.

// ExternalView returns what the external signing page shows behind an invitation
// link, or ErrInvalidToken when the link is unknown or expired.
func (s *Service) ExternalView(ctx context.Context, token string) (ExternalView, error) {
	ext, req, err := s.externalSigner(ctx, token)
	if err != nil {
		return ExternalView{}, err
	}
	me := signerByID(req, ext.SignerID)
	if me == nil {
		return ExternalView{}, ErrInvalidToken
	}

	view := ExternalView{
		Filename:     req.Filename,
		SignerName:   me.Name,
		SignerEmail:  me.Email,
		Message:      req.Message,
		Status:       req.Status,
		SignerStatus: me.Status,
		Mode:         req.Mode,
		SignerCount:  len(req.Signers),
		CreatedAt:    req.CreatedAt,
	}
	for _, sg := range req.Signers {
		if sg.Status == SignerSigned {
			view.SignedCount++
		}
	}
	if s.orgs != nil {
		name, err := s.orgs.OrgName(ctx, ext.OrgID)
		if err != nil {
			return ExternalView{}, err
		}
		view.OrgName = name
	}
	if _, err := s.store.GetCredential(ctx, ext.OrgID, externalSubject(me.Email)); err == nil {
		view.HasCredential = true
	} else if !errors.Is(err, ErrNoCredential) {
		return ExternalView{}, err
	}
	view.CanSign = req.Status == StatusAwaitingSignatures &&
		checkTurn(req, me.ID) == nil && view.HasCredential
	return view, nil
}

// StartExternalLink begins the credential-link ceremony for an external signee: the
// same service-scope authorization a member runs, but the resulting credential is
// cached under their (org, e-mail) subject rather than an internal user row. A signee
// who has already signed cannot re-link: their link stays readable so they can see
// what they signed, and nothing is left for it to authorize — so it must not be able
// to replace the certificate that subject signs a later request with.
func (s *Service) StartExternalLink(ctx context.Context, token string) (Start, error) {
	ext, req, err := s.externalSigner(ctx, token)
	if err != nil {
		return Start{}, err
	}
	me := signerByID(req, ext.SignerID)
	if me == nil {
		return Start{}, ErrInvalidToken
	}
	if me.Status == SignerSigned {
		return Start{}, ErrAlreadySigned
	}
	ref := signerRef{signerID: ext.SignerID, subj: externalSubject(ext.Email), token: token}
	return s.startLink(ctx, ext.OrgID, ref, "")
}

// StartExternalSign begins an external signee's signing ceremony for the request
// their link belongs to, under the same turn and in-flight rules as a member's.
func (s *Service) StartExternalSign(ctx context.Context, token string) (Start, error) {
	ext, req, err := s.externalSigner(ctx, token)
	if err != nil {
		return Start{}, err
	}
	ref := signerRef{signerID: ext.SignerID, subj: externalSubject(ext.Email), token: token}
	return s.startSign(ctx, ext.OrgID, req, ref, "")
}

// ExternalDocument returns the document the external signee is being asked to sign —
// the accumulating signed PDF if earlier signers have already signed, else the
// original upload — so they can read what they are about to put their signature on.
func (s *Service) ExternalDocument(ctx context.Context, token string) ([]byte, string, error) {
	ext, _, err := s.externalSigner(ctx, token)
	if err != nil {
		return nil, "", err
	}
	return s.store.GetLatestDocument(ctx, ext.OrgID, ext.RequestID)
}

// externalSigner resolves an invitation token to its signer row and the request it
// belongs to. It is the single door into this flow: nothing here takes an org or
// request id from the caller.
func (s *Service) externalSigner(ctx context.Context, token string) (externalSigner, Request, error) {
	ext, err := s.store.SignerByToken(ctx, token)
	if err != nil {
		return externalSigner{}, Request{}, err
	}
	req, err := s.store.GetRequest(ctx, ext.OrgID, ext.RequestID)
	if err != nil {
		return externalSigner{}, Request{}, err
	}
	return ext, req, nil
}

// ExternalSignPath is the SPA route an external signee's invitation link points at.
// It is exported so the notification adapter in cmd/api builds exactly the link the
// ceremony redirects back to (resultURL), rather than a second spelling of it.
func ExternalSignPath(token string) string {
	return "/sign/" + url.PathEscape(token)
}
