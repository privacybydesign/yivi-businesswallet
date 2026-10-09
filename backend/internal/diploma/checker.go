package diploma

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/verify"
)

// Reasons an extract is refused, stable keys the frontend translates.
const (
	// ReasonNotADiploma is a file that is no readable DUO diploma extract.
	ReasonNotADiploma = "not_a_diploma"
	// ReasonSignature is an extract whose DUO signature does not verify:
	// changed after signing, not signed by DUO, or not on a trusted chain.
	ReasonSignature = "signature_invalid"
	// ReasonHolder is an extract that names someone else than the person who
	// proved their identity.
	ReasonHolder = "holder_mismatch"
)

// Outcome is the verdict on one extract. Document is set whenever the
// signature verified and the file parsed; Reason is empty for an accepted
// extract.
type Outcome struct {
	Document *Document
	// SignedAt is when DUO signed it (the trusted timestamp when there is one).
	SignedAt time.Time
	Reason   string
	// FailedCheck is the first signature check that failed, with
	// ReasonSignature.
	FailedCheck string
}

// Accepted reports an extract that is DUO's, unchanged, and the holder's.
func (o Outcome) Accepted() bool { return o.Reason == "" }

// Checker decides on one extract: it parses it, verifies DUO's signature and
// matches the printed holder against a proven identity.
type Checker struct {
	validator Validator
	parser    Parser
}

func NewChecker(validator Validator, parser Parser) *Checker {
	return &Checker{validator: validator, parser: parser}
}

// Ping fails when the validator cannot check signatures.
func (c *Checker) Ping(ctx context.Context) error {
	return c.validator.Ping(ctx)
}

// Check decides on pdf for holder. An error is the checker's own failure (no
// trust anchors, PDFium unavailable), never the document's.
//
// The signature is checked first, on the raw bytes, so only a file DUO signed
// reaches PDFium: the upload is public, and a crafted PDF is otherwise parsed
// by a full PDF engine before anything vouches for it. A file without any
// signature is no extract, so it reads as not a diploma, as before.
func (c *Checker) Check(ctx context.Context, pdf []byte, holder Person) (Outcome, error) {
	verification, err := c.validator.Validate(ctx, pdf)
	if err != nil {
		return Outcome{}, fmt.Errorf("diploma: validate: %w", err)
	}
	if !verification.Valid {
		key := verification.Key()
		if key == verify.CheckSignaturePresent {
			return Outcome{Reason: ReasonNotADiploma}, nil
		}
		return Outcome{SignedAt: verification.SigningTime, Reason: ReasonSignature, FailedCheck: key}, nil
	}
	doc, err := c.parser.Parse(ctx, pdf)
	if errors.Is(err, ErrNotADiploma) {
		return Outcome{Reason: ReasonNotADiploma}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Document: doc, SignedAt: verification.SigningTime}
	if !MatchFullName(doc.FullName, doc.DateOfBirth.Format(time.DateOnly), holder).Matched {
		out.Reason = ReasonHolder
	}
	return out, nil
}
