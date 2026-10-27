package proofing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/addresslookup"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
)

// AddressMode is whether a flow has the address step: once their identity is
// approved, its subject must enter their postal address. The address is what
// the subject declares, not read off a document. Flows live in the engine,
// which knows nothing of it, so the wallet keeps this per org and flow.
type AddressMode string

const (
	AddressOff      AddressMode = "off"
	AddressRequired AddressMode = "required"
)

func (m AddressMode) valid() bool {
	return m == AddressOff || m == AddressRequired
}

// asked is the mode as read from a row; "" (a store-less service) is off.
func (m AddressMode) asked() bool { return m == AddressRequired }

const (
	// AddressWindow is how long after the identity was approved the subject
	// can still enter their address, on the page that ran the session.
	AddressWindow = time.Hour
	// maxAddressFieldRunes bounds each field of an address.
	maxAddressFieldRunes = 200
)

var (
	// ErrAddressNotAsked is an address on a request whose flow asks for none.
	ErrAddressNotAsked = errors.New("proofing: the request asks for no address")
	// ErrAddressClosed is an address before the identity was approved, once
	// AddressWindow passed, or after one was entered already.
	ErrAddressClosed = errors.New("proofing: the address can be entered once, within AddressWindow of an approved identity")
	// ErrAddressNeedsPage is an address flow sent where its subject gets no
	// page to enter it on: by mail, or as a bare deep link.
	ErrAddressNeedsPage = errors.New("proofing: a flow that asks for the address runs on a page: show it on screen or send a hosted link")
	// ErrAddressNotFound is an address in the Netherlands that the register
	// does not have: no such postal code or house number, or another street.
	ErrAddressNotFound = errors.New("proofing: the address is not in the Dutch address register")
	// ErrAddressLookupUnavailable is a lookup the register could not answer.
	ErrAddressLookupUnavailable = errors.New("proofing: the Dutch address register cannot be reached")
)

// addressFinder is the Dutch address register (addresslookup.PDOK).
type addressFinder interface {
	Find(ctx context.Context, postalCode string, houseNumber int) ([]addresslookup.Address, error)
}

// SetAddressLookup sets the register addresses are checked against and
// filled in from; call before serving. Without one, a Dutch address is kept
// AddressUnchecked and nothing is looked up.
func (s *Service) SetAddressLookup(l addressFinder) { s.addressLookup = l }

// AddressVerification is how an address was checked when it was entered.
type AddressVerification string

const (
	// AddressVerified is found in the Dutch address register, its fields
	// written as the register writes them.
	AddressVerified AddressVerification = "verified"
	// AddressUnchecked has a Dutch postal code, but the register could not be
	// asked.
	AddressUnchecked AddressVerification = "unchecked"
	// AddressUnsupported is outside the Netherlands, which has the only
	// register addresses are checked against.
	AddressUnsupported AddressVerification = "unsupported"
)

const (
	// addressLookupTimeout bounds one question to the register, so the page
	// never waits long on it.
	addressLookupTimeout = 5 * time.Second
	// registerCountry is the country a verified address is kept with, as the
	// register, which is Dutch, writes it.
	registerCountry = "Nederland"
)

// dutchCountries are the countries, as addressKey writes them, that name the
// Netherlands: its codes, and its names in the page's languages.
var dutchCountries = map[string]bool{
	"nl": true, "nld": true, "nederland": true, "netherlands": true, "thenetherlands": true, "holland": true,
}

// Address is the postal address a subject entered.
type Address struct {
	Street      string `json:"street"`
	HouseNumber string `json:"houseNumber"`
	PostalCode  string `json:"postalCode"`
	City        string `json:"city"`
	Country     string `json:"country"`
	// Verification is set when the address is kept; what a page sends is
	// ignored. An address kept before it was checked has none.
	Verification AddressVerification `json:"verification,omitempty"`
}

