package proofing

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/addresslookup"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
)

type fakeFlowAddresses struct{ modes map[string]AddressMode }

func (f *fakeFlowAddresses) Get(_ context.Context, _ uuid.UUID, flowID string) (AddressMode, error) {
	if mode, ok := f.modes[flowID]; ok {
		return mode, nil
	}
	return AddressOff, nil
}

func (f *fakeFlowAddresses) All(context.Context, uuid.UUID) (map[string]AddressMode, error) {
	return f.modes, nil
}

func (f *fakeFlowAddresses) Save(_ context.Context, _ uuid.UUID, flowID string, mode AddressMode) (AddressMode, error) {
	f.modes[flowID] = mode
	return mode, nil
}

var testAddress = Address{Street: "Kerkstraat", HouseNumber: "12a", PostalCode: "6511 AB", City: "Nijmegen", Country: "Nederland"}

// newAddressFixture is a provisioned fixture whose appFlow asks for the
// address as mode says.
func newAddressFixture(mode AddressMode) fixture {
	f := newFixture()
	f.svc.flowAddressSettings = &fakeFlowAddresses{modes: map[string]AddressMode{appFlow.ID: mode}}
	return f
}

// sendApprovedAddress sends appFlow on screen and has IPS approve it
// completedAgo ago.
func sendApprovedAddress(t *testing.T, f fixture, completedAgo time.Duration) Request {
	t.Helper()
	if _, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID, Channel: ChannelOnScreen}); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	completed := time.Now().Add(-completedAgo)
	f.requests.stored.Status, f.requests.stored.CompletedAt, f.requests.stored.CheckedAt = StatusApproved, &completed, &completed
	return *f.requests.stored
}

func TestAddressFlowIsNotMailed(t *testing.T) {
	f := newAddressFixture(AddressRequired)
	_, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID})
	if !errors.Is(err, ErrAddressNeedsPage) {
		t.Fatalf("CreateRequest by mail = %v, want ErrAddressNeedsPage", err)
	}
	if len(f.ips.sessions) != 0 {
		t.Errorf("sessions created = %d, want none", len(f.ips.sessions))
	}
}

func TestAddressModeKeptOnRequest(t *testing.T) {
	f := newAddressFixture(AddressRequired)
	sendApprovedAddress(t, f, 0)
	if got := f.requests.created[0].Address; got != AddressRequired {
		t.Errorf("stored address mode = %q, want required", got)
	}
}

func TestAddAddressTrimsAndKeeps(t *testing.T) {
	f := newAddressFixture(AddressRequired)
	req := sendApprovedAddress(t, f, time.Minute)
	in := testAddress
	in.Street = "  Kerkstraat "
	got, err := f.svc.AddAddress(context.Background(), testOrg.ID, req.ID, nil, in)
	if err != nil {
		t.Fatalf("AddAddress: %v", err)
	}
	want := testAddress
	want.Verification = AddressUnchecked
	if *got.SubjectAddress != want || *f.requests.stored.SubjectAddress != want {
		t.Errorf("address = %+v, stored %+v; want %+v", *got.SubjectAddress, *f.requests.stored.SubjectAddress, want)
	}
}

// The address is taken while the engine still reviews the identity, so the
// subject gives it before a reviewer decides.
func TestAddAddressDuringReview(t *testing.T) {
	f := newAddressFixture(AddressRequired)
	req := sendApprovedAddress(t, f, time.Minute)
	f.requests.stored.Status = StatusNeedsReview
	if _, err := f.svc.AddAddress(context.Background(), testOrg.ID, req.ID, nil, testAddress); err != nil {
		t.Fatalf("AddAddress in review: %v", err)
	}
	if f.requests.stored.SubjectAddress == nil {
		t.Error("the address entered in review was not kept")
	}
}

