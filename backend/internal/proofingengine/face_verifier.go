package proofingengine

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/images"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// faceVerifier checks a live capture's liveness and, given a reference photo,
// matches it. Its one implementation is regulaFaceVerifier (the Regula Face API).
type faceVerifier interface {
	Provider() flow.FaceProvider
	// Verify never falls back to another provider. ref is nil when the flow
	// has no face match.
	Verify(ctx context.Context, sess session.Session, ref *faceReference, live liveCapture) (faceOutcome, error)
	// Release discards whatever the provider still holds for live, when it is
	// sess's own. The caller defers it, once sess is authenticated, on every
	// path, including a refused request.
	Release(ctx context.Context, sess session.Session, live liveCapture)
}

// liveCapture is what the client sent: a Regula liveness transaction id.
type liveCapture struct {
	TransactionID string
}

// faceReference is the photo the live face is matched against (base64).
type faceReference struct {
	ImageBase64 string
	MimeType    string
}

type faceOutcome struct {
	LivenessPassed bool
	LivenessScore  *float64
	MatchScore     *float64
	Matched        *bool
	Threshold      *float64
	// Selfie is the live face a provider that holds the capture itself
	// (Regula) hands back, base64 of type SelfieMime; "" when it gave none.
	Selfie     string
	SelfieMime string
}

var (
	errFaceProviderUnavailable = errors.New(errCodeFaceProviderUnavailable)
	errWrongFaceCapture        = errors.New("wrong capture for this flow's face provider")
	errUnknownTransaction      = errors.New("unknown liveness transaction")
	errForeignTransaction      = errors.New("liveness transaction does not belong to this session")
)

// faceCaptureError wraps a capture the verifier could not process (HTTP 400).
type faceCaptureError struct{ err error }

func (e faceCaptureError) Error() string { return "could not process the selfie: " + e.err.Error() }
func (e faceCaptureError) Unwrap() error { return e.err }

// faceProviderFor resolves fd's face provider: the flow's own choice, else the
// deployment default: Regula, when configured, for a native face step.
func (s *Server) faceProviderFor(fd *flow.FlowDefinition) flow.FaceProvider {
	if fd != nil && fd.FaceProvider != "" {
		return fd.FaceProvider
	}
	if s.cfg.Regula != nil && fd != nil && fd.EffectiveSelfieLocation() == flow.LocationNative {
		return flow.FaceProviderRegula
	}
	return flow.FaceProviderEngine
}

// faceVerifier returns the verifier for p; ok is false when p isn't configured.
func (s *Server) faceVerifier(p flow.FaceProvider) (faceVerifier, bool) {
	if p == flow.FaceProviderRegula {
		if s.cfg.Regula == nil {
			return nil, false
		}
		return regulaFaceVerifier{client: s.cfg.Regula, threshold: s.cfg.RegulaFaceMatchThreshold}, true
	}
	// The wallet runs no face engine of its own: only Regula verifies a face.
	return nil, false
}

// regulaFaceVerifier confirms a liveness transaction the app ran against the
// Regula Face API, checks its tag, matches it, and always deletes it.
type regulaFaceVerifier struct {
	client    RegulaClient
	threshold float64
}

func (regulaFaceVerifier) Provider() flow.FaceProvider { return flow.FaceProviderRegula }

func (v regulaFaceVerifier) Release(ctx context.Context, sess session.Session, live liveCapture) {
	if live.TransactionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), regula.DefaultTimeout)
	defer cancel()
	// Only sess's own transaction: one tagged for another session is that
	// session's to finish. One left over is the sweep's (regulasweep).
	tx, err := v.client.GetLiveness(ctx, live.TransactionID)
	if errors.Is(err, regula.ErrTransactionNotFound) {
		return
	}
	if err != nil {
		slog.WarnContext(ctx, "identity proofing: look up Regula liveness transaction to delete", slog.String("session_id", sess.ID), slog.Any("error", err))
		return
	}
	if tag := regulaTag(sess); tag == "" || tx.Tag != tag {
		return
	}
	if err := v.client.DeleteLiveness(ctx, live.TransactionID); err != nil {
		slog.WarnContext(ctx, "identity proofing: delete Regula liveness transaction", slog.Any("error", err))
	}
}