// normalized trims every field and refuses an address with one empty or too
// long.
func (a Address) normalized() (Address, error) {
	out := Address{
		Street: strings.TrimSpace(a.Street), HouseNumber: strings.TrimSpace(a.HouseNumber),
		PostalCode: strings.TrimSpace(a.PostalCode), City: strings.TrimSpace(a.City), Country: strings.TrimSpace(a.Country),
	}
	for _, f := range []struct{ name, value string }{
		{"street", out.Street},
		{"houseNumber", out.HouseNumber},
		{"postalCode", out.PostalCode},
		{"city", out.City},
		{"country", out.Country},
	} {
		if f.value == "" {
			return Address{}, fmt.Errorf("%w: %s is required", ErrInvalidInput, f.name)
		}
		if utf8.RuneCountInString(f.value) > maxAddressFieldRunes {
			return Address{}, fmt.Errorf("%w: %s is at most %d characters", ErrInvalidInput, f.name, maxAddressFieldRunes)
		}
	}
	return out, nil
}

// webhookAddressKey is where a session.address_added delivery carries the
// address; Purge drops it from the request's deliveries.
const webhookAddressKey = "address"

type flowAddressStore interface {
	Get(ctx context.Context, orgID uuid.UUID, flowID string) (AddressMode, error)
	All(ctx context.Context, orgID uuid.UUID) (map[string]AddressMode, error)
	Save(ctx context.Context, orgID uuid.UUID, flowID string, mode AddressMode) (AddressMode, error)
}

// FlowAddressStore persists each flow's AddressMode per org.
type FlowAddressStore struct {
	db    database.DB
	audit audit.Recorder
}

func NewFlowAddressStore(db database.DB, recorder audit.Recorder) *FlowAddressStore {
	return &FlowAddressStore{db: db, audit: recorder}
}

// Get returns the flow's mode; a flow never set is AddressOff.
func (s *FlowAddressStore) Get(ctx context.Context, orgID uuid.UUID, flowID string) (AddressMode, error) {
	return getFlowAddress(ctx, s.db, orgID, flowID)
}

func getFlowAddress(ctx context.Context, q database.Querier, orgID uuid.UUID, flowID string) (AddressMode, error) {
	var mode AddressMode
	err := q.QueryRow(ctx, `SELECT address FROM identity_proofing_flow_settings
		WHERE organization_id = $1 AND flow_id = $2`, orgID, flowID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return AddressOff, nil
	}
	if err != nil {
		return "", fmt.Errorf("proofing: read address setting flow %s: %w", flowID, err)
	}
	return mode, nil
}

// All returns every flow of the org that asks for the address; a flow missing
// is off.
func (s *FlowAddressStore) All(ctx context.Context, orgID uuid.UUID) (map[string]AddressMode, error) {
	rows, err := s.db.Query(ctx, `SELECT flow_id, address FROM identity_proofing_flow_settings
		WHERE organization_id = $1 AND address <> 'off'`, orgID)
	if err != nil {
		return nil, fmt.Errorf("proofing: list address settings org %s: %w", orgID, err)
	}

	out := map[string]AddressMode{}
	var flowID string
	var mode AddressMode
	if _, err := pgx.ForEachRow(rows, []any{&flowID, &mode}, func() error {
		out[flowID] = mode
		return nil
	}); err != nil {
		return nil, fmt.Errorf("proofing: list address settings org %s: %w", orgID, err)
	}
	return out, nil
}