func TestAddAddressRefused(t *testing.T) {
	cases := []struct {
		name         string
		mode         AddressMode
		status       Status
		completedAgo time.Duration
		given        bool
		address      Address
		want         error
	}{
		{"flow asks none", AddressOff, StatusApproved, 0, false, testAddress, ErrAddressNotAsked},
		{"identity rejected", AddressRequired, StatusRejected, 0, false, testAddress, ErrAddressClosed},
		{"window passed", AddressRequired, StatusApproved, AddressWindow + time.Minute, false, testAddress, ErrAddressClosed},
		{"entered already", AddressRequired, StatusApproved, 0, true, testAddress, ErrAddressClosed},
		{"city missing", AddressRequired, StatusApproved, 0, false, Address{Street: "Kerkstraat", HouseNumber: "1", PostalCode: "1234 AB", Country: "NL"}, ErrInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAddressFixture(tc.mode)
			sendApprovedAddress(t, f, tc.completedAgo)
			f.requests.stored.Status = tc.status
			if tc.given {
				given := testAddress
				f.requests.stored.SubjectAddress = &given
			}
			if _, err := f.svc.AddAddress(context.Background(), testOrg.ID, f.requests.stored.ID, nil, tc.address); !errors.Is(err, tc.want) {
				t.Errorf("AddAddress = %v, want %v", err, tc.want)
			}
		})
	}
}

// fakeAddressLookup is the register: found at every postal code and number,
// err when it cannot be asked.
type fakeAddressLookup struct {
	found   []addresslookup.Address
	err     error
	queries []string
}

func (f *fakeAddressLookup) Find(_ context.Context, postalCode string, houseNumber int) ([]addresslookup.Address, error) {
	f.queries = append(f.queries, postalCode+" "+strconv.Itoa(houseNumber))
	return f.found, f.err
}

var registerAddress = addresslookup.Address{Street: "Kerkstraat", HouseNumber: "12A", PostalCode: "6511 AB", City: "Nijmegen"}

// testReason is a reviewer's reason for an address decision.
const testReason = "Matches the rental contract"

func TestAddAddressVerification(t *testing.T) {
	abroad := Address{Street: "Rue de la Loi", HouseNumber: "16", PostalCode: "1000", City: "Bruxelles", Country: "België"}
	cases := []struct {
		name    string
		lookup  *fakeAddressLookup
		address Address
		want    Address
		query   string
	}{
		{
			"found, kept as the register writes it", &fakeAddressLookup{found: []addresslookup.Address{registerAddress}},
			Address{Street: "kerkstraat", HouseNumber: "12 a", PostalCode: "6511ab", City: "Nijmegen", Country: "NL"},
			Address{Street: "Kerkstraat", HouseNumber: "12A", PostalCode: "6511 AB", City: "Nijmegen", Country: "Nederland", Verification: AddressVerified},
			"6511AB 12",
		},
		{
			"register down", &fakeAddressLookup{err: errors.New("pdok down")}, testAddress,
			Address{Street: "Kerkstraat", HouseNumber: "12a", PostalCode: "6511 AB", City: "Nijmegen", Country: "Nederland", Verification: AddressUnchecked},
			"6511AB 12",
		},
		{
			"not Dutch", &fakeAddressLookup{}, abroad,
			Address{Street: "Rue de la Loi", HouseNumber: "16", PostalCode: "1000", City: "Bruxelles", Country: "België", Verification: AddressUnsupported},
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAddressFixture(AddressRequired)
			f.svc.SetAddressLookup(tc.lookup)
			req := sendApprovedAddress(t, f, time.Minute)
			in := tc.address
			in.Verification = AddressVerified
			if tc.want.Verification == AddressVerified {
				in.Verification = ""
			}
			got, err := f.svc.AddAddress(context.Background(), testOrg.ID, req.ID, nil, in)
			if err != nil {
				t.Fatalf("AddAddress: %v", err)
			}
			if *got.SubjectAddress != tc.want {
				t.Errorf("address = %+v, want %+v", *got.SubjectAddress, tc.want)
			}
			if tc.query == "" && len(tc.lookup.queries) != 0 || tc.query != "" && (len(tc.lookup.queries) != 1 || tc.lookup.queries[0] != tc.query) {
				t.Errorf("register asked %q, want %q", tc.lookup.queries, tc.query)
			}
		})
	}
}

