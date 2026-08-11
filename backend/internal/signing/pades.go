package signing

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"strings"
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

// openPDF parses input as a PDF. digitorus/pdf reports a malformed xref/trailer by
// panicking and never recovers, so a bad upload must not escape as a panic on the
// request goroutine: it is ErrInvalidPDF like any other unusable file.
func openPDF(input []byte) (r *pdf.Reader, err error) {
	defer func() {
		if recover() != nil {
			r, err = nil, ErrInvalidPDF
		}
	}()
	rdr, err := pdf.NewReader(bytes.NewReader(input), int64(len(input)))
	if err != nil {
		return nil, ErrInvalidPDF
	}
	return rdr, nil
}

// startPAdES begins a PAdES signing pass over input using cred's certificate, and
// returns the session plus the digest that must be signed (over which the OAuth
// authorize step is bound). The signing certificate must already be known, since
// the CMS SignedAttributes (and thus the digest) include a hash of the cert.
//
// placements are this signer's visible marks. Their signature block becomes the
// visible appearance of the signature itself; their paraphs are stamped into a
// revision appended first, so the signature covers them (see stamp.go). A signer
// with no placements gets an invisible signature, which is what every signature was
// before placements existed.
func startPAdES(input []byte, cred signingprovider.Credential, placements []Placement) (*padesSession, []byte, error) {
	signer := &padesSigner{
		pub:      cred.Certificate.PublicKey,
		digestCh: make(chan []byte, 1),
		sigCh:    make(chan []byte, 1),
		errCh:    make(chan error, 1),
	}
	sess := &padesSession{signer: signer, result: make(chan padesResult, 1)}

	name := cred.Certificate.Subject.CommonName
	// One timestamp for both the visible "at date" line and the signature's own
	// signing time, so the stamp drawn before this pass and the /M it is covered by
	// name the same moment.
	signedAt := time.Now()
	input, err := stampSignerMarks(input, placements, name, signedAt)
	if err != nil {
		return nil, nil, err
	}
	size := int64(len(input))
	rdr, err := openPDF(input)
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
					Name:   name,
					Reason: "Qualified electronic signature",
					Date:   signedAt,
				},
			},
			// The signature itself is invisible; its visible mark is the multi-line block
			// stampSignerMarks drew above, inside this signer's ByteRange. pdfsign rewrites
			// a page only for a VISIBLE appearance (sign/sign.go), and that rewrite is what
			// forced the delicate page normalisation this used to need — an invisible
			// signature never touches a page, so our own robust rewrite carries the block.
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

// stampSignerMarks prepares the document for one signer's pass: it draws their
// paraphs and their signature block into it as locked stamp annotations, in one
// incremental revision appended BEFORE the signing pass — so this signer's own
// signature covers them and they cannot be moved or altered without breaking it. It
// returns the input unchanged when the signer placed nothing. digitorus/pdf panics
// its way out of a malformed object graph, and this runs on the request goroutine,
// so the read is recovered into ErrInvalidPDF rather than taking the process down.
func stampSignerMarks(input []byte, placements []Placement, name string, signedAt time.Time) (out []byte, err error) {
	paraphs := paraphPlacements(placements)
	block := signaturePlacement(placements)
	if len(paraphs) == 0 && block == nil {
		return input, nil
	}
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("%w: %v", ErrInvalidPDF, r)
		}
	}()
	rdr, err := openPDF(input)
	if err != nil {
		return nil, ErrInvalidPDF
	}
	return stampMarks(input, rdr, paraphs, paraphText(name), block, signatureLines(name, signedAt))
}

// signatureLines is the text drawn inside a signer's signature block, one string
// per line. The name is reordered to "Surname, given names" and the moment is the
// same one the signature dictionary records, in the server's local time zone.
func signatureLines(name string, signedAt time.Time) []string {
	return []string{
		"Electronically signed by:",
		reorderName(name),
		"at date: " + signedAt.Format(signatureDateLayout),
	}
}

// signatureDateLayout renders the signing time as e.g. "2026-08-11 14:32 CET": a
// stated zone rather than a bare local time, so the mark is unambiguous wherever it
// is read. MST in a Go layout prints the zone abbreviation of the time's location.
const signatureDateLayout = "2006-01-02 15:04 MST"

// reorderName turns the certificate's common name ("Dibran Mulder") into
// "Mulder, Dibran": the last whitespace-separated field is taken as the surname and
// the rest as the given names. It is a heuristic — a multi-word surname like "van
// der Berg" splits on its last word — so a name with fewer than two fields is left
// exactly as it stands rather than guessed at.
func reorderName(name string) string {
	fields := strings.Fields(name)
	if len(fields) < 2 {
		return strings.TrimSpace(name)
	}
	surname := fields[len(fields)-1]
	given := strings.Join(fields[:len(fields)-1], " ")
	return surname + ", " + given
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