// Save sets the flow's mode and audits identity_proofing.flow_address_configured
// with before and after. Saving what it already has changes nothing.
func (s *FlowAddressStore) Save(ctx context.Context, orgID uuid.UUID, flowID string, mode AddressMode) (AddressMode, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getFlowAddress(ctx, q, orgID, flowID)
		if err != nil {
			return err
		}
		if before == mode {
			return nil
		}
		if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_flow_settings (organization_id, flow_id, address)
			VALUES ($1, $2, $3)
			ON CONFLICT (organization_id, flow_id) DO UPDATE SET address = EXCLUDED.address, updated_at = now()`,
			orgID, flowID, string(mode)); err != nil {
			return fmt.Errorf("proofing: save address setting flow %s: %w", flowID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingFlowAddressConfigured,
			audit.Target{Type: audit.TargetIdentityProofingFlow, ID: flowID, OrgID: &orgID},
			audit.Updated(map[string]any{"address": string(before)}, map[string]any{"address": string(mode)}))
	})
	if err != nil {
		return "", err
	}
	return mode, nil
}

// AddAddress seals addr on req, audited identity_proofing.address_added
// (without the address), and sends session.address_added. It is false, and
// changes nothing, when the request holds an address already or was erased.
func (s *RequestStore) AddAddress(ctx context.Context, req Request, addr Address) (bool, error) {
	if s.cipher == nil {
		return false, ErrNoEncryptionKey
	}
	plain, err := json.Marshal(addr)
	if err != nil {
		return false, fmt.Errorf("proofing: marshal address request %s: %w", req.ID, err)
	}

	sealed, err := s.cipher.Encrypt(plain)
	if err != nil {
		return false, fmt.Errorf("proofing: encrypt address request %s: %w", req.ID, err)
	}

	added := false
	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		tag, err := q.Exec(ctx, `UPDATE identity_proofing_requests
			SET address_ciphertext = $2, address_added_at = now(), registered_address_ciphertext = NULL, updated_at = now()
			WHERE id = $1 AND purged_at IS NULL AND address_ciphertext IS NULL`, req.ID, sealed)
		if err != nil {
			return fmt.Errorf("proofing: add address request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		added = true

		if err := s.audit.Record(ctx, q, audit.IdentityProofingAddressAdded,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Created(req.auditFields(map[string]any{}))); err != nil {
			return err
		}

		data := sessionEventData(req, req.Status)
		data[webhookAddressKey] = addr
		return enqueueRequestWebhook(ctx, q, req, EventSessionAddressAdded, data)
	})
	if err != nil {
		return false, err
	}
	return added, nil
}

// SetRegisteredAddress seals the registered address a Yivi disclosure carried
// on req, until AddAddress keeps the entered one. It changes
// nothing once an address was entered or the request was erased.
func (s *RequestStore) SetRegisteredAddress(ctx context.Context, req Request, addr Address) error {
	if s.cipher == nil {
		return ErrNoEncryptionKey
	}
	plain, err := json.Marshal(addr)
	if err != nil {
		return fmt.Errorf("proofing: marshal registered address request %s: %w", req.ID, err)
	}
	sealed, err := s.cipher.Encrypt(plain)
	if err != nil {
		return fmt.Errorf("proofing: encrypt registered address request %s: %w", req.ID, err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE identity_proofing_requests
		SET registered_address_ciphertext = $2, updated_at = now()
		WHERE id = $1 AND purged_at IS NULL AND address_ciphertext IS NULL`, req.ID, sealed); err != nil {
		return fmt.Errorf("proofing: set registered address request %s: %w", req.ID, err)
	}
	return nil
}

// RegisteredAddress is the registered address SetRegisteredAddress kept on
// the request; nil when it has none.
func (s *RequestStore) RegisteredAddress(ctx context.Context, id uuid.UUID) (*Address, error) {
	var sealed []byte
	err := s.db.QueryRow(ctx, `SELECT registered_address_ciphertext FROM identity_proofing_requests
		WHERE id = $1`, id).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && sealed == nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("proofing: read registered address request %s: %w", id, err)
	}
	return s.decryptAddress(id, sealed)
}

// decryptAddress opens an address sealed by AddAddress or SetRegisteredAddress.
func (s *RequestStore) decryptAddress(id uuid.UUID, sealed []byte) (*Address, error) {
	if s.cipher == nil {
		return nil, ErrNoEncryptionKey
	}
	plain, err := s.cipher.Decrypt(sealed)
	if err != nil {
		return nil, fmt.Errorf("proofing: decrypt address request %s: %w", id, err)
	}
	var addr Address
	if err := json.Unmarshal(plain, &addr); err != nil {
		return nil, fmt.Errorf("proofing: unmarshal address request %s: %w", id, err)
	}
	return &addr, nil
}

// flowAddress is the flow's mode; a service without a store asks for none.
func (s *Service) flowAddress(ctx context.Context, orgID uuid.UUID, flowID string) (AddressMode, error) {
	if s.flowAddressSettings == nil {
		return AddressOff, nil
	}
	return s.flowAddressSettings.Get(ctx, orgID, flowID)
}

