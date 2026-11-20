package registryprovider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/euid"
)

func newDeclaredRegistry() (*DeclaredRegistry, *fakeRecorder) {
	rec := &fakeRecorder{}
	return NewDeclaredRegistry(fakeDB{}, rec), rec
}

func declaredRequest() ConsultRequest {
	return ConsultRequest{
		KVKNumber: "12345678", LegalName: "  Acme B.V. ",
		GivenNames: "Alice", FamilyName: "Owner", DateOfBirth: "1980-01-02",
	}
}

func TestDeclaredRegistryAcceptsAStatedCompany(t *testing.T) {
	reg, rec := newDeclaredRegistry()
	att, err := reg.Consult(context.Background(), declaredRequest())
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if att.LegalName != "Acme B.V." || att.KVKNumber != "12345678" || !att.Declared {
		t.Fatalf("attestation = %+v, want the stated company, marked declared", att)
	}
	if _, err := euid.Parse(att.EUID); err != nil {
		t.Fatalf("EUID %q does not parse: %v", att.EUID, err)
	}
	if !att.RequesterIsRepresentative || len(att.Representatives) != 1 {
		t.Fatalf("representatives = %+v, want only the requester", att.Representatives)
	}
	// Nobody confirmed the requester is a director.
	if rep := att.Representatives[0]; rep.Kind != KindOverig || rep.FamilyName != "Owner" || rep.DateOfBirth != "1980-01-02" {
		t.Fatalf("representative = %+v, want the requester as overig", rep)
	}
	if len(rec.records) != 1 || rec.records[0].action != audit.KVKRegistrationDeclared {
		t.Fatalf("audit = %+v, want one declared decision", rec.records)
	}
	if got := rec.records[0].after()["outcome"]; got != outcomeDeclared {
		t.Fatalf("audited outcome = %v, want %q", got, outcomeDeclared)
	}
}

func TestDeclaredRegistryRefusesAMalformedKVKNumber(t *testing.T) {
	for _, number := range []string{"", "1234567", "123456789", "1234567a", "12 34 56 78"} {
		reg, rec := newDeclaredRegistry()
		req := declaredRequest()
		req.KVKNumber = number
		if _, err := reg.Consult(context.Background(), req); !errors.Is(err, ErrUnknownKVK) {
			t.Fatalf("KVK number %q: err = %v, want ErrUnknownKVK", number, err)
		}
		if len(rec.records) != 1 || rec.records[0].action != audit.KVKRegistrationNotValidated {
			t.Fatalf("KVK number %q: audit = %+v, want one not-validated decision", number, rec.records)
		}
	}
}

func TestDeclaredRegistryNeedsACompanyName(t *testing.T) {
	for _, name := range []string{"", "   ", strings.Repeat("a", maxLegalNameLength+1)} {
		reg, rec := newDeclaredRegistry()
		req := declaredRequest()
		req.LegalName = name
		if _, err := reg.Consult(context.Background(), req); !errors.Is(err, ErrLegalNameRequired) {
			t.Fatalf("name of %d characters: err = %v, want ErrLegalNameRequired", len(name), err)
		}
		if len(rec.records) != 0 {
			t.Fatalf("audit = %+v, want none for a request without a usable name", rec.records)
		}
	}
}