func TestAddAddressNotInRegister(t *testing.T) {
	cases := []struct {
		name    string
		address Address
	}{
		{"other street", Address{Street: "Molenstraat", HouseNumber: "12A", PostalCode: "6511 AB", City: "Nijmegen", Country: "NL"}},
		{"other letter", Address{Street: "Kerkstraat", HouseNumber: "12B", PostalCode: "6511 AB", City: "Nijmegen", Country: "NL"}},
		{"no number", Address{Street: "Kerkstraat", HouseNumber: "A", PostalCode: "6511 AB", City: "Nijmegen", Country: "NL"}},
		{"mistyped postal code", Address{Street: "Kerkstraat", HouseNumber: "12A", PostalCode: "65111", City: "Nijmegen", Country: "Netherlands"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAddressFixture(AddressRequired)
			f.svc.SetAddressLookup(&fakeAddressLookup{found: []addresslookup.Address{registerAddress}})
			req := sendApprovedAddress(t, f, time.Minute)
			if _, err := f.svc.AddAddress(context.Background(), testOrg.ID, req.ID, nil, tc.address); !errors.Is(err, ErrAddressNotFound) {
				t.Fatalf("AddAddress = %v, want ErrAddressNotFound", err)
			}
			if f.requests.stored.SubjectAddress != nil {
				t.Errorf("address kept: %+v", *f.requests.stored.SubjectAddress)
			}
		})
	}
}

func TestLookUpAddress(t *testing.T) {
	f := newAddressFixture(AddressRequired)
	lookup := &fakeAddressLookup{found: []addresslookup.Address{registerAddress}}
	f.svc.SetAddressLookup(lookup)
	req := sendApprovedAddress(t, f, time.Minute)

	for _, in := range [][2]string{{"1000", "16"}, {"6511 AB", "A"}, {"", "12"}} {
		got, err := f.svc.LookUpAddress(context.Background(), testOrg.ID, req.ID, nil, in[0], in[1])
		if err != nil || len(got) != 0 || len(lookup.queries) != 0 {
			t.Fatalf("LookUpAddress(%q) = %v, %v, asked %q; want nothing asked", in, got, err, lookup.queries)
		}
	}
	got, err := f.svc.LookUpAddress(context.Background(), testOrg.ID, req.ID, nil, " 6511ab ", "12a")
	if err != nil || len(got) != 1 || got[0] != registerAddress || lookup.queries[0] != "6511AB 12" {
		t.Fatalf("LookUpAddress = %v, %v, asked %q", got, err, lookup.queries)
	}

	lookup.err = errors.New("pdok down")
	if _, err := f.svc.LookUpAddress(context.Background(), testOrg.ID, req.ID, nil, "6511 AB", "12"); !errors.Is(err, ErrAddressLookupUnavailable) {
		t.Errorf("register down = %v, want ErrAddressLookupUnavailable", err)
	}

	checked := time.Now().Add(-AddressWindow - time.Minute)
	f.requests.stored.CheckedAt = &checked
	lookup.queries = nil
	if _, err := f.svc.LookUpAddress(context.Background(), testOrg.ID, req.ID, nil, "6511 AB", "12"); !errors.Is(err, ErrAddressClosed) || len(lookup.queries) != 0 {
		t.Errorf("window passed = %v, asked %q; want ErrAddressClosed and nothing asked", err, lookup.queries)
	}
}

