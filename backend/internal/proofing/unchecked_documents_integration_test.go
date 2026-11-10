//go:build integration

package proofing

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// A file whose seal could not be checked is kept sealed with the identity it
// must name and opens its step's review. Its PDF goes once checked or once
// that review is decided, its name and outcome at the request's purge.
func TestUncheckedDocumentsKeptUntilDecided(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	recorder := audit.NewDBRecorder()
	cipher := newTestCipher(t)
	requests := NewRequestStore(pool, recorder, cipher)
	unchecked := NewUncheckedDocumentStore(pool, recorder, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	rita := makeUser(t, pool, "rita@example.org")
	in := newStoredRequest(orgID, sam, externalSubject("anna@example.org"))
	in.Diplomas, in.Employment = DiplomasRequired, EmploymentRequired
	req := createStarted(t, requests, in, "s1")
	if err := requests.RecordOutcome(ctx, req, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	holder := diploma.Person{GivenNames: "Anna", Surname: "Jansen", DateOfBirth: "1990-01-02"}
	extract := DiplomaFile{Name: "diploma.pdf", Bytes: []byte("%PDF diploma")}

	addUnchecked(t, unchecked, req, holder, extract)
	got := assertUncheckedKept(t, pool, requests, unchecked, req, holder, extract)

	if at, err := requests.DecideStepReview(ctx, got, ReviewKindDiplomas, rita, ReviewRejected, "seal never checked"); err != nil || at == nil {
		t.Fatalf("DecideStepReview = %v, %v", at, err)
	}
	got = resolveStatements(t, pool, requests, unchecked, req)

	if err := requests.Purge(ctx, got, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if named := countRows(t, pool, `SELECT count(*) FROM audit_events WHERE target_id = $1
		AND metadata->'after' ? 'fileName'`, req.ID.String()); named != 0 {
		t.Errorf("events still naming a file after purge = %d; want none", named)
	}
	if left := countRows(t, pool, `SELECT count(*) FROM identity_proofing_request_unchecked_documents
		WHERE request_id = $1`, req.ID); left != 0 {
		t.Errorf("after purge %d files left; want none", left)
	}
}

// addUnchecked keeps the extract twice (once) and four statements (three).
func addUnchecked(t *testing.T, unchecked *UncheckedDocumentStore, req Request, holder diploma.Person, extract DiplomaFile) {
	t.Helper()
	for _, tc := range []struct {
		kind ReviewKind
		file DiplomaFile
		want bool
	}{
		{ReviewKindDiplomas, extract, true},
		{ReviewKindDiplomas, extract, false},
		{ReviewKindEmployment, DiplomaFile{Name: "uwv.pdf", Bytes: []byte("%PDF uwv")}, true},
		{ReviewKindEmployment, DiplomaFile{Name: "uwv2.pdf", Bytes: []byte("%PDF uwv 2")}, true},
		{ReviewKindEmployment, DiplomaFile{Name: "uwv3.pdf", Bytes: []byte("%PDF uwv 3")}, true},
		{ReviewKindEmployment, DiplomaFile{Name: "uwv4.pdf", Bytes: []byte("%PDF uwv 4")}, false},
	} {
		if added, err := unchecked.Add(context.Background(), req, tc.kind, holder, tc.file); err != nil || added != tc.want {
			t.Fatalf("Add %s %s = %v, %v; want %v", tc.kind, tc.file.Name, added, err, tc.want)
		}
	}
}

func assertUncheckedKept(t *testing.T, pool *pgxpool.Pool, requests *RequestStore, unchecked *UncheckedDocumentStore,
	req Request, holder diploma.Person, extract DiplomaFile,
) Request {
	t.Helper()
	ctx := context.Background()
	got, err := requests.Get(ctx, req.OrganizationID, req.ID)
	if err != nil || got.UncheckedDiplomas != 1 || !got.UncheckedEmployment ||
		got.DiplomasReview != ReviewPending || got.EmploymentReview != ReviewPending {
		t.Fatalf("Get = %d diplomas, statement %v, reviews %q %q, %v; want one of each, both pending",
			got.UncheckedDiplomas, got.UncheckedEmployment, got.DiplomasReview, got.EmploymentReview, err)
	}
	docs, err := unchecked.List(ctx, req, ReviewKindDiplomas)
	if err != nil || len(docs) != 1 || string(docs[0].Bytes) != string(extract.Bytes) || docs[0].Holder != holder {
		t.Fatalf("List = %+v, %v; want the extract with its holder", docs, err)
	}
	if named := countRows(t, pool, `SELECT count(*) FROM audit_events WHERE action = $1
		AND metadata->'after'->>'fileName' = 'diploma.pdf' AND metadata->'after'->>'sealUnchecked' = 'true'`,
		audit.IdentityProofingDocumentUnchecked); named != 1 {
		t.Errorf("events naming the diploma = %d; want 1", named)
	}
	if audited := countRows(t, pool, `SELECT count(*) FROM audit_events WHERE action = $1`,
		audit.IdentityProofingDocumentUnchecked); audited != 4 {
		t.Errorf("audited = %d; want 4", audited)
	}
	return got
}

// resolveStatements checks the diplomas review left only the statements
// waiting, refuses each, and returns the request with no PDF kept.
func resolveStatements(t *testing.T, pool *pgxpool.Pool, requests *RequestStore, unchecked *UncheckedDocumentStore, req Request) Request {
	t.Helper()
	ctx := context.Background()
	got, err := requests.Get(ctx, req.OrganizationID, req.ID)
	if err != nil || got.UncheckedDiplomas != 0 || !got.UncheckedEmployment {
		t.Fatalf("after deciding diplomas: %d diplomas, statement %v, %v; want only the statement left",
			got.UncheckedDiplomas, got.UncheckedEmployment, err)
	}
	if len(got.SealChecks) != 4 || got.SealChecks[0].Outcome != SealCheckNotChecked || got.SealChecks[1].Outcome != "" {
		t.Fatalf("seal checks = %+v; want the diploma not checked, the statements waiting", got.SealChecks)
	}
	statements, err := unchecked.List(ctx, got, ReviewKindEmployment)
	if err != nil || len(statements) != MaxUncheckedStatements {
		t.Fatalf("List statements = %d, %v; want %d", len(statements), err, MaxUncheckedStatements)
	}
	for _, st := range statements {
		if err := unchecked.Resolve(ctx, got, st.ID, "signature_invalid"); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}
	got, err = requests.Get(ctx, req.OrganizationID, req.ID)
	if err != nil || got.UncheckedEmployment || got.SealChecks[1].Outcome != "signature_invalid" || got.SealChecks[1].CheckedAt == nil {
		t.Fatalf("after resolving: statement waits %v, %+v, %v; want it refused", got.UncheckedEmployment, got.SealChecks, err)
	}
	if sealed := countRows(t, pool, `SELECT count(*) FROM identity_proofing_request_unchecked_documents
		WHERE request_id = $1 AND ciphertext IS NOT NULL`, req.ID); sealed != 0 {
		t.Errorf("PDFs still kept = %d; want none", sealed)
	}
	return got
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
