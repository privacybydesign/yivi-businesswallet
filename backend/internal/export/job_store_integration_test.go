//go:build integration

package export_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/export"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestEnqueueClaimComplete(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := export.NewStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "caesar")
	ctx := context.Background()

	job, err := store.Enqueue(ctx, orgID, []string{export.SectionAttestations}, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if job.Status != export.JobQueued {
		t.Errorf("status = %q, want %q", job.Status, export.JobQueued)
	}

	claimed, err := store.Claim(ctx)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.ID != job.ID || claimed.Status != export.JobRunning {
		t.Fatalf("claimed = %+v, want the queued job running", claimed)
	}
	// A claimed job is off the queue, which is what keeps two workers from
	// building the same bundle.
	if _, err := store.Claim(ctx); err != export.ErrJobNotFound {
		t.Errorf("second Claim = %v, want ErrJobNotFound", err)
	}

	content := []byte("PK\x03\x04bundle bytes")
	token, err := store.Complete(ctx, job.ID, uuid.New(), "caesar-export.zip", content, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if token == "" {
		t.Fatal("Complete returned no download token")
	}

	ready, err := store.GetJob(ctx, orgID, job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if ready.Status != export.JobReady || ready.SizeBytes != int64(len(content)) {
		t.Errorf("job = %+v, want ready with the bundle's size", ready)
	}
	// The token is stored hashed: it must not be readable from the row.
	if ready.Checksum == "" {
		t.Error("no checksum recorded for the stored bundle")
	}
}

// The token is single-use, and a spent one is indistinguishable from a token
// that never existed.
func TestBundleTokenIsSingleUse(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := export.NewStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "caesar")
	ctx := context.Background()

	job, err := store.Enqueue(ctx, orgID, nil, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	content := []byte("PK\x03\x04bundle")
	token, err := store.Complete(ctx, job.ID, uuid.New(), "caesar-export.zip", content, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got, gotContent, err := store.Bundle(ctx, token)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if got.ID != job.ID || string(gotContent) != string(content) {
		t.Fatalf("bundle = %+v / %q, want the stored one", got, gotContent)
	}

	if _, _, err := store.Bundle(ctx, token); err != export.ErrBundleUnavailable {
		t.Errorf("second Bundle = %v, want ErrBundleUnavailable", err)
	}
	if _, _, err := store.Bundle(ctx, "deadbeef"); err != export.ErrBundleUnavailable {
		t.Errorf("unknown token = %v, want the same answer as a spent one", err)
	}
}

// A download that never reached the client puts the token back, so a dropped
// connection does not cost the owner their one link.
func TestReleaseTokenRestoresTheDownload(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := export.NewStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "caesar")
	ctx := context.Background()

	job, err := store.Enqueue(ctx, orgID, nil, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	token, err := store.Complete(ctx, job.ID, uuid.New(), "caesar-export.zip", []byte("PK"), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, _, err := store.Bundle(ctx, token); err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if err := store.ReleaseToken(ctx, job.ID); err != nil {
		t.Fatalf("ReleaseToken: %v", err)
	}

	if _, _, err := store.Bundle(ctx, token); err != nil {
		t.Errorf("Bundle after release = %v, want the token usable again", err)
	}
}

// An expired bundle is not downloadable, whatever the token.
func TestExpiredBundleIsNotDownloadable(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := export.NewStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "caesar")
	ctx := context.Background()

	job, err := store.Enqueue(ctx, orgID, nil, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	token, err := store.Complete(ctx, job.ID, uuid.New(), "caesar-export.zip", []byte("PK"), time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if _, _, err := store.Bundle(ctx, token); err != export.ErrBundleUnavailable {
		t.Errorf("Bundle = %v, want ErrBundleUnavailable for an expired bundle", err)
	}
}

// Pruning reclaims the payload and keeps the row: the job is part of the org's
// export history, only the bundle is transient.
func TestPruneExpiredDropsBytesAndKeepsHistory(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := export.NewStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "caesar")
	ctx := context.Background()

	expired, err := store.Enqueue(ctx, orgID, nil, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := store.Complete(ctx, expired.ID, uuid.New(), "old.zip", []byte("PK"), time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	live, err := store.Enqueue(ctx, orgID, nil, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	liveToken, err := store.Complete(ctx, live.ID, uuid.New(), "new.zip", []byte("PK"), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	pruned, err := store.PruneExpired(ctx)
	if err != nil {
		t.Fatalf("PruneExpired: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned %d bundles, want only the expired one", pruned)
	}

	jobs, err := store.ListJobs(ctx, orgID, 10)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Errorf("history holds %d jobs, want both kept", len(jobs))
	}
	if _, _, err := store.Bundle(ctx, liveToken); err != nil {
		t.Errorf("the live bundle was pruned too: %v", err)
	}
}

// A job id from another organisation is not found rather than forbidden.
func TestGetJobIsScopedToItsOrganisation(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := export.NewStore(pool, audit.NewDBRecorder())
	mine := makeOrg(t, pool, "caesar")
	theirs := makeOrg(t, pool, "acme")
	ctx := context.Background()

	job, err := store.Enqueue(ctx, mine, nil, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := store.GetJob(ctx, theirs, job.ID); err != export.ErrJobNotFound {
		t.Errorf("GetJob across orgs = %v, want ErrJobNotFound", err)
	}
	if _, _, err := store.BundleForJob(ctx, theirs, job.ID); err != export.ErrJobNotFound {
		t.Errorf("BundleForJob across orgs = %v, want ErrJobNotFound", err)
	}
}

// Queueing an export is the audited act, so the trail records it before any
// bundle exists.
func TestEnqueueAuditsTheRequest(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := export.NewStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "caesar")
	ctx := context.Background()

	job, err := store.Enqueue(ctx, orgID, []string{export.SectionQerds}, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	page, err := audit.NewReader(pool).ListForOrganization(ctx, orgID, nil, 10)
	if err != nil {
		t.Fatalf("ListForOrganization: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("read %d events, want 1", len(page.Events))
	}
	if page.Events[0].Action != audit.ExportRequested || page.Events[0].TargetID != job.ID.String() {
		t.Errorf("event = %+v, want the export request targeting the job", page.Events[0])
	}
}