// brpClaims are the registered address of testAddress as the Yivi app
// discloses it.
var brpClaims = map[string]string{
	openid4vpverifier.ClaimAddressStreet: "Kerkstraat", openid4vpverifier.ClaimAddressHouseNumber: "12 A",
	openid4vpverifier.ClaimAddressPostalCode: "6511AB", openid4vpverifier.ClaimAddressCity: "Nijmegen",
}

// discloseYiviAddress runs a Yivi request on a flow whose address mode is
// mode, its disclosure carrying registered when set, and approves it.
func discloseYiviAddress(t *testing.T, mode AddressMode, registered map[string]string) fixture {
	t.Helper()
	f := newAddressFixture(mode)
	ctx := context.Background()
	sent := f.sendYivi(t)
	if _, err := f.svc.StartYivi(ctx, testOrg.ID, sent.Request.ID, nil); err != nil {
		t.Fatalf("StartYivi: %v", err)
	}
	byCredential := map[string]map[string]string{"idcard": {openid4vpverifier.ClaimGivenNames: "Anna"}}
	if registered != nil {
		byCredential["address"] = registered
	}
	f.verifier.presentation = &openid4vpverifier.Presentation{ByCredential: byCredential}
	if _, err := f.svc.YiviDisclosure(ctx, testOrg.ID, sent.Request.ID, nil); err != nil {
		t.Fatalf("YiviDisclosure: %v", err)
	}
	completed := time.Now()
	f.requests.stored.Status, f.requests.stored.CompletedAt, f.requests.stored.CheckedAt = StatusApproved, &completed, &completed
	return f
}

func TestYiviAsksRegisteredAddressOnlyWhenAddressAsked(t *testing.T) {
	for _, tc := range []struct {
		mode AddressMode
		want openid4vpverifier.Scope
	}{{AddressRequired, openid4vpverifier.ScopeProofingAddress}, {AddressOff, openid4vpverifier.ScopeProofing}} {
		f := discloseYiviAddress(t, tc.mode, brpClaims)
		if got := f.verifier.scopes[0]; got != tc.want {
			t.Errorf("address %s: scope = %v, want %v", tc.mode, got, tc.want)
		}
		if kept := f.requests.registeredAddress != nil; kept != tc.mode.asked() {
			t.Errorf("address %s: registered address kept = %v", tc.mode, kept)
		}
	}
}

// withClaim is brpClaims with claim set to value.
func withClaim(claim, value string) map[string]string {
	claims := maps.Clone(brpClaims)
	claims[claim] = value
	return claims
}

