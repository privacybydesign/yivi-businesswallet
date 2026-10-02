package diploma

import (
	"context"
	"errors"
	"fmt"
	"time"
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

// Outcome is the verdict on one extract. Document is set whenever the file
// parsed; Reason is empty for an accepted extract.
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
func (c *Checker) Check(ctx context.Context, pdf []byte, holder Person) (Outcome, error) {
	doc, err := c.parser.Parse(pdf)
	if errors.Is(err, ErrNotADiploma) {
		return Outcome{Reason: ReasonNotADiploma}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	verification, err := c.validator.Validate(ctx, pdf)
	if err != nil {
		return Outcome{}, fmt.Errorf("diploma: validate: %w", err)
	}
	out := Outcome{Document: doc, SignedAt: verification.SigningTime}
	if !verification.Valid {
		out.Reason, out.FailedCheck = ReasonSignature, verification.Key()
		return out, nil
	}
	if !MatchFullName(doc.FullName, doc.DateOfBirth.Format(time.DateOnly), holder).Matched {
		out.Reason = ReasonHolder
	}
	return out, nil
}
