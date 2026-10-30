package proofing

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/idscan"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

var (
	// ErrDocumentScanNeedsPage is a flow that photographs the document sent
	// where its subject gets no page to scan it on: by mail, or as a bare deep
	// link.
	ErrDocumentScanNeedsPage = errors.New("proofing: a flow that photographs the document runs on a page: show it on screen or send a hosted link")
	// ErrNoDocumentScanner is a flow that photographs the document on a
	// deployment without the document scan service (ID_SCAN_KEY_ID unset).
	ErrNoDocumentScanner = errors.New("proofing: no document scan service configured")
	// ErrDocumentScanNotAsked is a scan on a request whose flow takes no
	// document photo.
	ErrDocumentScanNotAsked = errors.New("proofing: the request takes no document photo")
	// ErrDocumentScanDone is a scan started once the photos are in.
	ErrDocumentScanDone = errors.New("proofing: the document photo is in already")
	// ErrDocumentScanMismatch is a finished scan that is not the one the
	// request's page started last.
	ErrDocumentScanMismatch = errors.New("proofing: the document scan is not this request's")
	// ErrDocumentScanFailed is a scan the person did not finish, or that gave
	// no photo: the page offers another.
	ErrDocumentScanFailed = errors.New("proofing: the document scan did not finish")
	// ErrDocumentScanPending is a Yivi disclosure before the document photo is
	// in.
	ErrDocumentScanPending = errors.New("proofing: the document photo is not in yet")
)

// documentScanner is the document scan service the browser takes a document
// photo through (implemented by *idscan.Client).
type documentScanner interface {
	CreateTransaction(ctx context.Context, locale string, opts idscan.ScanOptions) (idscan.Transaction, error)
	Result(ctx context.Context, transactionID, resultID string) (idscan.Result, error)
}

// SetDocumentScanner gives the service its document scan service.
func (s *Service) SetDocumentScanner(scanner documentScanner) {
	s.documentScanner = scanner
}

// documentScanLanguageDutch is the page language the scan service speaks
// Dutch for; any other gets its English.
const documentScanLanguageDutch = "nl"

// documentScanLocale is the scan service's locale for a page in language.
func documentScanLocale(language string) string {
	if strings.HasPrefix(strings.ToLower(language), documentScanLanguageDutch) {
		return idscan.LocaleDutch
	}
	return idscan.LocaleEnglish
}

// StartDocumentScan starts (or, after a failed one, restarts) the document
// photo of an on-screen request in the scan service's page, in language,
// which the wallet's page embeds. It answers the page's URL; the transaction
// stays on the request, server-side.
func (s *Service) StartDocumentScan(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID, language string) (string, error) {
	req, err := s.sentRequest(ctx, orgID, id, requestedBy)
	if err != nil {
		return "", err
	}
	return s.startDocumentScan(ctx, req, language)
}

// HostedStartDocumentScan is StartDocumentScan for a hosted request.
func (s *Service) HostedStartDocumentScan(ctx context.Context, token, language string) (string, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return "", err
	}
	return s.startDocumentScan(ctx, req, language)
}

func (s *Service) startDocumentScan(ctx context.Context, req Request, language string) (string, error) {
	sess, err := s.documentScanSession(req)
	if err != nil {
		return "", err
	}
	if req.DocumentScannedAt != nil {
		return "", ErrDocumentScanDone
	}
	f, err := s.sessionFlow(ctx, req)
	if err != nil {
		return "", err
	}
	trx, err := s.documentScanner.CreateTransaction(ctx, documentScanLocale(language), documentScanOptions(f))
	if err != nil {
		return "", fmt.Errorf("proofing: start document scan request %s: %w", req.ID, err)
	}
	if err := s.requests.SetDocumentScanTransaction(ctx, req, sess.ID, trx.ID); err != nil {
		return "", err
	}
	return trx.URL, nil
}

// FinishDocumentScan hands the photos of the scan the page started last to
// the engine, as the request's document_photo step: transactionID and
// resultID are what the scan service sent the person back with. A scan the
// person did not finish is ErrDocumentScanFailed. Finishing it again (a
// reload) changes nothing.
func (s *Service) FinishDocumentScan(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID, transactionID, resultID string) error {
	req, err := s.sentRequest(ctx, orgID, id, requestedBy)
	if err != nil {
		return err
	}
	return s.finishDocumentScan(ctx, req, transactionID, resultID)
}

// HostedFinishDocumentScan is FinishDocumentScan for a hosted request.
func (s *Service) HostedFinishDocumentScan(ctx context.Context, token, transactionID, resultID string) error {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return err
	}
	return s.finishDocumentScan(ctx, req, transactionID, resultID)
}

