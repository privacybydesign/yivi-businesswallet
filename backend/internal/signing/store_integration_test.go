//go:build integration

package signing

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// These cover what the external-signee model changed at the database boundary: a
// signer row that has no users row, a credential keyed by an address instead of a
// user, and the invitation token that is such a signee's only key. They live in the
// package (not signing_test) because the credential subject is deliberately internal
// — a caller cannot name a credential row without going through a signer.

func newStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	return NewStore(pool, audit.NopRecorder{}), pool
}

func makeOrg(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		"Acme", slug, slug+"-kvk", slug+"-euid", slug+"-address").Scan(&id)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return id
}

func makeUser(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		"INSERT INTO users (email, given_names, last_name) VALUES ($1,$2,$3) RETURNING id",
		email, "Test", "User").Scan(&id)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id
}

// testCredential is a self-signed certificate standing in for a QTSP-issued one; the
// store only ever stores and re-parses the public chain.
func testCredential(t *testing.T, id string) signingprovider.Credential {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: id},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return signingprovider.Credential{
		ID: id, Certificate: cert, Chain: []*x509.Certificate{cert}, KeyAlgo: []string{"1.2.840.10045.4.3.2"},
	}
}

const samplePDF = "%PDF-1.4 sample"

// A request with one member and one external signee round-trips: both rows persist,
// each carries its own kind, and only the external one has an address of its own.
func TestCreateRequestPersistsMixedSigners(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	alice := makeUser(t, pool, "alice@acme.example")

	id, created, err := store.CreateRequest(ctx, orgID, alice, "Contract.pdf", []byte(samplePDF), ModeSequential,
		[]SignerInput{
			{Kind: KindInternal, UserID: alice, Order: 1},
			{Kind: KindExternal, Email: "outsider@example.org", Name: "Outsider", Order: 2},
		}, RecipientInput{Channel: ChannelNone})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("CreateRequest returned %d signer rows, want 2", len(created))
	}
	for _, sg := range created {
		if sg.ID == uuid.Nil {
			t.Error("a created signer row must carry its own id, so an external signee can be addressed")
		}
	}

	req, err := store.GetRequest(ctx, orgID, id)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if len(req.Signers) != 2 {
		t.Fatalf("loaded %d signers, want 2", len(req.Signers))
	}
	member, external := req.Signers[0], req.Signers[1]
	if member.Kind != KindInternal || member.UserID == nil || *member.UserID != alice {
		t.Errorf("first signer = %+v, want the internal member", member)
	}
	if member.Email != "" {
		t.Errorf("a member signer carries address %q; names come from the directory", member.Email)
	}
	if external.Kind != KindExternal || external.UserID != nil {
		t.Errorf("second signer = %+v, want an external signee with no user id", external)
	}
	if external.Email != "outsider@example.org" || external.Name != "Outsider" {
		t.Errorf("external signee = %q / %q", external.Email, external.Name)
	}
}

// Placements are stored per signer and come back on the signer row, so the signing
// pass can render each signer's own marks. The unique indexes are what keep the model
// honest: one signature block per signer, one paraph per page.
func TestCreateRequestPersistsPlacementsPerSigner(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	alice := makeUser(t, pool, "alice@acme.example")

	id, created, err := store.CreateRequest(ctx, orgID, alice, "Contract.pdf", []byte(samplePDF), ModeParallel,
		[]SignerInput{
			{Kind: KindInternal, UserID: alice, Order: 1, Placements: []Placement{
				{Kind: PlacementSignature, Page: 1, X: 60, Y: 80, Width: 180, Height: 60},
				{Kind: PlacementParaph, Page: 1, X: 500, Y: 40, Width: 48, Height: 24},
				{Kind: PlacementParaph, Page: 2, X: 500, Y: 40, Width: 48, Height: 24},
			}},
			{Kind: KindExternal, Email: "outsider@example.org", Name: "Outsider", Order: 2},
		}, RecipientInput{Channel: ChannelNone})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	req, err := store.GetRequest(ctx, orgID, id)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	member, external := req.Signers[0], req.Signers[1]
	if len(member.Placements) != 3 {
		t.Fatalf("member has %d placements, want 3", len(member.Placements))
	}
	// Ordered kind then page, so the signature block comes before the paraphs.
	if got := member.Placements[0]; got.Kind != PlacementParaph || got.Page != 1 {
		t.Errorf("first placement = %+v, want the page-1 paraph", got)
	}
	block := signaturePlacement(member.Placements)
	if block == nil || block.X != 60 || block.Width != 180 {
		t.Errorf("signature placement = %+v, want the stored rectangle", block)
	}
	if len(external.Placements) != 0 {
		t.Errorf("a signer who placed nothing has %d placements", len(external.Placements))
	}

	// A second signature block for one signer is refused by the database, not just by
	// the service: the one-appearance-per-signature rule is a property of PAdES.
	_, err = pool.Exec(ctx,
		`INSERT INTO signing_signer_placements (signer_id, kind, page, x, y, width, height)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		created[0].ID, PlacementSignature, 2, 10.0, 10.0, 40.0, 20.0)
	if err == nil {
		t.Error("a second signature placement for one signer should be refused")
	}

	// A page whose crop box starts below the origin puts every rectangle on it in
	// negative coordinates. validatePlacements is what decides whether a rectangle is
	// on its page, and it accepts this one, so the table must store it.
	_, err = pool.Exec(ctx,
		`INSERT INTO signing_signer_placements (signer_id, kind, page, x, y, width, height)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		created[0].ID, PlacementParaph, 3, -10.0, -800.0, 48.0, 24.0)
	if err != nil {
		t.Errorf("a rectangle on a page with a negative-origin crop box was refused: %v", err)
	}
}

