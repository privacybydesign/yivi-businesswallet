package employment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/verify"
)

type fakeValidator struct {
	verification *diploma.Verification
	err          error
}

func (f fakeValidator) Ping(context.Context) error { return f.err }

func (f fakeValidator) Validate(context.Context, []byte) (*diploma.Verification, error) {
	return f.verification, f.err
}

// sealed is a verification that passed.
var sealed = &diploma.Verification{Valid: true, Checks: []verify.Check{}}

type fakeParser struct {
	statement *Statement
	err       error
}

func (f fakeParser) Parse(context.Context, []byte) (*Statement, error) { return f.statement, f.err }

func TestCheck(t *testing.T) {
	holder := diploma.Person{GivenNames: "Johanna Theodora", Surname: "DE VRIES", DateOfBirth: "1980-02-03"}
	statement := &Statement{
		Initials: []string{"J", "Th"}, Surname: "Jansen-de Vries",
		DateOfBirth: date("03-02-1980"), Date: date("15-10-2026"),
	}
	someoneElse := *statement
	someoneElse.Surname = "Jansen"
	now := date("15-10-2026").Add(statementMaxAge)

	cases := []struct {
		name       string
		validator  fakeValidator
		parser     fakeParser
		now        time.Time
		wantReason string
		wantFailed string
	}{
		{name: "accepted", validator: fakeValidator{verification: sealed}, parser: fakeParser{statement: statement}, now: now},
		{
			name: "unsigned", validator: fakeValidator{verification: &diploma.Verification{
				Checks: []verify.Check{{ID: verify.CheckSignaturePresent}},
			}},
			now: now, wantReason: ReasonNotAStatement,
		},
		{
			name: "seal does not verify", validator: fakeValidator{verification: &diploma.Verification{
				Checks: []verify.Check{{ID: verify.CheckSingleSignature, OK: true}, {ID: verify.CheckSignerIdentity}},
			}},
			now: now, wantReason: ReasonSignature, wantFailed: verify.CheckSignerIdentity,
		},
		{
			name: "not a statement", validator: fakeValidator{verification: sealed},
			parser: fakeParser{err: ErrNotAStatement}, now: now, wantReason: ReasonNotAStatement,
		},
		{
			name: "someone else", validator: fakeValidator{verification: sealed},
			parser: fakeParser{statement: &someoneElse}, now: now, wantReason: ReasonHolder,
		},
		{
			name: "stale", validator: fakeValidator{verification: sealed},
			parser: fakeParser{statement: statement}, now: now.Add(time.Second), wantReason: ReasonStale,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewChecker(tc.validator, tc.parser)
			c.now = func() time.Time { return tc.now }
			out, err := c.Check(context.Background(), []byte("%PDF-"), holder)
			if err != nil {
				t.Fatal(err)
			}
			if out.Reason != tc.wantReason || out.FailedCheck != tc.wantFailed {
				t.Errorf("Outcome = %q/%q, want %q/%q", out.Reason, out.FailedCheck, tc.wantReason, tc.wantFailed)
			}
			if out.Accepted() && out.Statement != statement {
				t.Errorf("an accepted outcome carries %+v", out.Statement)
			}
		})
	}
}

func TestCheckFailure(t *testing.T) {
	c := NewChecker(fakeValidator{err: diploma.ErrTrustUnavailable}, fakeParser{})
	if _, err := c.Check(context.Background(), nil, diploma.Person{}); !errors.Is(err, diploma.ErrTrustUnavailable) {
		t.Errorf("err = %v, want ErrTrustUnavailable", err)
	}
}
