//go:build integration

package proofing

import (
	"context"
	"errors"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// A session attached with no app leaves the request's method empty, even one
// the request was created with; SetMethod then stores the chosen app once.
func TestStoreSetMethod(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()

	in := newStoredRequest(orgID, sam, externalSubject("a@example.org"))
	in.Method = proofingprovider.MethodIdem
	req, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if req.Method != proofingprovider.MethodIdem {
		t.Fatalf("created method = %q, want %q", req.Method, proofingprovider.MethodIdem)
	}
	req.Method = ""
	if ok, err := store.AttachSession(ctx, req, attachedSession("s1")); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	storedMethod := func() *string {
		t.Helper()
		var method *string
		if err := pool.QueryRow(ctx, `SELECT method FROM identity_proofing_requests WHERE id = $1`, req.ID).Scan(&method); err != nil {
			t.Fatal(err)
		}
		return method
	}
	if m := storedMethod(); m != nil {
		t.Fatalf("method after a pending attach = %q, want NULL", *m)
	}

	if err := store.SetMethod(ctx, req, "s-other", proofingprovider.MethodIdem); !errors.Is(err, ErrSessionOver) {
		t.Errorf("another session's method: %v, want ErrSessionOver", err)
	}
	if err := store.SetMethod(ctx, req, "s1", proofingprovider.MethodIdem); err != nil {
		t.Fatalf("SetMethod: %v", err)
	}
	if err := store.SetMethod(ctx, req, "s1", proofingprovider.MethodYivi); !errors.Is(err, ErrSessionOver) {
		t.Errorf("second SetMethod: %v, want ErrSessionOver", err)
	}
	if m := storedMethod(); m == nil || *m != string(proofingprovider.MethodIdem) {
		t.Errorf("stored method = %v, want %q", m, proofingprovider.MethodIdem)
	}
	got, err := store.Get(ctx, orgID, req.ID)
	if err != nil || got.Method != proofingprovider.MethodIdem {
		t.Errorf("Get method = %q, %v; want %q", got.Method, err, proofingprovider.MethodIdem)
	}
}
