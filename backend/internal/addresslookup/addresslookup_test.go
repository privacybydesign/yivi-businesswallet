package addresslookup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
)

func TestPostalCode(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		dutch bool
	}{
		{"1015 CP", "1015CP", true},
		{" 1015cp ", "1015CP", true},
		{"0123 AB", "", false},
		{"1000", "", false},
		{"SW1A 1AA", "", false},
		{"10115", "", false},
		{"1015  CP", "", false},
	}
	for _, tc := range cases {
		got, dutch := PostalCode(tc.in)
		if got != tc.want || dutch != tc.dutch {
			t.Errorf("PostalCode(%q) = %q, %v; want %q, %v", tc.in, got, dutch, tc.want, tc.dutch)
		}
	}
}

func TestHouseNumber(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"12", 12, true},
		{"12A", 12, true},
		{" 1-2", 1, true},
		{"12 bis", 12, true},
		{"A12", 0, false},
		{"0", 0, false},
		{"100000", 0, false},
	}
	for _, tc := range cases {
		got, ok := HouseNumber(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("HouseNumber(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// pdokAnswer is a Locatieserver answer: one address, and one without a
// postal code that is left out.
const pdokAnswer = `{"response":{"numFound":2,"docs":[
	{"straatnaam":"Keizersgracht","huis_nlt":"12A","postcode":"1015CP","woonplaatsnaam":"Amsterdam"},
	{"straatnaam":"Keizersgracht","huis_nlt":"12","woonplaatsnaam":"Amsterdam"}
]}}`

var keizersgracht = Address{Street: "Keizersgracht", HouseNumber: "12A", PostalCode: "1015 CP", City: "Amsterdam"}

// newPDOK serves pdokAnswer and records each request's path and query.
func newPDOK(t *testing.T, status int) (*PDOK, *[]*url.URL) {
	t.Helper()
	var seen []*url.URL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL)
		w.WriteHeader(status)
		if _, err := w.Write([]byte(pdokAnswer)); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return NewPDOK(srv.URL+"/", srv.Client()), &seen
}

func TestFind(t *testing.T) {
	c, seen := newPDOK(t, http.StatusOK)
	got, err := c.Find(context.Background(), "1015CP", 12)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !slices.Equal(got, []Address{keizersgracht}) {
		t.Errorf("Find = %+v, want %+v", got, keizersgracht)
	}
	u := (*seen)[0]
	if want := []string{addressType, "postcode:1015CP", "huisnummer:12"}; u.Path != "/free" || !slices.Equal(u.Query()["fq"], want) {
		t.Errorf("asked %s, want /free with fq %q", u, want)
	}
}

func TestFindRefusesBadPostalCode(t *testing.T) {
	c, seen := newPDOK(t, http.StatusOK)
	if _, err := c.Find(context.Background(), "1015CP OR *:*", 12); err == nil {
		t.Fatal("Find took a postal code that is none")
	}
	if len(*seen) != 0 {
		t.Errorf("asked the register %d times, want never", len(*seen))
	}
}

func TestSearchFails(t *testing.T) {
	c, _ := newPDOK(t, http.StatusBadGateway)
	if _, err := c.Find(context.Background(), "1015CP", 12); err == nil {
		t.Error("Find on a 502 succeeded")
	}
	if err := c.Ping(context.Background()); err == nil {
		t.Error("Ping on a 502 succeeded")
	}
}
