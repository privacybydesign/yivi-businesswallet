package employment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/verify"
)

// Reasons a statement is refused, stable keys the frontend translates.
const (
	// ReasonNotAStatement is a file that is no readable Verzekeringsbericht.
	ReasonNotAStatement = "not_a_statement"
	// ReasonSignature is a statement whose UWV seal does not verify: changed
	// after sealing, not sealed by UWV, or not on a trusted chain.
	ReasonSignature = "signature_invalid"
	// ReasonHolder is a statement about someone else than the person who
	// proved their identity.
	ReasonHolder = "holder_mismatch"
	// ReasonStale is a statement UWV made longer than statementMaxAge ago.
	ReasonStale = "stale"
)

// statementMaxAge is how old a statement may be: an older one says little
// about the holder's employment now.
const statementMaxAge = 30 * 24 * time.Hour

// Outcome is the verdict on one statement. Statement is set whenever the
// seal verified and the file parsed; Reason is empty for an accepted one.
type Outcome struct {
	Statement *Statement
	Reason    string
	// FailedCheck is the first signature check that failed, with
	// ReasonSignature.
	FailedCheck string
}

// Accepted reports a statement that is UWV's, unchanged, recent and the
// holder's.
func (o Outcome) Accepted() bool { return o.Reason == "" }

// Checker decides on one statement: it verifies UWV's seal, parses the
// statement and matches the printed holder against a proven identity.
type Checker struct {
	validator diploma.Validator
	parser    Parser
	now       func() time.Time
}

// NewChecker checks with validator, which must verify under trust.UWV.
func NewChecker(validator diploma.Validator, parser Parser) *Checker {
	return &Checker{validator: validator, parser: parser, now: time.Now}
}

// Ping fails when the validator cannot check seals.
func (c *Checker) Ping(ctx context.Context) error {
	return c.validator.Ping(ctx)
}

// Check decides on pdf for holder. An error is the checker's own failure (no
// trust anchors, PDFium unavailable), never the document's.
//
// The seal is checked first, on the raw bytes, so only a file UWV sealed
// reaches PDFium, as diploma.Checker does.
func (c *Checker) Check(ctx context.Context, pdf []byte, holder diploma.Person) (Outcome, error) {
	verification, err := c.validator.Validate(ctx, pdf)
	if err != nil {
		return Outcome{}, fmt.Errorf("employment: validate: %w", err)
	}
	if !verification.Valid {
		key := verification.Key()
		if key == verify.CheckSignaturePresent {
			return Outcome{Reason: ReasonNotAStatement}, nil
		}
		return Outcome{Reason: ReasonSignature, FailedCheck: key}, nil
	}

	st, err := c.parser.Parse(ctx, pdf)
	if errors.Is(err, ErrNotAStatement) {
		return Outcome{Reason: ReasonNotAStatement}, nil
	}
	if err != nil {
		return Outcome{}, err
	}

	out := Outcome{Statement: st}
	switch {
	case !diploma.MatchInitials(st.Initials, st.Surname, st.DateOfBirth.Format(time.DateOnly), holder).Matched:
		out.Reason = ReasonHolder
	case c.now().Sub(st.Date) > statementMaxAge:
		out.Reason = ReasonStale
	}
	return out, nil
}
