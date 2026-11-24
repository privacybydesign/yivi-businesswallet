package attestation

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
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
	svc.SetStatusListPolicy(StatusListPolicy{Enforce: true, Default: !requireStatusList})
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

// templateFakes answer the schema and template lookups of the save handlers.
type templateFakes struct {
	schemaStore
	templateStore
	rulebook string
}

func (f templateFakes) GetSchema(context.Context, uuid.UUID, uuid.UUID) (Schema, error) {
	return Schema{Rulebook: f.rulebook}, nil
}

func (f templateFakes) GetTemplate(context.Context, uuid.UUID, uuid.UUID) (Template, error) {
	return Template{Rulebook: f.rulebook}, nil
}

// saveCodes saves a template valid one second over 24 hours, by create and by
// update, and returns the error codes.
func saveCodes(t *testing.T, policy StatusListPolicy, rulebook string) (create, update string) {
	t.Helper()
	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, "")
	svc.SetStatusListPolicy(policy)
	fakes := templateFakes{rulebook: rulebook}
	h := &Handler{service: svc, schemas: fakes, templates: fakes}
	body := `{"schemaId":"` + uuid.NewString() + `","name":"Employee","validitySeconds":86401}`

	ctx := organization.ContextWithOrg(context.Background(), organization.Organization{ID: uuid.New()})
	createReq := httptest.NewRequestWithContext(ctx, "POST", "/orgs/acme/attestations/templates", strings.NewReader(body))
	create = apiErrorCode(t, h.createTemplate(httptest.NewRecorder(), createReq))

	updateReq := httptest.NewRequestWithContext(ctx, "PUT", "/orgs/acme/attestations/templates/x", strings.NewReader(body))
	updateReq.SetPathValue("id", uuid.NewString())
	update = apiErrorCode(t, h.updateTemplate(httptest.NewRecorder(), updateReq))
	return create, update
}

func TestSaveTemplateRefusesLongValidityWithoutStatusList(t *testing.T) {
	create, update := saveCodes(t, StatusListPolicy{Enforce: true}, "")
	if create != "status_list_required" || update != "status_list_required" {
		t.Errorf("codes = %q, %q, want status_list_required twice", create, update)
	}
}

// The rulebook issuer ignores the template validity, so a rulebook template is
// held to that issuer's status list instead.
func TestRulebookTemplateFollowsTheRulebookIssuer(t *testing.T) {
	for name, tc := range map[string]struct {
		policy StatusListPolicy
		want   bool
	}{
		"rulebook issuer without a list":          {StatusListPolicy{Enforce: true, Default: true, RulebookRouted: true}, true},
		"rulebook issuer with a list":             {StatusListPolicy{Enforce: true, RulebookRouted: true, Rulebook: true}, false},
		"no rulebook issuer, default without one": {StatusListPolicy{Enforce: true}, true},
		"a local stack":                           {StatusListPolicy{RulebookRouted: true}, false},
	} {
		err := serviceWithPolicy(tc.policy).CheckValidity(seconds(48*time.Hour), true)
		if got := errors.Is(err, ErrStatusListRequired) || errors.Is(err, ErrRulebookStatusListRequired); got != tc.want {
			t.Errorf("%s: refused = %v, want %v", name, got, tc.want)
		}
	}
	create, update := saveCodes(t, StatusListPolicy{Enforce: true, Default: true, RulebookRouted: true}, "lei")
	if create != "rulebook_status_list_required" || update != "rulebook_status_list_required" {
		t.Errorf("rulebook template without a rulebook status list: codes = %q, %q, want rulebook_status_list_required twice", create, update)
	}
}

func serviceWithPolicy(p StatusListPolicy) *Service {
	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, "")
	svc.SetStatusListPolicy(p)
	return svc
}
