package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// stubStore stands in for *Store: the routes under test are pure request/response
// plumbing plus validation, so they are exercised without a database.
type stubStore struct {
	settings  Settings
	overrides map[string]TemplateOverride
	saved     []TemplateOverride
	deleted   []string
	saveErr   error
}

func newStubStore() *stubStore {
	return &stubStore{overrides: map[string]TemplateOverride{}}
}

func (s *stubStore) GetSettings(context.Context, uuid.UUID) (Settings, error) {
	return s.settings, nil
}

func (s *stubStore) Upsert(context.Context, uuid.UUID, SettingsInput) (Settings, error) {
	return s.settings, nil
}

func (s *stubStore) ListTemplates(context.Context, uuid.UUID) ([]TemplateOverride, error) {
	out := make([]TemplateOverride, 0, len(s.overrides))
	for _, kind := range Kinds() {
		for _, locale := range Locales() {
			if record, ok := s.overrides[templateTargetID(kind, locale)]; ok {
				out = append(out, record)
			}
		}
	}
	return out, nil
}

func (s *stubStore) GetTemplate(_ context.Context, _ uuid.UUID, kind Kind, locale Locale) (TemplateOverride, bool, error) {
	record, ok := s.overrides[templateTargetID(kind, locale)]
	return record, ok, nil
}

func (s *stubStore) SaveTemplate(_ context.Context, _ uuid.UUID, kind Kind, locale Locale, tpl Template) (TemplateOverride, error) {
	if s.saveErr != nil {
		return TemplateOverride{}, s.saveErr
	}
	if err := ValidateTemplate(kind, tpl); err != nil {
		return TemplateOverride{}, &InvalidTemplateError{Reason: err}
	}
	record := TemplateOverride{Kind: kind, Locale: locale, Template: tpl, UpdatedAt: time.Unix(0, 0).UTC()}
	s.overrides[templateTargetID(kind, locale)] = record
	s.saved = append(s.saved, record)
	return record, nil
}

func (s *stubStore) DeleteTemplate(_ context.Context, _ uuid.UUID, kind Kind, locale Locale) (bool, error) {
	key := templateTargetID(kind, locale)
	if _, ok := s.overrides[key]; !ok {
		return false, nil
	}
	delete(s.overrides, key)
	s.deleted = append(s.deleted, key)
	return true, nil
}

type stubMailService struct {
	body      Body
	err       error
	specimens []specimen
	previewed *Template
}

type specimen struct {
	kind   Kind
	locale Locale
	to     string
}

func (s *stubMailService) SendSpecimen(_ context.Context, _ uuid.UUID, kind Kind, locale Locale, to, _ string) error {
	if s.err != nil {
		return s.err
	}
	s.specimens = append(s.specimens, specimen{kind: kind, locale: locale, to: to})
	return nil
}

func (s *stubMailService) Preview(_ context.Context, _ uuid.UUID, kind Kind, locale Locale, tpl *Template, orgName string) (Body, error) {
	s.previewed = tpl
	if s.err != nil {
		return Body{}, s.err
	}
	if s.body.Subject != "" {
		return s.body, nil
	}
	// Render for real so a preview test still exercises the renderer's rules.
	resolved, _ := DefaultTemplate(kind, locale)
	if tpl != nil {
		resolved = *tpl
	}
	vars, _ := SampleVariables(kind, locale, orgName)
	body, err := Render(kind, locale, resolved, resolveBrand(Seeds{}), vars)
	if err != nil {
		return Body{}, &InvalidTemplateError{Reason: err}
	}
	return body, nil
}

// serve routes one request through the handler with the auth middleware bypassed
// and an admin org in context, the way the real middleware leaves it. The gating
// itself is organization's and covered there; these tests are about the template
// routes.
func serve(t *testing.T, store orgMailStore, service mailService, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	handler := NewHandler(store, service, passthrough, passthrough)
	mux := http.NewServeMux()
	handler.Register(mux)

	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, target, reader)
	req = req.WithContext(adminContext(req.Context()))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func adminContext(ctx context.Context) context.Context {
	ctx = organization.ContextWithOrg(ctx, organization.Organization{
		ID: uuid.New(), Slug: "acme", Name: "Acme BV",
	})
	return organization.ContextWithRole(ctx, organization.RoleAdmin)
}

func passthrough(next http.Handler) http.Handler { return next }

func decodeInto[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return out
}