// allFlowAddresses is every flow's mode that was set; a missing flow is off.
func (s *Service) allFlowAddresses(ctx context.Context, orgID uuid.UUID) (map[string]AddressMode, error) {
	if s.flowAddressSettings == nil {
		return map[string]AddressMode{}, nil
	}
	return s.flowAddressSettings.All(ctx, orgID)
}

// FlowAddress returns one of the org's flows' AddressMode.
func (s *Service) FlowAddress(ctx context.Context, org Org, flowID string) (AddressMode, error) {
	if err := s.orgHasFlow(ctx, org, flowID); err != nil {
		return "", err
	}
	return s.flowAddress(ctx, org.ID, flowID)
}

// SaveFlowAddress sets one of the org's flows' AddressMode, not a data request
// flow's. Requests already sent keep the mode they were sent with.
func (s *Service) SaveFlowAddress(ctx context.Context, org Org, flowID string, mode AddressMode) (AddressMode, error) {
	if s.flowAddressSettings == nil {
		return "", errors.New("proofing: no address settings store configured")
	}
	if !mode.valid() {
		return "", fmt.Errorf("%w: address is off or required", ErrInvalidInput)
	}
	if err := s.orgHasFlow(ctx, org, flowID); err != nil {
		return "", err
	}
	if err := s.refuseDataRequestFlow(ctx, org, flowID); err != nil {
		return "", err
	}
	return s.flowAddressSettings.Save(ctx, org.ID, flowID, mode)
}

// AddAddress keeps the address entered on the on-screen page of a request the
// caller may reach (requestedBy as in Request).
func (s *Service) AddAddress(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID, addr Address) (Request, error) {
	req, err := s.sentRequest(ctx, orgID, id, requestedBy)
	if err != nil {
		return Request{}, err
	}
	return s.addAddress(ctx, req, addr)
}

// HostedAddAddress is AddAddress for a hosted request, by its subject.
func (s *Service) HostedAddAddress(ctx context.Context, token string, addr Address) (Request, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return Request{}, err
	}
	return s.addAddress(audit.ContextWithActor(ctx, hostedSubjectActor), req, addr)
}

// addressOpen reports whether req takes an address now: it asks for one, holds
// none yet, is approved, not erased or cancelled, and within AddressWindow.
func (s *Service) addressOpen(req Request) error {
	if !req.Address.asked() {
		return ErrAddressNotAsked
	}
	if req.PurgedAt != nil || req.CancelledAt != nil {
		return ErrSessionOver
	}
	now := s.now()
	if req.SubjectAddress != nil || req.EffectiveStatus(now) != StatusApproved || req.CompletedAt == nil ||
		now.Sub(*req.CompletedAt) > AddressWindow {
		return ErrAddressClosed
	}
	return nil
}

func (s *Service) addAddress(ctx context.Context, req Request, addr Address) (Request, error) {
	if err := s.addressOpen(req); err != nil {
		return Request{}, err
	}

	addr, err := addr.normalized()
	if err != nil {
		return Request{}, err
	}

	addr, err = s.verifyAddress(ctx, addr)
	if err != nil {
		return Request{}, err
	}

	added, err := s.requests.AddAddress(ctx, req, addr)
	if err != nil {
		return Request{}, err
	}
	// Lost to a concurrent entry, or to a purge.
	if !added {
		return Request{}, ErrAddressClosed
	}

	now := s.now()
	req.SubjectAddress, req.AddressAddedAt = &addr, &now
	return req, nil
}

