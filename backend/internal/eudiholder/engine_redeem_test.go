package eudiholder

import (
	"errors"
	"testing"

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

	err    error
	stored int
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