func TestAddAddressComparedWithRegistered(t *testing.T) {
	registered := Address{Street: "Kerkstraat", HouseNumber: "12 A", PostalCode: "6511AB", City: "Nijmegen", Country: "Nederland"}
	other := Address{Street: "Molenstraat", HouseNumber: "3", PostalCode: "6511 AB", City: "Nijmegen", Country: "NL"}
	cases := []struct {
		name       string
		lookup     *fakeAddressLookup
		registered map[string]string
		address    Address
		want       Address
		wantReview AddressReview
	}{
		{
			"same, the register written its way", &fakeAddressLookup{found: []addresslookup.Address{registerAddress}},
			withClaim(openid4vpverifier.ClaimAddressStreet, "Kerkstr"),
			Address{Street: "kerkstraat", HouseNumber: "12a", PostalCode: "6511 ab", City: "Nijmegen", Country: "NL"},
			Address{Street: "Kerkstraat", HouseNumber: "12A", PostalCode: "6511 AB", City: "Nijmegen", Country: registerCountry, Verification: AddressRegistered},
			"",
		},
		{
			"same, unchecked, made-up street", nil, brpClaims,
			Address{Street: "Nepstraat", HouseNumber: "12a", PostalCode: "6511 ab", City: "Nergenshuizen", Country: "NL"},
			Address{Street: "Kerkstraat", HouseNumber: "12 A", PostalCode: "6511AB", City: "Nijmegen", Country: registerCountry, Verification: AddressRegistered},
			"",
		},
		{
			"another address", nil, brpClaims, other,
			Address{
				Street: "Molenstraat", HouseNumber: "3", PostalCode: "6511 AB", City: "Nijmegen", Country: "NL",
				Verification: AddressRegisteredMismatch, Registered: &registered,
			},
			AddressReviewPending,
		},
		{
			"addition taken for the number", nil, withClaim(openid4vpverifier.ClaimAddressHouseNumber, "12-1"),
			Address{Street: "Kerkstraat", HouseNumber: "121", PostalCode: "6511AB", City: "Nijmegen", Country: "NL"},
			Address{
				Street: "Kerkstraat", HouseNumber: "121", PostalCode: "6511AB", City: "Nijmegen", Country: "NL",
				Verification: AddressRegisteredMismatch,
				Registered:   &Address{Street: "Kerkstraat", HouseNumber: "12-1", PostalCode: "6511AB", City: "Nijmegen", Country: "Nederland"},
			},
			AddressReviewPending,
		},
		{
			"letter missing on the card", &fakeAddressLookup{found: []addresslookup.Address{registerAddress}},
			withClaim(openid4vpverifier.ClaimAddressHouseNumber, "12"),
			Address{Street: "Kerkstraat", HouseNumber: "12A", PostalCode: "6511AB", City: "Nijmegen", Country: "NL"},
			Address{Street: "Kerkstraat", HouseNumber: "12A", PostalCode: "6511 AB", City: "Nijmegen", Country: registerCountry, Verification: AddressVerified},
			AddressReviewPending,
		},
		{
			"number written otherwise, unchecked", nil, brpClaims,
			Address{Street: "Kerkstraat", HouseNumber: "0012a", PostalCode: "6511AB", City: "Nijmegen", Country: "NL"},
			Address{Street: "Kerkstraat", HouseNumber: "0012a", PostalCode: "6511AB", City: "Nijmegen", Country: "NL", Verification: AddressUnchecked},
			AddressReviewPending,
		},
		{
			"no house number on the card", nil, withClaim(openid4vpverifier.ClaimAddressHouseNumber, ""), other,
			Address{Street: "Molenstraat", HouseNumber: "3", PostalCode: "6511 AB", City: "Nijmegen", Country: "NL", Verification: AddressUnchecked},
			AddressReviewPending,
		},
		{
			"not disclosed", nil, nil, other,
			Address{Street: "Molenstraat", HouseNumber: "3", PostalCode: "6511 AB", City: "Nijmegen", Country: "NL", Verification: AddressUnchecked},
			AddressReviewPending,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := discloseYiviAddress(t, AddressRequired, tc.registered)
			if tc.lookup != nil {
				f.svc.addressLookup = tc.lookup
			}
			got, err := f.svc.AddAddress(context.Background(), testOrg.ID, f.requests.stored.ID, nil, tc.address)
			if err != nil {
				t.Fatalf("AddAddress: %v", err)
			}
			if !reflect.DeepEqual(*got.SubjectAddress, tc.want) {
				t.Errorf("address = %+v, want %+v", *got.SubjectAddress, tc.want)
			}
			if got.Status != StatusApproved || got.ErrorCode != "" || got.AddressReview != tc.wantReview {
				t.Errorf("status = %s %q, address review %q; want approved, %q", got.Status, got.ErrorCode, got.AddressReview, tc.wantReview)
			}
			if f.requests.registeredAddress != nil {
				t.Error("registered address kept after the address was added")
			}
		})
	}
}