// verifyAddress checks a normalized address in the Netherlands, by its country
// or its postal code, against the register: one it has is kept as the register
// writes it, one it lacks is ErrAddressNotFound. The street must match, so a
// mistyped postal code that happens to exist is not taken for another address.
// Any other address is kept as entered.
func (s *Service) verifyAddress(ctx context.Context, addr Address) (Address, error) {
	postalCode, dutchPostalCode := addresslookup.PostalCode(addr.PostalCode)
	if !dutchPostalCode {
		if dutchCountries[addressKey(addr.Country)] {
			return Address{}, fmt.Errorf("%w: a Dutch postal code is four digits and two letters", ErrAddressNotFound)
		}
		addr.Verification = AddressUnsupported
		return addr, nil
	}

	number, ok := addresslookup.HouseNumber(addr.HouseNumber)
	if !ok {
		return Address{}, fmt.Errorf("%w: the house number must start with a number", ErrAddressNotFound)
	}

	if s.addressLookup == nil {
		addr.Verification = AddressUnchecked
		return addr, nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, addressLookupTimeout)
	defer cancel()
	found, err := s.addressLookup.Find(lookupCtx, postalCode, number)
	if err != nil {
		slog.WarnContext(ctx, "proofing: address kept unchecked, the register could not be asked", slog.String("error", err.Error()))
		addr.Verification = AddressUnchecked
		return addr, nil
	}

	for _, f := range found {
		if addressKey(f.HouseNumber) == addressKey(addr.HouseNumber) && addressKey(f.Street) == addressKey(addr.Street) {
			addr.Street, addr.HouseNumber, addr.PostalCode, addr.City, addr.Country = f.Street, f.HouseNumber, f.PostalCode, f.City, registerCountry
			addr.Verification = AddressVerified
			return addr, nil
		}
	}
	return Address{}, ErrAddressNotFound
}

// registeredAddress is the registered address a Yivi disclosure carried, as
// claims of pbdf.gemeente.address.
func registeredAddress(claims map[string]string) Address {
	return Address{
		Street: claims[openid4vpverifier.ClaimAddressStreet], HouseNumber: claims[openid4vpverifier.ClaimAddressHouseNumber],
		PostalCode: claims[openid4vpverifier.ClaimAddressPostalCode], City: claims[openid4vpverifier.ClaimAddressCity],
		Country: registerCountry,
	}
}

// addressKey is a street or house number as compared with the register's:
// case, spaces and punctuation left out, so "12 a" is "12A" and "1-2" is "1 2".
func addressKey(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

// LookUpAddress returns the register's addresses at the postal code and house
// number typed on the on-screen page of a request the caller may reach
// (requestedBy as in Request), while it takes an address, so the page can
// fill in the rest.
func (s *Service) LookUpAddress(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID, postalCode, houseNumber string) ([]addresslookup.Address, error) {
	req, err := s.sentRequest(ctx, orgID, id, requestedBy)
	if err != nil {
		return nil, err
	}
	return s.lookUpAddress(ctx, req, postalCode, houseNumber)
}

// HostedLookUpAddress is LookUpAddress for a hosted request.
func (s *Service) HostedLookUpAddress(ctx context.Context, token, postalCode, houseNumber string) ([]addresslookup.Address, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return nil, err
	}
	return s.lookUpAddress(ctx, req, postalCode, houseNumber)
}

// lookUpAddress asks the register only for a request that takes an address
// now, so the route is no open way into it. A postal code that is not Dutch,
// or a house number without a number, finds nothing without asking.
func (s *Service) lookUpAddress(ctx context.Context, req Request, postalCode, houseNumber string) ([]addresslookup.Address, error) {
	if err := s.addressOpen(req); err != nil {
		return nil, err
	}

	for _, f := range []struct{ name, value string }{{"postalCode", postalCode}, {"houseNumber", houseNumber}} {
		if utf8.RuneCountInString(f.value) > maxAddressFieldRunes {
			return nil, fmt.Errorf("%w: %s is at most %d characters", ErrInvalidInput, f.name, maxAddressFieldRunes)
		}
	}
	compact, dutch := addresslookup.PostalCode(postalCode)
	number, ok := addresslookup.HouseNumber(houseNumber)
	if !dutch || !ok {
		return []addresslookup.Address{}, nil
	}

	if s.addressLookup == nil {
		return nil, ErrAddressLookupUnavailable
	}

	lookupCtx, cancel := context.WithTimeout(ctx, addressLookupTimeout)
	defer cancel()
	found, err := s.addressLookup.Find(lookupCtx, compact, number)
	if err != nil {
		slog.WarnContext(ctx, "proofing: no address looked up, the register could not be asked", slog.String("error", err.Error()))
		return nil, ErrAddressLookupUnavailable
	}
	return found, nil
}
