package diploma

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/verify"
)

type fakeParser struct {
	doc *Document
	err error
}

func (f fakeParser) Parse([]byte) (*Document, error) { return f.doc, f.err }

type fakeValidator struct{ v *Verification }

func (fakeValidator) Ping(context.Context) error { return nil }

func (f fakeValidator) Validate(context.Context, []byte) (*Verification, error) { return f.v, nil }

func TestCheck(t *testing.T) {
	extract := &Document{FullName: "Anna Maria van der Berg", DateOfBirth: time.Date(1980, 2, 3, 0, 0, 0, 0, time.UTC)}
	holder := Person{GivenNames: "ANNA", Surname: "VAN DER BERG", DateOfBirth: "1980-02-03"}
	broken := &Verification{Checks: []verify.Check{{ID: verify.CheckSignaturePresent, OK: true}, {ID: verify.CheckCoversWholeFile}}}
	cases := []struct {
		name       string
		parser     fakeParser
		validator  Validator
		holder     Person
		wantReason string
		wantCheck  string
	}{
		{"accepted", fakeParser{doc: extract}, StubValidator{}, holder, "", ""},
		{"not an extract", fakeParser{err: ErrNotADiploma}, StubValidator{}, holder, ReasonNotADiploma, ""},
		{"changed after signing", fakeParser{doc: extract}, fakeValidator{broken}, holder, ReasonSignature, verify.CheckCoversWholeFile},
		{"someone else's", fakeParser{doc: extract}, StubValidator{}, Person{GivenNames: "Piet", Surname: "Berg", DateOfBirth: "1980-02-03"}, ReasonHolder, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := NewChecker(tc.validator, tc.parser).Check(context.Background(), nil, tc.holder)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if out.Reason != tc.wantReason || out.FailedCheck != tc.wantCheck || out.Accepted() != (tc.wantReason == "") {
				t.Errorf("Check = %+v, want reason %q check %q", out, tc.wantReason, tc.wantCheck)
			}
		})
	}
}

func TestCheckFailsOnTheCheckersOwnError(t *testing.T) {
	broken := errors.New("pdfium unavailable")
	if _, err := NewChecker(StubValidator{}, fakeParser{err: broken}).Check(context.Background(), nil, Person{}); !errors.Is(err, broken) {
		t.Errorf("Check = %v, want the parser's error", err)
	}
}