// The list is the whole kind × locale matrix, so the editor can show every cause
// and which of them a tenant has taken over.
func TestListTemplatesCoversEveryKindAndLocale(t *testing.T) {
	store := newStubStore()
	store.overrides[templateTargetID(KindSMTPTest, LocaleNL)] = TemplateOverride{
		Kind: KindSMTPTest, Locale: LocaleNL,
		Template:  Template{Subject: "Onze eigen test"},
		UpdatedAt: time.Unix(0, 0).UTC(),
	}

	rec := serve(t, store, &stubMailService{}, http.MethodGet, "/orgs/acme/email/templates", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeInto[templateListResponse](t, rec)
	if len(got.Kinds) != len(Kinds()) {
		t.Fatalf("got %d kinds, want %d", len(got.Kinds), len(Kinds()))
	}
	for _, kind := range got.Kinds {
		if len(kind.Locales) != len(Locales()) {
			t.Errorf("kind %q covers %d locales, want %d", kind.Kind, len(kind.Locales), len(Locales()))
		}
		if len(kind.Variables) == 0 {
			t.Errorf("kind %q lists no variables; the editor's palette would be empty", kind.Kind)
		}
		for _, cell := range kind.Locales {
			if cell.Subject == "" {
				t.Errorf("kind %q locale %q has no subject", kind.Kind, cell.Locale)
			}
			customized := kind.Kind == string(KindSMTPTest) && cell.Locale == string(LocaleNL)
			if cell.Customized != customized {
				t.Errorf("kind %q locale %q: customized = %v, want %v", kind.Kind, cell.Locale, cell.Customized, customized)
			}
			if cell.Customized && cell.Subject != "Onze eigen test" {
				t.Errorf("customized cell carries %q, want the org's own subject", cell.Subject)
			}
		}
	}
}

// An uncustomised template still has to come back, filled with the shipped
// default, or the editor would open on an empty form.
func TestGetTemplateReturnsTheShippedDefaultUntilCustomized(t *testing.T) {
	rec := serve(t, newStubStore(), &stubMailService{}, http.MethodGet, "/orgs/acme/email/templates/invitation/en", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeInto[templateResponse](t, rec)
	shipped, _ := DefaultTemplate(KindInvitation, LocaleEN)
	if got.Customized {
		t.Error("customized = true for an org that has not edited this template")
	}
	if got.Template.Subject != shipped.Subject || got.Default.Subject != shipped.Subject {
		t.Errorf("template = %+v, want the shipped default", got.Template)
	}
	if got.UpdatedAt != nil {
		t.Errorf("updatedAt = %v, want none for an uncustomized template", got.UpdatedAt)
	}
}

func TestPutTemplateSavesTrimmedProseAndDropsBlankParagraphs(t *testing.T) {
	store := newStubStore()
	rec := serve(t, store, &stubMailService{}, http.MethodPut, "/orgs/acme/email/templates/invitation/en", Template{
		Subject:    "  Join {{orgName}}  ",
		Headline:   " Welcome ",
		Paragraphs: []string{" We would like you on board. ", "   ", ""},
		CTALabel:   "Accept",
		CTAURL:     "{{acceptUrl}}",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved %d templates, want 1", len(store.saved))
	}
	saved := store.saved[0].Template
	if saved.Subject != "Join {{orgName}}" || saved.Headline != "Welcome" {
		t.Errorf("saved %+v, want trimmed prose", saved)
	}
	if len(saved.Paragraphs) != 1 || saved.Paragraphs[0] != "We would like you on board." {
		t.Errorf("paragraphs = %q, want the one non-blank paragraph", saved.Paragraphs)
	}
	got := decodeInto[templateResponse](t, rec)
	if !got.Customized || got.UpdatedAt == nil {
		t.Errorf("response = %+v, want a customized template with an updatedAt", got)
	}
}

// A tenant's typo must come back as a 400 naming the field, because the editor
// shows that message beside the input.
func TestPutTemplateRejectsAnUnknownPlaceholderWith400(t *testing.T) {
	store := newStubStore()
	rec := serve(t, store, &stubMailService{}, http.MethodPut, "/orgs/acme/email/templates/invitation/en", Template{
		Subject:  "Join {{orgName}}",
		Headline: "Hello {{recipientName}}",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "invalid_template") || !strings.Contains(body, "recipientName") {
		t.Errorf("body = %s, want the offending placeholder named", body)
	}
	if len(store.saved) != 0 {
		t.Error("an invalid template was saved")
	}
}

// The call-to-action is the one field that reaches an href, so a literal
// javascript: URL has to be refused at save time, not at send time.
func TestPutTemplateRejectsAnUnsafeCallToActionURL(t *testing.T) {
	store := newStubStore()
	rec := serve(t, store, &stubMailService{}, http.MethodPut, "/orgs/acme/email/templates/invitation/en", Template{
		Subject:  "Join {{orgName}}",
		Headline: "Welcome",
		CTALabel: "Accept",
		CTAURL:   "javascript:alert(1)",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if len(store.saved) != 0 {
		t.Error("a template with a javascript: call to action was saved")
	}
}

func TestPutTemplateRejectsTooManyParagraphs(t *testing.T) {
	paragraphs := make([]string, maxParagraphs+1)
	for i := range paragraphs {
		paragraphs[i] = "Something to say."
	}
	store := newStubStore()
	rec := serve(t, store, &stubMailService{}, http.MethodPut, "/orgs/acme/email/templates/smtp_test/en", Template{
		Subject:    "It works",
		Headline:   "It works",
		Paragraphs: paragraphs,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if len(store.saved) != 0 {
		t.Error("an over-long template was saved")
	}
}

func TestDeleteTemplateRevertsAndReturnsTheDefault(t *testing.T) {
	store := newStubStore()
	store.overrides[templateTargetID(KindSMTPTest, LocaleEN)] = TemplateOverride{
		Kind: KindSMTPTest, Locale: LocaleEN,
		Template: Template{Subject: "Ours", Headline: "Ours"},
	}

	rec := serve(t, store, &stubMailService{}, http.MethodDelete, "/orgs/acme/email/templates/smtp_test/en", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeInto[templateResponse](t, rec)
	shipped, _ := DefaultTemplate(KindSMTPTest, LocaleEN)
	if got.Customized || got.Template.Subject != shipped.Subject {
		t.Errorf("response = %+v, want the shipped default back in force", got)
	}
	if len(store.deleted) != 1 {
		t.Errorf("deleted %v, want one reset", store.deleted)
	}
}

func TestDeleteTemplateThatWasNeverCustomizedIs404(t *testing.T) {
	rec := serve(t, newStubStore(), &stubMailService{}, http.MethodDelete, "/orgs/acme/email/templates/smtp_test/en", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// Kinds and locales are a closed backend set, so a value outside it is a request
// for something that does not exist rather than malformed input.
func TestUnknownKindOrLocaleIs404(t *testing.T) {
	targets := []string{
		"/orgs/acme/email/templates/not_a_kind/en",
		"/orgs/acme/email/templates/invitation/de",
	}
	for _, target := range targets {
		rec := serve(t, newStubStore(), &stubMailService{}, http.MethodGet, target, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", target, rec.Code)
		}
	}
}

func TestPreviewRendersTheSuppliedDraft(t *testing.T) {
	service := &stubMailService{}
	rec := serve(t, newStubStore(), service, http.MethodPost, "/orgs/acme/email/templates/invitation/preview", previewRequest{
		Locale: "en",
		Template: &Template{
			Subject:  "Join {{orgName}} today",
			Headline: "Welcome to {{orgName}}",
			CTALabel: "Accept",
			CTAURL:   "{{acceptUrl}}",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	got := decodeInto[previewResponse](t, rec)
	if got.Subject != "Join Acme BV today" {
		t.Errorf("subject = %q, want the org's real name substituted", got.Subject)
	}
	if got.HTML == "" || got.Text == "" {
		t.Error("a preview must carry both the HTML and the text alternative")
	}
	if !strings.Contains(got.Text, "wallet.example.org/invite") {
		t.Errorf("the sample accept URL is missing from the preview:\n%s", got.Text)
	}
}

// Without a draft the preview shows what is actually in force, so an admin can
// check the current mail before touching it.
func TestPreviewWithoutADraftRendersWhatIsInForce(t *testing.T) {
	service := &stubMailService{}
	rec := serve(t, newStubStore(), service, http.MethodPost, "/orgs/acme/email/templates/smtp_test/preview", previewRequest{Locale: "nl"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if service.previewed != nil {
		t.Error("a draft was passed to the service even though the request carried none")
	}
	got := decodeInto[previewResponse](t, rec)
	shipped, _ := DefaultTemplate(KindSMTPTest, LocaleNL)
	if got.Subject != shipped.Subject {
		t.Errorf("subject = %q, want the shipped Dutch default %q", got.Subject, shipped.Subject)
	}
}

func TestPreviewRejectsAnUnsupportedLocale(t *testing.T) {
	rec := serve(t, newStubStore(), &stubMailService{}, http.MethodPost, "/orgs/acme/email/templates/smtp_test/preview", previewRequest{Locale: "de"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

// A draft that cannot render is the tenant's mistake, so the preview reports it
// as a 400 with the reason rather than as a server error.
func TestPreviewOfAnInvalidDraftIs400(t *testing.T) {
	rec := serve(t, newStubStore(), &stubMailService{}, http.MethodPost, "/orgs/acme/email/templates/invitation/preview", previewRequest{
		Locale:   "en",
		Template: &Template{Subject: "Join us", Headline: "Hello {{nope}}"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_template") {
		t.Errorf("body = %s, want the invalid_template code", rec.Body.String())
	}
}

// The existing "does my SMTP work" button sends no kind, and must keep sending
// the self-test in the deployment's default language.
func TestSendTestWithoutAKindSendsTheSMTPSelfTest(t *testing.T) {
	service := &stubMailService{}
	rec := serve(t, newStubStore(), service, http.MethodPost, "/orgs/acme/email/test", testRequest{To: "admin@example.org"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	if len(service.specimens) != 1 {
		t.Fatalf("sent %d specimens, want 1", len(service.specimens))
	}
	got := service.specimens[0]
	if got.kind != KindSMTPTest || got.locale != "" || got.to != "admin@example.org" {
		t.Errorf("specimen = %+v, want the SMTP self-test in the deployment default locale", got)
	}
}

func TestSendTestCarriesTheRequestedKindAndLocale(t *testing.T) {
	service := &stubMailService{}
	rec := serve(t, newStubStore(), service, http.MethodPost, "/orgs/acme/email/test", testRequest{
		To: "admin@example.org", Kind: string(KindCredentialOffer), Locale: string(LocaleNL),
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	got := service.specimens[0]
	if got.kind != KindCredentialOffer || got.locale != LocaleNL {
		t.Errorf("specimen = %+v, want the requested kind and locale", got)
	}
}

func TestSendTestRejectsAnUnknownKind(t *testing.T) {
	service := &stubMailService{}
	rec := serve(t, newStubStore(), service, http.MethodPost, "/orgs/acme/email/test", testRequest{
		To: "admin@example.org", Kind: "not_a_kind",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if len(service.specimens) != 0 {
		t.Error("a specimen was sent for an unknown kind")
	}
}

func TestSendTestReportsNotConfiguredAsAConflict(t *testing.T) {
	service := &stubMailService{err: ErrNotConfigured}
	rec := serve(t, newStubStore(), service, http.MethodPost, "/orgs/acme/email/test", testRequest{To: "admin@example.org"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

// An unknown field is a typo or a stale client, and silently ignoring it would
// look like a save that worked.
func TestPutTemplateRejectsAnUnknownField(t *testing.T) {
	store := newStubStore()
	handler := NewHandler(store, &stubMailService{}, passthrough, passthrough)
	mux := http.NewServeMux()
	handler.Register(mux)

	req := httptest.NewRequest(http.MethodPut, "/orgs/acme/email/templates/smtp_test/en",
		strings.NewReader(`{"subject":"It works","headline":"It works","htmlBody":"<script>"}`))
	req = req.WithContext(adminContext(req.Context()))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if len(store.saved) != 0 {
		t.Error("a template with an unknown field was saved")
	}
}

func TestInvalidTemplateErrorReportsTheReasonAsA400(t *testing.T) {
	err, ok := invalidTemplateError(errors.New("something else"))
	if ok || err != nil {
		t.Fatalf("invalidTemplateError on an unrelated error = (%v, %v), want (nil, false)", err, ok)
	}

	// Wrapped once on the way out of a store, as a real handler receives it.
	wrapped := fmt.Errorf("saving: %w", &InvalidTemplateError{Reason: errors.New("headline: must not be empty")})
	err, ok = invalidTemplateError(wrapped)
	if !ok {
		t.Fatal("invalidTemplateError did not recognise an InvalidTemplateError")
	}
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want an APIError", err)
	}
	if apiErr.Status != http.StatusBadRequest || apiErr.Code != "invalid_template" {
		t.Errorf("apiErr = %+v, want a 400 invalid_template", apiErr)
	}
	if !strings.Contains(apiErr.Message, "headline") {
		t.Errorf("message = %q, want the field named", apiErr.Message)
	}
}
