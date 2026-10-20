// Package addresslookup looks Dutch addresses up in the BAG (Basisregistratie
// Adressen en Gebouwen), the government's register of every address in the
// Netherlands, through PDOK's Locatieserver: open, keyless, and the register
// itself rather than a copy of it.
package addresslookup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	// suggestRows is how many addresses one suggestion answer carries.
	suggestRows = 6
	// findRows bounds the addresses at one postal code and house number: every
	// letter and addition of it, far fewer in practice.
	findRows = 100
	// maxResponseBytes caps an answer; findRows addresses fit many times over.
	maxResponseBytes = 1 << 20
	// addressType limits a search to addresses, not streets or places.
	addressType = "type:adres"
	// addressFields are the fields an address is read from.
	addressFields = "straatnaam,huis_nlt,postcode,woonplaatsnaam"
	// maxHouseNumber is the highest house number the BAG gives.
	maxHouseNumber = 99999
	// postalCodeDigits is how many digits a postal code starts with.
	postalCodeDigits = 4
)

var (
	// postalCodePattern is a Dutch postal code: four digits, the first not a
	// zero, and two letters. No other country writes its codes this way.
	postalCodePattern = regexp.MustCompile(`^([1-9][0-9]{3}) ?([A-Za-z]{2})$`)
	// houseNumberPattern is a house number with any letter or addition after it.
	houseNumberPattern = regexp.MustCompile(`^([0-9]+)`)
)

// Address is one address as the BAG writes it.
type Address struct {
	Street string `json:"street"`
	// HouseNumber carries its letter and addition, as "12A" or "1-2".
	HouseNumber string `json:"houseNumber"`
	// PostalCode is written "1015 CP".
	PostalCode string `json:"postalCode"`
	City       string `json:"city"`
}

// PostalCode returns a Dutch postal code compact and upper case ("1015CP"),
// and whether s is one.
func PostalCode(s string) (string, bool) {
	m := postalCodePattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	return m[1] + strings.ToUpper(m[2]), true
}

// HouseNumber returns the number a house number starts with, and whether it
// starts with one the BAG can give.
func HouseNumber(s string) (int, bool) {
	m := houseNumberPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 || n > maxHouseNumber {
		return 0, false
	}
	return n, true
}

// PDOK is the Locatieserver client.
type PDOK struct {
	baseURL string
	http    *http.Client
}

// NewPDOK builds a client of the Locatieserver search API at baseURL.
func NewPDOK(baseURL string, httpClient *http.Client) *PDOK {
	return &PDOK{baseURL: strings.TrimSuffix(baseURL, "/"), http: httpClient}
}

// Ping is the boot check: one search that asks for no rows.
func (c *PDOK) Ping(ctx context.Context) error {
	_, err := c.search(ctx, "free", url.Values{"q": {"*:*"}, "fq": {addressType}, "rows": {"0"}})
	return err
}

// Suggest returns the addresses that best match what someone typed so far;
// one without a postal code is left out.
func (c *PDOK) Suggest(ctx context.Context, query string) ([]Address, error) {
	return c.search(ctx, "suggest", url.Values{
		"q": {query}, "fq": {addressType}, "fl": {addressFields}, "rows": {strconv.Itoa(suggestRows)},
	})
}

// Find returns every address at a postal code (as PostalCode returns it) and
// house number, with each letter and addition the number has.
func (c *PDOK) Find(ctx context.Context, postalCode string, houseNumber int) ([]Address, error) {
	if _, ok := PostalCode(postalCode); !ok {
		return nil, fmt.Errorf("addresslookup: %q is not a Dutch postal code", postalCode)
	}
	return c.search(ctx, "free", url.Values{
		"q":  {"*:*"},
		"fq": {addressType, "postcode:" + postalCode, "huisnummer:" + strconv.Itoa(houseNumber)},
		"fl": {addressFields}, "rows": {strconv.Itoa(findRows)},
	})
}

type searchAnswer struct {
	Response struct {
		Docs []struct {
			Street      string `json:"straatnaam"`
			HouseNumber string `json:"huis_nlt"`
			PostalCode  string `json:"postcode"`
			City        string `json:"woonplaatsnaam"`
		} `json:"docs"`
	} `json:"response"`
}

func (c *PDOK) search(ctx context.Context, endpoint string, params url.Values) ([]Address, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("addresslookup: build %s request: %w", endpoint, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("addresslookup: %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("addresslookup: %s: unexpected status %d", endpoint, resp.StatusCode)
	}
	var answer searchAnswer
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&answer); err != nil {
		return nil, fmt.Errorf("addresslookup: decode %s answer: %w", endpoint, err)
	}
	out := make([]Address, 0, len(answer.Response.Docs))
	for _, d := range answer.Response.Docs {
		compact, ok := PostalCode(d.PostalCode)
		if !ok {
			continue
		}
		out = append(out, Address{
			Street: d.Street, HouseNumber: d.HouseNumber,
			PostalCode: compact[:postalCodeDigits] + " " + compact[postalCodeDigits:], City: d.City,
		})
	}
	return out, nil
}