// The invitation token is the external signee's only key: it resolves to exactly
// their signer row, expires, and is replaced when re-issued.
func TestExternalInvitationTokenLifecycle(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	alice := makeUser(t, pool, "alice@acme.example")

	id, created, err := store.CreateRequest(ctx, orgID, alice, "Contract.pdf", []byte(samplePDF), ModeParallel,
		[]SignerInput{{Kind: KindExternal, Email: "outsider@example.org", Name: "Outsider", Order: 1}},
		RecipientInput{Channel: ChannelNone})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	signerID := created[0].ID

	// No token until the signee is invited.
	if _, err := store.SignerByToken(ctx, "never-issued"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("unknown token err = %v, want ErrInvalidToken", err)
	}
	if _, err := store.SignerByToken(ctx, ""); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("empty token err = %v, want ErrInvalidToken", err)
	}

	token, err := store.IssueExternalToken(ctx, id, signerID)
	if err != nil {
		t.Fatalf("IssueExternalToken: %v", err)
	}
	got, err := store.SignerByToken(ctx, token)
	if err != nil {
		t.Fatalf("SignerByToken: %v", err)
	}
	if got.SignerID != signerID || got.RequestID != id || got.OrgID != orgID {
		t.Errorf("resolved %+v, want signer %s of request %s in org %s", got, signerID, id, orgID)
	}
	if got.Email != "outsider@example.org" {
		t.Errorf("resolved address = %q", got.Email)
	}

	// Re-issuing replaces the token, so only one link is ever live.
	second, err := store.IssueExternalToken(ctx, id, signerID)
	if err != nil {
		t.Fatalf("IssueExternalToken (again): %v", err)
	}
	if second == token {
		t.Error("re-issuing must mint a new token")
	}
	if _, err := store.SignerByToken(ctx, token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("the replaced token still resolves (err = %v)", err)
	}

	// An expired link is refused just like an unknown one.
	if _, err := pool.Exec(ctx,
		"UPDATE signing_request_signers SET invite_expires_at = now() - interval '1 second' WHERE id = $1",
		signerID); err != nil {
		t.Fatalf("expire token: %v", err)
	}
	if _, err := store.SignerByToken(ctx, second); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expired token err = %v, want ErrInvalidToken", err)
	}

	// Signing leaves the link resolvable: the ceremony returns the signee to it, and it
	// is where they see that their signature landed. Replay is stopped by their signer
	// status (checkTurn answers ErrAlreadySigned), not by deleting the token.
	third, err := store.IssueExternalToken(ctx, id, signerID)
	if err != nil {
		t.Fatalf("IssueExternalToken (third): %v", err)
	}
	allSigned, err := store.RecordSignature(ctx, orgID, id, signerID, []byte(samplePDF+" signed"))
	if err != nil {
		t.Fatalf("RecordSignature: %v", err)
	}
	if !allSigned {
		t.Error("the only signer signed, so the request should report all signed")
	}
	got, err = store.SignerByToken(ctx, third)
	if err != nil {
		t.Fatalf("SignerByToken after signing: %v", err)
	}
	req, err := store.GetRequest(ctx, orgID, id)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	me := signerByID(req, got.SignerID)
	if me == nil || me.Status != SignerSigned {
		t.Fatalf("signer behind the link = %+v, want status signed", me)
	}
	if err := checkTurn(req, got.SignerID); !errors.Is(err, ErrAlreadySigned) {
		t.Errorf("a signed signee's link can start another signature (err = %v)", err)
	}
}

