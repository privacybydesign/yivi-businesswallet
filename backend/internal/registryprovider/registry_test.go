package registryprovider

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// recordedDecision captures what the registry audited for one consult.
type recordedDecision struct {
	action string
	target audit.Target
	meta   map[string]any
}

// fakeRecorder captures audit records instead of writing them.
type fakeRecorder struct{ records []recordedDecision }

func (r *fakeRecorder) Record(_ context.Context, _ database.Querier, action string, target audit.Target, meta map[string]any) error {
	r.records = append(r.records, recordedDecision{action: action, target: target, meta: meta})
	return nil
}

// fakeDB resolves the register org id lookup to a fixed row; the registry uses no
// other database surface during a consult.
type fakeDB struct{}

func (fakeDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (fakeDB) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (fakeDB) QueryRow(context.Context, string, ...any) pgx.Row        { return fixedIDRow{} }
func (fakeDB) Begin(context.Context) (pgx.Tx, error)                   { return nil, nil }

type fixedIDRow struct{}

func (fixedIDRow) Scan(dest ...any) error {
	if len(dest) == 1 {
		if p, ok := dest[0].(*uuid.UUID); ok {
			*p = uuid.UUID{1}
		}
	}
	return nil
}

func newTestRegistry() (*SeededRegistry, *fakeRecorder) {
	rec := &fakeRecorder{}
	return NewSeededRegistry(fakeDB{}, rec), rec
}

// after unwraps the {after} envelope the registry records its decision in.
func (d recordedDecision) after() map[string]any {
	a, _ := d.meta["after"].(map[string]any)
	return a
}

func TestConsultValidatesMatchingRepresentative(t *testing.T) {
	reg, rec := newTestRegistry()

	att, err := reg.Consult(context.Background(), ConsultRequest{
		KVKNumber:   "90000010",
		GivenNames:  "Johannes Hendrik",
		FamilyName:  "Janssen",
		DateOfBirth: "1979-05-14",
	})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if !att.RequesterIsRepresentative {
		t.Fatal("expected requester to be validated as a representative")
	}
	if att.LegalName != "Yivi B.V." {
		t.Fatalf("legal name = %q, want Yivi B.V.", att.LegalName)
	}
	rep := att.Representatives[att.RequesterRepresentativeIndex]
	if rep.FamilyName != "Janssen" || rep.Kind != KindBestuurder {
		t.Fatalf("matched representative = %+v, want Janssen/bestuurder", rep)
	}
	if len(rec.records) != 1 || rec.records[0].action != audit.KVKRegistrationValidated {
		t.Fatalf("audit = %+v, want one KVKRegistrationValidated", rec.records)
	}
	if got := rec.records[0].after()["outcome"]; got != "validated" {
		t.Fatalf("audited outcome = %v, want validated", got)
	}
}

func TestConsultMatchesAccentAndCaseInsensitively(t *testing.T) {
	reg, _ := newTestRegistry()

	att, err := reg.Consult(context.Background(), ConsultRequest{
		KVKNumber:   "90000030",
		GivenNames:  "ANKE",
		FamilyName:  "bakker",
		DateOfBirth: "1990-02-17",
	})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if !att.RequesterIsRepresentative {
		t.Fatal("expected case-insensitive name match to validate")
	}
}

func TestConsultBouncesNonMatchingIdentity(t *testing.T) {
	reg, rec := newTestRegistry()

	att, err := reg.Consult(context.Background(), ConsultRequest{
		KVKNumber:  "90000010",
		GivenNames: "Mallory",
		FamilyName: "Impostor",
	})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if att.RequesterIsRepresentative {
		t.Fatal("expected a non-matching identity to be bounced")
	}
	if len(rec.records) != 1 || rec.records[0].action != audit.KVKRegistrationNotValidated {
		t.Fatalf("audit = %+v, want one KVKRegistrationNotValidated", rec.records)
	}
	if got := rec.records[0].after()["reason"]; got != reasonNotARepresentative {
		t.Fatalf("audited reason = %v, want %q", got, reasonNotARepresentative)
	}
}

func TestConsultBouncesWrongDateOfBirth(t *testing.T) {
	reg, _ := newTestRegistry()

	att, err := reg.Consult(context.Background(), ConsultRequest{
		KVKNumber:   "90000010",
		GivenNames:  "Johannes Hendrik",
		FamilyName:  "Janssen",
		DateOfBirth: "2000-01-01",
	})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if att.RequesterIsRepresentative {
		t.Fatal("expected a matching name but wrong date of birth to be bounced")
	}
}

func TestConsultRejectsUnknownKVK(t *testing.T) {
	reg, rec := newTestRegistry()

	_, err := reg.Consult(context.Background(), ConsultRequest{
		KVKNumber:  "00000001",
		GivenNames: "Johannes Hendrik",
		FamilyName: "Janssen",
	})
	if !errors.Is(err, ErrUnknownKVK) {
		t.Fatalf("err = %v, want ErrUnknownKVK", err)
	}
	if len(rec.records) != 1 || rec.records[0].action != audit.KVKRegistrationNotValidated {
		t.Fatalf("audit = %+v, want one KVKRegistrationNotValidated", rec.records)
	}
	if got := rec.records[0].after()["reason"]; got != reasonUnknownKVK {
		t.Fatalf("audited reason = %v, want %q", got, reasonUnknownKVK)
	}
}
