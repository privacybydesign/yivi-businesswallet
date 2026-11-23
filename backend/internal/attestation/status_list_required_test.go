package attestation

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func seconds(d time.Duration) *int {
	s := int(d / time.Second)
	return &s
}

func TestNeedsStatusList(t *testing.T) {
	never := 0
	for name, tc := range map[string]struct {
		validity *int
		want     bool
	}{
		"an hour":          {seconds(time.Hour), false},
		"exactly 24 hours": {seconds(statusListRequiredAbove), false},
		"24 hours and one": {seconds(statusListRequiredAbove + time.Second), true},
		"the default":      {nil, true},
		"never expires":    {&never, true},
	} {
		if got := needsStatusList(tc.validity); got != tc.want {
			t.Errorf("%s: needsStatusList = %v, want %v", name, got, tc.want)
		}
	}
}

// validityStore is fakeStore with a template of a given validity, counting the
// ledger rows an issuance creates.
type validityStore struct {
	fakeStore
	validity *int
	offered  int
}

func (s *validityStore) GetTemplateDetail(context.Context, uuid.UUID, uuid.UUID) (TemplateDetail, error) {
	return TemplateDetail{ValiditySeconds: s.validity}, nil
}

func (s *validityStore) CreateOffered(context.Context, uuid.UUID, IssueInput, TemplateDetail, *uuid.UUID, *time.Time, string, string) (Issued, error) {
	s.offered++
	return Issued{}, nil
}

func issueWith(t *testing.T, validity *int, requireStatusList bool) (*validityStore, error) {
	t.Helper()
	store := &validityStore{validity: validity}
	svc := NewService(store, &fakeIssuer{}, fakeInstances{}, nil, nil, nil, nil, nil, "http://app.test")
	if requireStatusList {
		svc.RequireStatusList()
	}
	_, err := svc.Issue(context.Background(), uuid.New(), nil, "Acme", IssueInput{Recipient: Recipient{Kind: RecipientExternal}, DeliveryMethod: DeliveryMethodQR})
	return store, err
}

func TestIssueRefusesLongValidityWithoutStatusList(t *testing.T) {
	for name, validity := range map[string]*int{
		"a year":      seconds(365 * 24 * time.Hour),
		"the default": nil,
	} {
		store, err := issueWith(t, validity, true)
		if !errors.Is(err, ErrStatusListRequired) {
			t.Errorf("%s: err = %v, want ErrStatusListRequired", name, err)
		}
		if store.offered != 0 {
			t.Errorf("%s: an offered row was written for a refused issuance", name)
		}
	}
}

func TestIssueAllowsShortValidityWithoutStatusList(t *testing.T) {
	store, err := issueWith(t, seconds(statusListRequiredAbove), true)
	if err != nil {
		t.Fatalf("issuing a 24 hour credential: %v", err)
	}
	if store.offered != 1 {
		t.Errorf("offered rows = %d, want 1", store.offered)
	}
}

func TestIssueAllowsLongValidityWithStatusList(t *testing.T) {
	if _, err := issueWith(t, nil, false); err != nil {
		t.Fatalf("issuing with the default validity: %v", err)
	}
}

func TestSaveTemplateRefusesLongValidityWithoutStatusList(t *testing.T) {
	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, "")
	svc.RequireStatusList()
	h := &Handler{service: svc}
	body := `{"schemaId":"` + uuid.NewString() + `","name":"Employee","validitySeconds":86401}`

	create := httptest.NewRequest("POST", "/orgs/acme/attestations/templates", strings.NewReader(body))
	if code := apiErrorCode(t, h.createTemplate(httptest.NewRecorder(), create)); code != "status_list_required" {
		t.Errorf("create: code = %q, want status_list_required", code)
	}

	update := httptest.NewRequest("PUT", "/orgs/acme/attestations/templates/x", strings.NewReader(body))
	update.SetPathValue("id", uuid.NewString())
	if code := apiErrorCode(t, h.updateTemplate(httptest.NewRecorder(), update)); code != "status_list_required" {
		t.Errorf("update: code = %q, want status_list_required", code)
	}
}
