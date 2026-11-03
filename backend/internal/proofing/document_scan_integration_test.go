//go:build integration

package proofing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// A request whose flow photographs the document keeps the scan its page
// started for its session, and when the photos went in; a purge clears the
// transaction, which reads them.
func TestStoreDocumentScan(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()

	in := newStoredRequest(orgID, sam, externalSubject("a@example.org"))
	in.Flow.Steps = []string{"document_capture", "nfc_read", stepDocumentPhoto}
	req := createStarted(t, store, in, "s1")
	if !req.DocumentScan || req.DocumentScannedAt != nil || req.documentScanTransactionID != "" {
		t.Fatalf("created = %+v; want a document scan, none started", req)
	}

	if err := store.SetDocumentScanTransaction(ctx, req, "s-other", "trx-1"); !errors.Is(err, ErrSessionOver) {
		t.Errorf("another session's scan: %v, want ErrSessionOver", err)
	}
	for _, trx := range []string{"trx-1", "trx-2"} {
		if err := store.SetDocumentScanTransaction(ctx, req, "s1", trx); err != nil {
			t.Fatalf("SetDocumentScanTransaction(%s): %v", trx, err)
		}
	}
	// The engine's notice can settle the request on these photos before the
	// finish marks them.
	if err := store.RecordOutcome(ctx, req, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if err := store.MarkDocumentScanned(ctx, req, "s1", "trx-1"); !errors.Is(err, ErrSessionOver) {
		t.Errorf("an earlier scan: %v, want ErrSessionOver", err)
	}
	if err := store.MarkDocumentScanned(ctx, req, "s1", "trx-2"); err != nil {
		t.Fatalf("MarkDocumentScanned: %v", err)
	}
	scanned, err := store.Get(ctx, orgID, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scanned.documentScanTransactionID != "trx-2" || scanned.DocumentScannedAt == nil {
		t.Errorf("scanned = %q at %v; want trx-2, done", scanned.documentScanTransactionID, scanned.DocumentScannedAt)
	}

	if err := store.Purge(ctx, scanned, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	var trx *string
	if err := pool.QueryRow(ctx, `SELECT document_scan_transaction_id FROM identity_proofing_requests WHERE id = $1`, req.ID).Scan(&trx); err != nil {
		t.Fatal(err)
	}
	if trx != nil {
		t.Errorf("purged transaction = %q, want it cleared", *trx)
	}
}

// A scan out of the scan service's attempts is audited on its request with
// the task, attempts and code; an erased request records nothing.
func TestStoreDocumentScanFailed(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	req := createStarted(t, store, newStoredRequest(orgID, sam, externalSubject("a@example.org")), "s1")

	failure := proofingprovider.DocumentScanFailure{Task: "document_scan", Code: "image_quality", Attempts: 3}
	if err := store.RecordDocumentScanFailed(ctx, req, failure); err != nil {
		t.Fatalf("RecordDocumentScanFailed: %v", err)
	}
	var target, metadata string
	if err := pool.QueryRow(ctx, `SELECT target_id, metadata::text FROM audit_events WHERE action = $1`,
		audit.IdentityProofingDocumentScanFailed).Scan(&target, &metadata); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{`"task": "document_scan"`, `"attempts": 3`, `"scanCode": "image_quality"`} {
		if target != req.ID.String() || !strings.Contains(metadata, want) {
			t.Errorf("audited on %s with %s; want the request, with %s", target, metadata, want)
		}
	}

	if err := store.Purge(ctx, req, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if err := store.RecordDocumentScanFailed(ctx, req, failure); err != nil {
		t.Fatalf("RecordDocumentScanFailed after purge: %v", err)
	}
	if n := countAction(t, pool, audit.IdentityProofingDocumentScanFailed); n != 1 {
		t.Errorf("document_scan_failed audits = %d, want the one before the purge", n)
	}
}

// A request on a flow that takes no document photo has no scan.
func TestStoreNoDocumentScan(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	req := createStarted(t, store, newStoredRequest(orgID, sam, externalSubject("a@example.org")), "s1")
	if req.DocumentScan {
		t.Error("a flow without document_photo has a document scan")
	}
}
