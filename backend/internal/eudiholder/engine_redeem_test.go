package eudiholder

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/privacybydesign/irmago/common/clientmodels"
	"github.com/privacybydesign/irmago/eudi/credentials/proofs"
	"github.com/privacybydesign/irmago/eudi/openid4vci"
	"github.com/privacybydesign/irmago/eudi/storage/db"
	"github.com/privacybydesign/irmago/eudi/storage/db/models"
	"gorm.io/datatypes"
)

// fakeCredentialStore stands in for irmago's holder credential store. StoreBatch
// generates instance ids the way irmago's BeforeCreate hook does, which is what
// storeRecorder reads back. The embedded interface satisfies the rest of
// db.CredentialStore; nothing under test calls those methods.
type fakeCredentialStore struct {
	db.CredentialStore

	err       error
	stored    int
	instances []db.CredentialStatusInstance
}

func (f *fakeCredentialStore) ListInstancesWithStatusReference() ([]db.CredentialStatusInstance, error) {
	return f.instances, f.err
}

func (f *fakeCredentialStore) StoreBatch(batch *models.CredentialBatch) error {
	if f.err != nil {
		return f.err
	}
	f.stored++
	for i := range batch.Instances {
		if batch.Instances[i].ID.IsNil() {
			batch.Instances[i].ID = datatypes.NewUUIDv4()
		}
	}
	return nil
}

func batchWithInstances(vct string, instances int) *models.CredentialBatch {
	return &models.CredentialBatch{
		VerifiableCredentialType: vct,
		Instances:                make([]models.IssuedCredentialInstance, instances),
	}
}

// TestStoreRecorderCapturesInstanceRefPerVCT is the receive half of the
// "held-credential detail shows the wrong credential" fix: irmago reports a
// completed session with the *offered* credential, which carries no instance id and
// no hash, so the ref must come from the batch the redemption actually stored.
// Two credentials of one vct must therefore yield two distinct refs.
func TestStoreRecorderCapturesInstanceRefPerVCT(t *testing.T) {
	t.Parallel()
	rec := &storeRecorder{CredentialStore: &fakeCredentialStore{}}

	first := batchWithInstances("nl.kvk.registration", 1)
	if err := rec.StoreBatch(first); err != nil {
		t.Fatalf("store first batch: %v", err)
	}
	ref := rec.refFor("nl.kvk.registration")
	if want := first.Instances[0].ID.String(); ref != want {
		t.Fatalf("refFor = %q, want the stored instance id %q", ref, want)
	}

	// A second credential of the same vct must not overwrite the first's ref: the
	// held index needs one ref per credential, which is what vct alone cannot give.
	second := batchWithInstances("nl.kvk.registration", 1)
	if err := rec.StoreBatch(second); err != nil {
		t.Fatalf("store second batch: %v", err)
	}
	if got := rec.refFor("nl.kvk.registration"); got != ref {
		t.Fatalf("refFor after a second store = %q, want the first batch's ref %q", got, ref)
	}
	if first.Instances[0].ID == second.Instances[0].ID {
		t.Fatal("the two stored batches share an instance id")
	}
}

// TestStoreRecorderRefForFallsBackToFirstBatch covers the vct mismatch: the
// credential irmago reports is built from the first fetched configuration, which is
// also the first one stored, so the first batch is still the right credential when
// the issued vct differs from the one asked for.
func TestStoreRecorderRefForFallsBackToFirstBatch(t *testing.T) {
	t.Parallel()
	rec := &storeRecorder{CredentialStore: &fakeCredentialStore{}}

	batch := batchWithInstances("vct.as.issued", 1)
	if err := rec.StoreBatch(batch); err != nil {
		t.Fatalf("store: %v", err)
	}
	if got, want := rec.refFor("vct.from.metadata"), batch.Instances[0].ID.String(); got != want {
		t.Fatalf("refFor(other vct) = %q, want the first stored ref %q", got, want)
	}
}

func TestStoreRecorderRefForEmptyWithoutAStore(t *testing.T) {
	t.Parallel()
	rec := &storeRecorder{CredentialStore: &fakeCredentialStore{}}
	if got := rec.refFor("nl.kvk.registration"); got != "" {
		t.Fatalf("refFor with nothing stored = %q, want empty", got)
	}
}

// TestStoreRecorderPropagatesStoreFailure keeps the decorator transparent: a failed
// insert must surface unchanged and record no ref.
func TestStoreRecorderPropagatesStoreFailure(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("insert failed")
	rec := &storeRecorder{CredentialStore: &fakeCredentialStore{err: wantErr}}

	if err := rec.StoreBatch(batchWithInstances("nl.kvk.registration", 1)); !errors.Is(err, wantErr) {
		t.Fatalf("StoreBatch error = %v, want %v", err, wantErr)
	}
	if got := rec.refFor("nl.kvk.registration"); got != "" {
		t.Fatalf("refFor after a failed store = %q, want empty", got)
	}
}

// TestStoreRecorderIgnoresInstancelessBatch guards the index read: irmago rejects an
// empty batch, but the recorder must not panic if one ever reaches it.
func TestStoreRecorderIgnoresInstancelessBatch(t *testing.T) {
	t.Parallel()
	rec := &storeRecorder{CredentialStore: &fakeCredentialStore{}}
	if err := rec.StoreBatch(batchWithInstances("nl.kvk.registration", 0)); err != nil {
		t.Fatalf("store: %v", err)
	}
	if got := rec.refFor("nl.kvk.registration"); got != "" {
		t.Fatalf("refFor for an instanceless batch = %q, want empty", got)
	}
}