// A member signer has no token to issue — the link exists only for people who have no
// other way in.
func TestIssueExternalTokenRefusesAMemberSigner(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	alice := makeUser(t, pool, "alice@acme.example")

	id, created, err := store.CreateRequest(ctx, orgID, alice, "Contract.pdf", []byte(samplePDF), ModeParallel,
		[]SignerInput{{Kind: KindInternal, UserID: alice, Order: 1}}, RecipientInput{Channel: ChannelNone})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if _, err := store.IssueExternalToken(ctx, id, created[0].ID); !errors.Is(err, ErrNotSigner) {
		t.Errorf("IssueExternalToken for a member = %v, want ErrNotSigner", err)
	}
}

// The credential table now holds both subject kinds side by side: a member keyed by
// user id and an external signee keyed by address, neither overwriting the other.
func TestCredentialsAreKeyedPerSubject(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	alice := makeUser(t, pool, "alice@acme.example")
	member := internalSubject(alice)
	external := externalSubject("Outsider@Example.ORG")

	if _, err := store.GetCredential(ctx, orgID, external); !errors.Is(err, ErrNoCredential) {
		t.Errorf("unlinked external subject = %v, want ErrNoCredential", err)
	}

	if err := store.UpsertCredential(ctx, orgID, member, testCredential(t, "member-cred")); err != nil {
		t.Fatalf("UpsertCredential (member): %v", err)
	}
	if err := store.UpsertCredential(ctx, orgID, external, testCredential(t, "external-cred")); err != nil {
		t.Fatalf("UpsertCredential (external): %v", err)
	}

	gotMember, err := store.GetCredential(ctx, orgID, member)
	if err != nil {
		t.Fatalf("GetCredential (member): %v", err)
	}
	if gotMember.ID != "member-cred" {
		t.Errorf("member credential = %q, want member-cred", gotMember.ID)
	}
	// The address is normalised, so the same signee is one row however it was typed.
	gotExternal, err := store.GetCredential(ctx, orgID, externalSubject("outsider@example.org"))
	if err != nil {
		t.Fatalf("GetCredential (external): %v", err)
	}
	if gotExternal.ID != "external-cred" {
		t.Errorf("external credential = %q, want external-cred", gotExternal.ID)
	}

	// Re-linking replaces that subject's row rather than adding a second one.
	if err := store.UpsertCredential(ctx, orgID, external, testCredential(t, "external-cred-2")); err != nil {
		t.Fatalf("UpsertCredential (relink): %v", err)
	}
	relinked, err := store.GetCredential(ctx, orgID, external)
	if err != nil {
		t.Fatalf("GetCredential (relinked): %v", err)
	}
	if relinked.ID != "external-cred-2" {
		t.Errorf("relinked credential = %q, want external-cred-2", relinked.ID)
	}
	var rows int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM signing_credentials WHERE organization_id = $1", orgID).Scan(&rows); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if rows != 2 {
		t.Errorf("org holds %d credential rows, want 2 (one per subject)", rows)
	}
}

// An external signee's pending turn holds up a later member in sequential mode, and
// only lets them through once it is signed: the mixed-mode turn rule at the SQL level.
func TestListPendingForUserWaitsForAnEarlierExternalSignee(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	orgID := makeOrg(t, pool, "acme")
	alice := makeUser(t, pool, "alice@acme.example")

	id, created, err := store.CreateRequest(ctx, orgID, alice, "Contract.pdf", []byte(samplePDF), ModeSequential,
		[]SignerInput{
			{Kind: KindExternal, Email: "outsider@example.org", Name: "Outsider", Order: 1},
			{Kind: KindInternal, UserID: alice, Order: 2},
		}, RecipientInput{Channel: ChannelNone})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	pending, err := store.ListPendingForUser(ctx, orgID, alice)
	if err != nil {
		t.Fatalf("ListPendingForUser: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("the member's turn is offered while the external signee is pending (%d requests)", len(pending))
	}

	if _, err := store.RecordSignature(ctx, orgID, id, created[0].ID, []byte(samplePDF+" signed")); err != nil {
		t.Fatalf("RecordSignature (external): %v", err)
	}
	pending, err = store.ListPendingForUser(ctx, orgID, alice)
	if err != nil {
		t.Fatalf("ListPendingForUser (after): %v", err)
	}
	if len(pending) != 1 || pending[0].ID != id {
		t.Fatalf("after the external signee signed, the member's turn should be offered, got %d requests", len(pending))
	}
}
