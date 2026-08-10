package signing

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/digitorus/pdf"
	"github.com/digitorus/pdfsign/sign"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
)

// padesSession runs one PAdES signing as a single, parked pdfsign pass. Because
// the actual signature is produced remotely by the QTSP only AFTER an interactive
// wallet ceremony, we cannot supply a synchronous local key: instead pdfsign runs
// in a goroutine whose crypto.Signer publishes the digest-to-sign and then blocks
// until the signature arrives, at which point the one pdfsign pass completes and
// embeds it. One pass means pkcs7 stamps the CMS signingTime exactly once, so no
// deterministic-time patch is needed.
//
// The session lives in memory for the duration of the ceremony (bounded by
// SessionTTL). It therefore does not survive a backend restart and assumes a
// single API instance — an accepted limitation of this demo capability.
type padesSession struct {
	signer *padesSigner
	result chan padesResult // buffered(1): the goroutine never blocks delivering it
}

type padesResult struct {
	pdf []byte
	err error
}

// padesSigner is the crypto.Signer pdfsign calls once. Sign publishes the digest
// (so the caller can bind it into the OAuth authorize step) and blocks until the
// externally-produced signature is delivered or the session is abandoned.
type padesSigner struct {
	pub      crypto.PublicKey
	digestCh chan []byte // buffered(1): the digest to sign, published once
	sigCh    chan []byte // buffered(1): the QTSP signature
	errCh    chan error  // buffered(1): abandon (timeout/cancel)
	once     sync.Once
}

func (s *padesSigner) Public() crypto.PublicKey { return s.pub }

func (s *padesSigner) Sign(_ io.Reader, digest []byte, _ crypto.SignerOpts) ([]byte, error) {
	s.once.Do(func() { s.digestCh <- append([]byte(nil), digest...) })
	select {
	case sig := <-s.sigCh:
		return sig, nil
	case err := <-s.errCh:
		return nil, err
	}
}

// validatePDF reports whether input parses as a PDF the signing pass could open,
// so a create-request call can reject a bad upload before storing it. It returns
// ErrInvalidPDF on any parse failure.
func validatePDF(input []byte) error {
	if _, err := pdf.NewReader(bytes.NewReader(input), int64(len(input))); err != nil {
		return ErrInvalidPDF
	}
	return nil
}

// startPAdES begins a PAdES signing pass over input using cred's certificate, and
// returns the session plus the digest that must be signed (over which the OAuth
// authorize step is bound). The signing certificate must already be known, since
// the CMS SignedAttributes (and thus the digest) include a hash of the cert.
func startPAdES(input []byte, cred signingprovider.Credential) (*padesSession, []byte, error) {
	signer := &padesSigner{
		pub:      cred.Certificate.PublicKey,
		digestCh: make(chan []byte, 1),
		sigCh:    make(chan []byte, 1),
		errCh:    make(chan error, 1),
	}
	sess := &padesSession{signer: signer, result: make(chan padesResult, 1)}

	size := int64(len(input))
	// digitorus/pdf reports a malformed xref/trailer by panicking and never recovers,
	// so a bad upload must not escape as a panic on the request goroutine: it is
	// ErrInvalidPDF like any other unusable file.
	rdr, err := func() (r *pdf.Reader, err error) {
		defer func() {
			if recover() != nil {
				err = ErrInvalidPDF
			}
		}()
		return pdf.NewReader(bytes.NewReader(input), size)
	}()
	if err != nil {
		return nil, nil, ErrInvalidPDF
	}

	go func() {
		// digitorus/pdf reports a malformed object graph by panicking (lex.go errorf)
		// and pdfsign resolves objects lazily, so a document pdf.NewReader accepted can
		// still panic here. This is NOT the request goroutine, so an unrecovered panic
		// would take the whole process down — recover it into ErrInvalidPDF on result.
		defer func() {
			if r := recover(); r != nil {
				sess.result <- padesResult{err: fmt.Errorf("%w: %v", ErrInvalidPDF, r)}
			}
		}()
		out := &bytes.Buffer{}
		data := sign.SignData{
			Signer:            signer,
			Certificate:       cred.Certificate,
			CertificateChains: [][]*x509.Certificate{cred.Chain},
			DigestAlgorithm:   crypto.SHA256,
			Signature: sign.SignDataSignature{
				CertType:   sign.ApprovalSignature,
				DocMDPPerm: sign.DoNotAllowAnyChangesPerms,
				Info: sign.SignDataSignatureInfo{
					Name:   cred.Certificate.Subject.CommonName,
					Reason: "Qualified electronic signature",
					Date:   time.Now(),
				},
			},
		}
		if err := sign.Sign(bytes.NewReader(input), out, rdr, size, data); err != nil {
			sess.result <- padesResult{err: fmt.Errorf("signing: assemble PAdES: %w", err)}
			return
		}
		sess.result <- padesResult{pdf: out.Bytes()}
	}()

	// Normally pdfsign calls Sign (publishing the digest) partway through. But if it
	// fails BEFORE that — e.g. an encrypted or unsupported PDF that still parsed as a
	// reader — the digest never arrives; take the error off the result channel then
	// rather than blocking forever.
	select {
	case digest := <-signer.digestCh:
		return sess, digest, nil
	case r := <-sess.result:
		// pdfsign rejected the document before it could sign (no signable page, empty
		// xref, ...) or panicked assembling it. Either way that is a property of the
		// upload, not a server fault, so it must reach the caller as ErrInvalidPDF —
		// mapStartError turns anything else into a 500. The panic path already wrapped
		// ErrInvalidPDF; don't double-wrap it.
		if r.err != nil {
			if errors.Is(r.err, ErrInvalidPDF) {
				return nil, nil, r.err
			}
			return nil, nil, fmt.Errorf("%w: %v", ErrInvalidPDF, r.err)
		}
		return nil, nil, ErrInvalidPDF
	}
}

// finish delivers the externally-produced signature and returns the signed PDF.
func (s *padesSession) finish(signature []byte) ([]byte, error) {
	s.signer.sigCh <- signature
	r := <-s.result
	return r.pdf, r.err
}

// abandon unblocks a parked signing pass with err so its goroutine exits without
// leaking (result is buffered, so the discarded delivery does not block it).
func (s *padesSession) abandon(err error) {
	select {
	case s.signer.errCh <- err:
	default:
	}
}