// A single credential's refresh hands irmago's sweep only its own batch.
func TestBatchStatusStoreListsOneBatch(t *testing.T) {
	target, other := datatypes.NewUUIDv4(), datatypes.NewUUIDv4()
	store := batchStatusStore{
		CredentialStore: &fakeCredentialStore{instances: []db.CredentialStatusInstance{
			{InstanceID: datatypes.NewUUIDv4(), BatchID: other, StatusListURI: "https://issuer.test/other"},
			{InstanceID: datatypes.NewUUIDv4(), BatchID: target, StatusListURI: "https://issuer.test/target"},
			{InstanceID: datatypes.NewUUIDv4(), BatchID: target, StatusListURI: "https://issuer.test/target"},
		}},
		batchID: target,
	}
	got, err := store.ListInstancesWithStatusReference()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d instances, want the target batch's 2", len(got))
	}
	for _, inst := range got {
		if inst.BatchID != target {
			t.Errorf("listed an instance of batch %s, want only %s", inst.BatchID, target)
		}
	}
}

func TestOfferTxCodeReadsTheValueTheOfferCarries(t *testing.T) {
	withValue := "openid-credential-offer://?credential_offer=" + url.QueryEscape(
		`{"credential_issuer":"https://i.example","grants":{"urn:ietf:params:oauth:grant-type:pre-authorized_code":{"pre-authorized_code":"c","tx_code":{"length":5,"value":12345}}}}`)
	if got := offerTxCode(withValue); got == nil || *got != "12345" {
		t.Errorf("offerTxCode = %v, want 12345", got)
	}
	asString := strings.Replace(withValue, url.QueryEscape(`12345`), url.QueryEscape(`"12345"`), 1)
	if got := offerTxCode(asString); got == nil || *got != "12345" {
		t.Errorf("offerTxCode(string value) = %v, want 12345", got)
	}

	withoutValue := "openid-credential-offer://?credential_offer=" + url.QueryEscape(
		`{"credential_issuer":"https://i.example","grants":{"urn:ietf:params:oauth:grant-type:pre-authorized_code":{"pre-authorized_code":"c","tx_code":{"length":5}}}}`)
	for _, uri := range []string{withoutValue, "openid-credential-offer://?credential_offer_uri=https%3A%2F%2Fi.example%2Fo", "%%"} {
		if got := offerTxCode(uri); got != nil {
			t.Errorf("offerTxCode(%q) = %q, want nil", uri, *got)
		}
	}
}

type countingBinder struct {
	openid4vci.HolderKeyBinder

	asked []uint
}

func (c *countingBinder) CreateKeyPairsWithProofs(num uint, _ proofs.ProofBuilder) ([]models.PublicHolderBindingKey, []string, error) {
	c.asked = append(c.asked, num)
	return make([]models.PublicHolderBindingKey, num), make([]string, num), nil
}

// TestOneCopyBinderAsksForOneCopy: the EU reference issuer advertises batch_size
// 100, and 100 WSCA keys plus their proofs exceed the wallet provider's rate limit.
func TestOneCopyBinderAsksForOneCopy(t *testing.T) {
	t.Parallel()

	for _, num := range []uint{0, 1, 100} {
		inner := &countingBinder{}
		keys, proofsOut, err := oneCopyBinder{inner}.CreateKeyPairsWithProofs(num, nil)
		if err != nil {
			t.Fatalf("num %d: %v", num, err)
		}
		want := min(num, copiesPerCredential)
		if len(inner.asked) != 1 || inner.asked[0] != want {
			t.Errorf("num %d: inner binder asked for %v, want [%d]", num, inner.asked, want)
		}
		if uint(len(keys)) != want || uint(len(proofsOut)) != want {
			t.Errorf("num %d: got %d keys and %d proofs, want %d each", num, len(keys), len(proofsOut), want)
		}
	}
}

// TestRedeemFailureMarksUsedOffer: only the issuer refusing the code makes the
// offer unredeemable; a failure before that, like WSCA throttling, stays
// retryable. The wrapped errors are the ones staging logged.
func TestRedeemFailureMarksUsedOffer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wrapped string
		used    bool
	}{
		{
			name:    "code refused",
			wrapped: "could not continue issuance session: token response returned status code 400, invalid_grantCode inactive - .",
			used:    true,
		},
		{
			name:    "wsca throttled",
			wrapped: `could not obtain credential "we-build-eucc": could not create key pairs: irmabinding: WSCA GenerateKey failed: execute: server returned 429: too many attempts; please retry later`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newRedeemHandler(nil)
			h.Failure(&clientmodels.SessionError{WrappedError: tt.wrapped})
			res := <-h.done
			if res.err == nil {
				t.Fatal("want an error")
			}
			if got := errors.Is(res.err, ErrOfferUsed); got != tt.used {
				t.Errorf("errors.Is(ErrOfferUsed) = %v, want %v (err: %v)", got, tt.used, res.err)
			}
		})
	}
}
