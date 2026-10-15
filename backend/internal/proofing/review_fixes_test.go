package proofing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// Requiring diplomas on a flow that never reads the name and birth date
// would refuse every extract on its holder: it is refused when set.
func TestRequiredDiplomasNeedIdentity(t *testing.T) {
	f := newFixture()
	f.svc.flowDiplomaSettings = &fakeFlowDiplomas{modes: map[string]DiplomaMode{}}
	if _, err := f.svc.SaveFlowDiplomas(context.Background(), testOrg, appFlow.ID, DiplomasRequired); !errors.Is(err, ErrFlowNoIdentity) {
		t.Errorf("required on a flow without identity = %v, want ErrFlowNoIdentity", err)
	}
	if _, err := f.svc.SaveFlowDiplomas(context.Background(), testOrg, appFlow.ID, DiplomasOff); err != nil {
		t.Errorf("off on a flow without identity = %v, want saved", err)
	}
	if _, err := f.svc.SaveFlowDiplomas(context.Background(), testOrg, chipFlow.ID, DiplomasRequired); err != nil {
		t.Errorf("required on a flow reading identity = %v, want saved", err)
	}
}

// Without an encryption key, a keyed call gets the same documented 409 as an
// unkeyed one, not a 500.
func TestIdempotencyWithoutKeyIs409(t *testing.T) {
	h := &Handler{idempotency: NewIdempotencyStore(nil, nil)}
	r := httptest.NewRequest(http.MethodPost, "/proofing/sessions", strings.NewReader(`{}`))
	r.Header.Set(idempotencyHeader, "k-1")
	r = r.WithContext(context.WithValue(r.Context(), apiCallerKey{}, APIKeyCaller{CustomerID: uuid.New()}))
	w := httptest.NewRecorder()
	h.idempotent(func(http.ResponseWriter, *http.Request) error {
		t.Error("the call ran without a key to store its answer")
		return nil
	}).ServeHTTP(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "no_encryption_key") {
		t.Errorf("answer = %d %s, want 409 no_encryption_key", w.Code, w.Body.String())
	}
}

// A live link spends its customer's hosted budget; once it is over it only
// spends its own, so an old link polled hard leaves the customer's live pages
// alone.
func TestStaleHostedLinkSpendsItsOwnBudget(t *testing.T) {
	f := newFixture()
	_, token := f.sendHosted(t)
	_, live, err := f.svc.hostedLimitKey(context.Background(), token)
	if err != nil {
		t.Fatalf("hostedLimitKey: %v", err)
	}
	if !live.live || live.key != initech.ID.String() {
		t.Errorf("waiting link's budget = %+v, want its customer's", live)
	}
	if _, err := f.svc.DeclineHosted(context.Background(), token); err != nil {
		t.Fatalf("DeclineHosted: %v", err)
	}
	_, stale, err := f.svc.hostedLimitKey(context.Background(), token)
	if err != nil {
		t.Fatalf("hostedLimitKey: %v", err)
	}
	if stale.live || stale.key == initech.ID.String() {
		t.Errorf("declined link's budget = %+v, want its own", stale)
	}
}

// An ID card's MRZ cuts a long name off; the chip's DG11 holds it whole, and
// a request for that person matches it.
func TestExpectedSubjectMatchesTheChipsFullName(t *testing.T) {
	const fullName = "Johanna Wilhelmina Maria van den Berg-Schoonhoven"
	for name, tc := range map[string]struct {
		identity proofingprovider.Identity
		want     Status
	}{
		"DG11 has the whole name": {proofingprovider.Identity{
			GivenName: "JOHA", FamilyName: "VAN DEN BERG SCHOONHOVEN", BirthDate: testBirthDate,
			FullGivenName: "Johanna Wilhelmina Maria", FullFamilyName: "van den Berg-Schoonhoven",
		}, StatusApproved},
		"no DG11: the cut-off MRZ name": {proofingprovider.Identity{
			GivenName: "JOHA", FamilyName: "VAN DEN BERG SCHOONHOVEN", BirthDate: testBirthDate,
		}, StatusRejected},
		"DG11 names someone else": {proofingprovider.Identity{
			GivenName: "JOHA", FamilyName: "VAN DEN BERG SCHOONHOVEN", BirthDate: testBirthDate,
			FullGivenName: "Johanna", FullFamilyName: "van den Berg",
		}, StatusRejected},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			f.sendForPerson(t, fullName, testBirthDate)
			f.ips.identity = tc.identity
			f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved}
			if req := f.reconcile(t); req.Status != tc.want {
				t.Errorf("status = %s (%s), want %s", req.Status, req.ErrorCode, tc.want)
			}
		})
	}
}

// settingsChange sets every one of settings, as a whole-form save does; an
// unset erasure handling is left as it is.
func settingsChange(settings CustomerSettings) CustomerSettingsChange {
	change := CustomerSettingsChange{SessionTTL: &settings.SessionTTL, DataRetentionDays: &settings.DataRetentionDays}
	if settings.ErasureHandling != "" {
		change.ErasureHandling = &settings.ErasureHandling
	}
	return change
}
