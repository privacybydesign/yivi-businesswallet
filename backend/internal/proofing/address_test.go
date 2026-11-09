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
			checkRegisterAsked(t, tc.lookup, tc.query)
		})
	}
}

// checkRegisterAsked checks that the register was asked query once, or not at
// all for "".
func checkRegisterAsked(t *testing.T, lookup *fakeAddressLookup, query string) {
	t.Helper()
	if query == "" && len(lookup.queries) != 0 || query != "" && (len(lookup.queries) != 1 || lookup.queries[0] != query) {
		t.Errorf("register asked %q, want %q", lookup.queries, query)
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
	for _, mode := range []AddressMode{AddressRequired, AddressOff} {
		f := discloseYiviAddress(t, mode, brpClaims)
		if got := f.verifier.requests[0].Address; got != mode.asked() {
			t.Errorf("address %s: asks the registered address = %v", mode, got)
		}
		if kept := f.requests.stored.SubjectAddress != nil; kept != mode.asked() {
			t.Errorf("address %s: registered address kept as the address = %v", mode, kept)
		}
	}
}

// A request that asks the address requires the registered address in the
// Yivi disclosure, and refuses one without it.
func TestYiviDisclosureWithoutRegisteredAddressRefused(t *testing.T) {
	f := newAddressFixture(AddressRequired)
	ctx := context.Background()
	sent := f.sendYivi(t)
	if _, err := f.svc.StartYivi(ctx, testOrg.ID, sent.Request.ID, nil); err != nil {
		t.Fatalf("StartYivi: %v", err)
	}
	f.verifier.presentation = &openid4vpverifier.Presentation{ByCredential: map[string]map[string]string{
		"idcard": {openid4vpverifier.ClaimGivenNames: "Anna"},
	}}
	if _, err := f.svc.YiviDisclosure(ctx, testOrg.ID, sent.Request.ID, nil); err == nil {
		t.Fatal("YiviDisclosure accepted a disclosure without the registered address")
	}
	if f.requests.registeredAddress != nil {
		t.Error("a registered address was kept")
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAddressFixture(AddressRequired)
			if tc.lookup != nil {
				f.svc.addressLookup = tc.lookup
			}
			entered, err := tc.address.normalized()
			if err != nil {
				t.Fatal(err)
			}
			if entered, err = f.svc.verifyAddress(context.Background(), entered); err != nil {
				t.Fatalf("verifyAddress: %v", err)
			}
			got := compareRegistered(entered, registeredAddress(tc.registered))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("address = %+v, want %+v", got, tc.want)
			}
			var review AddressReview
			if r := addressReviewOf(got, AddressRequired); r != nil {
				review = *r
			}
			if review != tc.wantReview {
				t.Errorf("address review %q, want %q", review, tc.wantReview)
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

	if _, err := f.svc.DecideAddressReview(ctx, testOrg.ID, req.ID, StepReviewInput{Confirm: true, Reason: testReason, ReviewerID: alex.UserID, Scope: ReviewScopeReviews, OwnAllowed: false}); !errors.Is(err, ErrOwnReview) {
		t.Errorf("subject deciding = %v; want ErrOwnReview", err)
	}
	// The subject may when SelfReview allows it (a platform admin on a dev stack).
	decided, err := f.svc.DecideAddressReview(ctx, testOrg.ID, req.ID, StepReviewInput{Confirm: false, Reason: testReason, ReviewerID: alex.UserID, Scope: ReviewScopeReviews, OwnAllowed: true})
	if err != nil || decided.AddressReview != AddressReviewRejected || decided.Status != StatusApproved {
		t.Fatalf("DecideAddressReview = %s, %s, %v; want approved, address rejected", decided.Status, decided.AddressReview, err)
	}
	if _, err := f.svc.DecideAddressReview(ctx, testOrg.ID, req.ID, StepReviewInput{Confirm: true, Reason: testReason, ReviewerID: uuid.New(), Scope: ReviewScopeAll, OwnAllowed: false}); !errors.Is(err, ErrAddressNotUnderReview) {
		t.Errorf("deciding again = %v; want ErrAddressNotUnderReview", err)
	}
}

// The Yivi app's registered address is the person's address: the page asks
// none, and no reviewer confirms it.
func TestYiviRegisteredAddressKept(t *testing.T) {
	ctx := context.Background()
	f := discloseYiviAddress(t, AddressRequired, brpClaims)
	want := Address{Street: "Kerkstraat", HouseNumber: "12 A", PostalCode: "6511AB", City: "Nijmegen", Country: registerCountry, Verification: AddressRegistered}
	got := f.requests.stored
	if got.SubjectAddress == nil || !reflect.DeepEqual(*got.SubjectAddress, want) || got.AddressReview != "" {
		t.Fatalf("address = %+v, review %q; want the registered one, no review", got.SubjectAddress, got.AddressReview)
	}
	if addressUntil(*got) != nil {
		t.Error("the page still asks the address")
	}
	if _, err := f.svc.AddAddress(ctx, testOrg.ID, got.ID, nil, testAddress); !errors.Is(err, ErrAddressClosed) {
		t.Errorf("AddAddress after = %v; want ErrAddressClosed", err)
	}
	if _, err := f.svc.DecideAddressReview(ctx, testOrg.ID, got.ID, StepReviewInput{Confirm: true, Reason: testReason, ReviewerID: uuid.New(), Scope: ReviewScopeAll, OwnAllowed: false}); !errors.Is(err, ErrAddressNotUnderReview) {
		t.Errorf("DecideAddressReview = %v; want ErrAddressNotUnderReview", err)
	}
}

// A card without a house number names no one address: the page asks it.
func TestYiviRegisteredAddressWithoutNumberAsked(t *testing.T) {
	f := discloseYiviAddress(t, AddressRequired, withClaim(openid4vpverifier.ClaimAddressHouseNumber, ""))
	if f.requests.stored.SubjectAddress != nil {
		t.Fatalf("address kept = %+v; want none", f.requests.stored.SubjectAddress)
	}
	if addressUntil(*f.requests.stored) == nil {
		t.Error("the page does not ask the address")
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
		if _, err := f.svc.DecideAddressReview(ctx, testOrg.ID, req.ID, StepReviewInput{Confirm: true, Reason: reason, ReviewerID: uuid.New(), Scope: ReviewScopeAll, OwnAllowed: false}); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("reason %q: DecideAddressReview = %v; want ErrInvalidInput", reason, err)
		}
	}
}

// A flow that reviews every address sends the registered one to a reviewer
// too; otherwise only an address no register confirmed waits.
func TestAddressReviewOf(t *testing.T) {
	for _, tc := range []struct {
		verification AddressVerification
		mode         AddressMode
		want         bool
	}{
		{AddressRegistered, AddressRequired, false},
		{AddressRegistered, AddressAlwaysReview, true},
		{AddressVerified, AddressRequired, true},
		{AddressRegisteredMismatch, AddressAlwaysReview, true},
	} {
		got := addressReviewOf(Address{Verification: tc.verification}, tc.mode) != nil
		if got != tc.want {
			t.Errorf("%s under %s: reviewed = %v, want %v", tc.verification, tc.mode, got, tc.want)
		}
	}
}

func TestYiviRegisteredAddressAlwaysReviewed(t *testing.T) {
	f := discloseYiviAddress(t, AddressAlwaysReview, brpClaims)
	got := f.requests.stored
	if got.SubjectAddress == nil || got.AddressReview != AddressReviewPending || got.Status != StatusApproved {
		t.Fatalf("address %+v, review %q, %s; want the registered one kept, pending, approved", got.SubjectAddress, got.AddressReview, got.Status)
	}
	if steps := got.Steps(time.Now()); len(steps) != 1 || steps[0].State != StepInReview {
		t.Errorf("steps = %+v, want the address in review", steps)
	}
}

// The subject's page never learns that a reviewer looks at every address or
// statement.
func TestStepModesForSubject(t *testing.T) {
	if got := AddressAlwaysReview.forSubject(); got != AddressRequired {
		t.Errorf("address always_review for the subject = %q, want required", got)
	}
	if got := EmploymentAlwaysReview.forSubject(); got != EmploymentRequired {
		t.Errorf("employment always_review for the subject = %q, want required", got)
	}
	if got := AddressOff.forSubject(); got != AddressOff {
		t.Errorf("address off for the subject = %q, want off", got)
	}
}