func TestSelfDeclaredAddressReviewed(t *testing.T) {
	ctx := context.Background()
	f := newAddressFixture(AddressRequired)
	req := sendApprovedAddress(t, f, time.Minute)
	got, err := f.svc.AddAddress(ctx, testOrg.ID, req.ID, nil, testAddress)
	if err != nil {
		t.Fatalf("AddAddress: %v", err)
	}
	if got.AddressReview != AddressReviewPending || got.Status != StatusApproved {
		t.Fatalf("after AddAddress = %s, %s; want approved, address pending", got.Status, got.AddressReview)
	}

	if _, err := f.svc.DecideAddressReview(ctx, testOrg.ID, req.ID, alex.UserID, ReviewScopeReviews, false, true, testReason); !errors.Is(err, ErrOwnReview) {
		t.Errorf("subject deciding = %v; want ErrOwnReview", err)
	}
	// The subject may when SelfReview allows it (a platform admin on a dev stack).
	decided, err := f.svc.DecideAddressReview(ctx, testOrg.ID, req.ID, alex.UserID, ReviewScopeReviews, true, false, testReason)
	if err != nil || decided.AddressReview != AddressReviewRejected || decided.Status != StatusApproved {
		t.Fatalf("DecideAddressReview = %s, %s, %v; want approved, address rejected", decided.Status, decided.AddressReview, err)
	}
	if _, err := f.svc.DecideAddressReview(ctx, testOrg.ID, req.ID, uuid.New(), ReviewScopeAll, false, true, testReason); !errors.Is(err, ErrAddressNotUnderReview) {
		t.Errorf("deciding again = %v; want ErrAddressNotUnderReview", err)
	}
}

func TestRegisteredAddressNotReviewed(t *testing.T) {
	ctx := context.Background()
	f := discloseYiviAddress(t, AddressRequired, brpClaims)
	registered := Address{Street: "Kerkstraat", HouseNumber: "12 A", PostalCode: "6511AB", City: "Nijmegen", Country: "NL"}
	got, err := f.svc.AddAddress(ctx, testOrg.ID, f.requests.stored.ID, nil, registered)
	if err != nil || got.AddressReview != "" {
		t.Fatalf("AddAddress = %q, %v; want no review", got.AddressReview, err)
	}
	if _, err := f.svc.DecideAddressReview(ctx, testOrg.ID, got.ID, uuid.New(), ReviewScopeAll, false, true, testReason); !errors.Is(err, ErrAddressNotUnderReview) {
		t.Errorf("DecideAddressReview = %v; want ErrAddressNotUnderReview", err)
	}
}

func TestAddressReviewNeedsReason(t *testing.T) {
	ctx := context.Background()
	f := newAddressFixture(AddressRequired)
	req := sendApprovedAddress(t, f, time.Minute)
	if _, err := f.svc.AddAddress(ctx, testOrg.ID, req.ID, nil, testAddress); err != nil {
		t.Fatalf("AddAddress: %v", err)
	}
	for _, reason := range []string{"", "  ", strings.Repeat("x", maxReviewReasonLength+1)} {
		if _, err := f.svc.DecideAddressReview(ctx, testOrg.ID, req.ID, uuid.New(), ReviewScopeAll, false, true, reason); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("reason %q: DecideAddressReview = %v; want ErrInvalidInput", reason, err)
		}
	}
}

func TestYiviDisclosureWithoutCardClearsRegistered(t *testing.T) {
	ctx := context.Background()
	f := newAddressFixture(AddressRequired)
	sent := f.sendYivi(t)
	if _, err := f.svc.StartYivi(ctx, testOrg.ID, sent.Request.ID, nil); err != nil {
		t.Fatalf("StartYivi: %v", err)
	}
	earlier := registeredAddress(brpClaims)
	f.requests.registeredAddress = &earlier
	f.verifier.presentation = &openid4vpverifier.Presentation{ByCredential: map[string]map[string]string{
		"idcard": {openid4vpverifier.ClaimGivenNames: "Anna"},
	}}
	if _, err := f.svc.YiviDisclosure(ctx, testOrg.ID, sent.Request.ID, nil); err != nil {
		t.Fatalf("YiviDisclosure: %v", err)
	}
	if f.requests.registeredAddress != nil {
		t.Errorf("registered address = %+v; want the earlier attempt's cleared", *f.requests.registeredAddress)
	}
}
