package organization

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// fakeAddressResolver is a fixed stand-in for defaultAddressResolver.
type fakeAddressResolver struct {
	address string
	ok      bool
	err     error
}

func (f fakeAddressResolver) DefaultDigitalAddress(context.Context, uuid.UUID) (string, bool, error) {
	return f.address, f.ok, f.err
}

func detailsRequestTo(h *Handler, org Organization) (*httptest.ResponseRecorder, error) {
	req := httptest.NewRequest(http.MethodGet, "/orgs/"+org.Slug, nil)
	ctx := auth.ContextWithUser(req.Context(), user.User{ID: uuid.New()})
	req = req.WithContext(contextWithOrg(ctx, org))
	rec := httptest.NewRecorder()
	return rec, h.details(rec, req)
}

// TestDetailsUsesLiveDefaultAddress reproduces #260: the Wallet card must show
// the org's current default QERDS address, not the registration-time snapshot
// stuck on organizations.digital_address.
func TestDetailsUsesLiveDefaultAddress(t *testing.T) {
	org := Organization{ID: uuid.New(), Slug: "acme", DigitalAddress: "acme@qerds.localhost"}
	h := &Handler{
		store:          fakeRepo{},
		defaultAddress: fakeAddressResolver{address: "acme@example.eu", ok: true},
	}

	rec, err := detailsRequestTo(h, org)
	if err != nil {
		t.Fatalf("details: %v", err)
	}

	var resp orgDetailResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.DigitalAddress != "acme@example.eu" {
		t.Errorf("digitalAddress = %q, want the live default %q", resp.DigitalAddress, "acme@example.eu")
	}
}

// TestDetailsNoDefaultAddressOmitsStaleSnapshot covers the issue's "say so
// rather than fall back" requirement: with no default provisioned, the stale
// snapshot must not leak through either.
func TestDetailsNoDefaultAddressOmitsStaleSnapshot(t *testing.T) {
	org := Organization{ID: uuid.New(), Slug: "acme", DigitalAddress: "acme@qerds.localhost"}
	h := &Handler{
		store:          fakeRepo{},
		defaultAddress: fakeAddressResolver{ok: false},
	}

	rec, err := detailsRequestTo(h, org)
	if err != nil {
		t.Fatalf("details: %v", err)
	}

	var resp orgDetailResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.DigitalAddress != "" {
		t.Errorf("digitalAddress = %q, want empty when no default is provisioned", resp.DigitalAddress)
	}
}