func (s *Service) finishDocumentScan(ctx context.Context, req Request, transactionID, resultID string) error {
	sess, err := s.documentScanSession(req)
	if err != nil {
		return err
	}
	if req.documentScanTransactionID == "" || transactionID != req.documentScanTransactionID {
		return ErrDocumentScanMismatch
	}
	if req.DocumentScannedAt != nil {
		return nil
	}
	if resultID == "" {
		return ErrDocumentScanFailed
	}
	res, err := s.documentScanner.Result(ctx, transactionID, resultID)
	if errors.Is(err, idscan.ErrNoDocumentImage) {
		return fmt.Errorf("%w: %w", ErrDocumentScanFailed, err)
	}
	if err != nil {
		return fmt.Errorf("proofing: document scan result request %s: %w", req.ID, err)
	}
	if !res.Completed {
		return ErrDocumentScanFailed
	}
	f, err := s.sessionFlow(ctx, req)
	if err != nil {
		return err
	}
	photo := proofingprovider.DocumentPhoto{
		Front: providerImage(res.Front), BSNRedacted: redactsDocumentBSN(f), Check: documentCheck(res, f),
	}
	if res.Back != nil {
		back := providerImage(*res.Back)
		photo.Back = &back
	}
	if err := s.ips.SubmitDocumentPhoto(ctx, requestTenant(req), sess.ID, sess.Token, photo); err != nil {
		return documentScanStepError(err)
	}
	if err := s.requests.MarkDocumentScanned(ctx, req, sess.ID, transactionID); err != nil {
		return err
	}
	// A flow of the photo alone is decided by it.
	s.reconcile(ctx, requestTenant(req), req)
	return nil
}

// documentScanOptions are the rules of flow f the scan service enforces: the
// BSN blacked out when f says so, and f's age check.
func documentScanOptions(f proofingprovider.Flow) idscan.ScanOptions {
	opts := idscan.ScanOptions{RedactBSN: redactsDocumentBSN(f)}
	if slices.Contains(f.RequestedAttributes, attributeAgeOver) {
		opts.MinimumAge = f.AgeOver
	}
	return opts
}

// documentCheck is the scan service's result res in the engine's terms. The
// personal number goes along only for a flow that keeps the BSN.
func documentCheck(res idscan.Result, f proofingprovider.Flow) proofingprovider.DocumentCheck {
	doc := res.Document
	scanned := proofingprovider.ScannedDocument{
		Kind: documentKinds[doc.Type], IssuingState: doc.IssuingState, Nationality: doc.Nationality, Number: doc.Number,
		Surname: doc.Surname, GivenNames: doc.GivenNames, DateOfBirth: doc.DateOfBirth, DateOfExpiry: doc.DateOfExpiry, Sex: doc.Sex,
	}
	if f.BSNPolicy == bsnPolicyRetrieve {
		scanned.PersonalNumber = doc.PersonalNumber
	}
	return proofingprovider.DocumentCheck{
		Verdict: documentVerdicts[res.Authenticity], Score: res.Score,
		FieldsValid: res.FieldsValid, ZonesMatch: res.ZonesMatch, Document: scanned,
	}
}

// documentKinds are ID Scan's document types the engine's flows accept,
// as the engine's kinds; any other type has no kind.
var documentKinds = map[string]string{
	"passport":                       proofingprovider.DocumentKindPassport,
	"identity_card":                  proofingprovider.DocumentKindIdentityCard,
	"national_identity_card":         proofingprovider.DocumentKindIdentityCard,
	"residence_permit":               proofingprovider.DocumentKindResidencePermit,
	"residence_permit_identity_card": proofingprovider.DocumentKindResidencePermit,
	"driving_license":                proofingprovider.DocumentKindDrivingLicence,
}

// documentVerdicts are ID Scan's judgements in the engine's terms.
var documentVerdicts = map[idscan.Authenticity]proofingprovider.DocumentVerdict{
	idscan.Authentic: proofingprovider.DocumentAuthentic,
	idscan.Doubtful:  proofingprovider.DocumentDoubtful,
	idscan.Fake:      proofingprovider.DocumentFake,
}

// documentScanSession is req's running engine session, if req takes a
// document photo and the deployment has the scan service.
func (s *Service) documentScanSession(req Request) (*ipsSession, error) {
	if !req.DocumentScan {
		return nil, ErrDocumentScanNotAsked
	}
	if s.documentScanner == nil {
		return nil, ErrNoDocumentScanner
	}
	sess := req.liveSession(s.now())
	if sess == nil || req.Status.Settled() {
		return nil, ErrSessionOver
	}
	return sess, nil
}

func providerImage(img idscan.Image) proofingprovider.Image {
	return proofingprovider.Image{MimeType: img.MimeType, Base64: base64.StdEncoding.EncodeToString(img.Data)}
}

// documentScanStepError reads the engine refusing the photos as the scan
// failing, and it no longer knowing the session, or answering it gone or
// decided, as the session being over.
func documentScanStepError(err error) error {
	var rejected *proofingprovider.RejectedError
	if errors.As(err, &rejected) {
		switch rejected.Status {
		case http.StatusBadRequest:
			return fmt.Errorf("%w: %w", ErrDocumentScanFailed, err)
		case http.StatusConflict:
			return fmt.Errorf("%w: %w", ErrSessionOver, err)
		}
	}
	return yiviStepError(err)
}