func (v regulaFaceVerifier) Verify(ctx context.Context, sess session.Session, ref *faceReference, live liveCapture) (faceOutcome, error) {
	if live.TransactionID == "" {
		return faceOutcome{}, errWrongFaceCapture
	}
	var reference string
	if ref != nil {
		raw, mime, err := decodeImageBase64(ref.ImageBase64, ref.MimeType)
		if err != nil {
			return faceOutcome{}, faceCaptureError{err}
		}
		// Regula wants a plain image; a DG2 portrait is often JPEG2000.
		reference = base64.StdEncoding.EncodeToString(raw)
		if converted, _, err := images.ToDisplayablePNG(reference, mime); err == nil && converted != "" {
			reference = converted
		}
	}

	tx, err := v.client.GetLiveness(ctx, live.TransactionID)
	if errors.Is(err, regula.ErrTransactionNotFound) {
		return faceOutcome{}, errUnknownTransaction
	}
	if err != nil {
		slog.ErrorContext(ctx, "identity proofing: Regula liveness lookup", slog.String("session_id", sess.ID), slog.Any("error", err))
		return faceOutcome{}, errFaceProviderUnavailable
	}
	if tag := regulaTag(sess); tag == "" || tx.Tag != tag {
		return faceOutcome{}, errForeignTransaction
	}
	out := faceOutcome{LivenessPassed: tx.Confirmed()}
	if out.LivenessPassed && ref != nil {
		match, err := v.client.Match(ctx, reference, live.TransactionID)
		if err != nil {
			slog.ErrorContext(ctx, "identity proofing: Regula face match", slog.String("session_id", sess.ID), slog.Any("error", err))
			return faceOutcome{}, errFaceProviderUnavailable
		}
		similarity := match.Similarity
		matched := similarity >= v.threshold
		threshold := v.threshold
		out.MatchScore, out.Matched, out.Threshold = &similarity, &matched, &threshold
		out.Selfie, out.SelfieMime = regulaSelfie(sess, match.LiveCrop)
	}
	return out, nil
}

// regulaSelfie is the live face's crop from a Regula match, kept as the
// session's selfie once the transaction is deleted; "" when Regula returned
// none or it is not an image.
func regulaSelfie(sess session.Session, crop string) (image, mime string) {
	if crop == "" {
		return "", ""
	}
	raw, err := base64.StdEncoding.DecodeString(crop)
	if err != nil {
		slog.Warn("identity proofing: Regula returned an undecodable face crop", slog.String("session_id", sess.ID), slog.Any("error", err))
		return "", ""
	}
	mime = http.DetectContentType(raw)
	if !strings.HasPrefix(mime, "image/") {
		slog.Warn("identity proofing: Regula returned a face crop that is no image", slog.String("session_id", sess.ID), slog.String("type", mime))
		return "", ""
	}
	return crop, mime
}

// regulaTag is the liveness tag that binds a Regula transaction to sess: its
// tenant reference (random, not the session id), prefixed so the wallet's
// transactions stand out on a shared Face API. Regula allows [A-Za-z0-9_-], up
// to 127 characters. The "ips-" prefix stays as it is: changing it would stop
// the sweep matching transactions already tagged.
func regulaTag(sess session.Session) string {
	if sess.TenantReference == "" {
		return ""
	}
	return "ips-" + sess.TenantReference
}

// selfieEngine is biometricsInfo.Engine for recorded selfie evidence.
func selfieEngine(ev *session.SelfieStepEvidence) string {
	if ev.Provider == faceProviderRegula {
		return faceProviderRegula
	}
	return ""
}
