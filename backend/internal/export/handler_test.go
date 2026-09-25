package export

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

func passthrough(next http.Handler) http.Handler { return next }

func testHandler(t *testing.T, writers []SectionWriter) *Handler {
	t.Helper()
	svc := NewService(&fakeRecorder{}, writers)
	fixedClock(svc)
	return NewHandler(svc, newFakeJobs(), passthrough, passthrough)
}

func exportRequest(t *testing.T, h *Handler, query, role string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)

	target := "/orgs/caesar/export"
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := organization.ContextWithOrg(req.Context(), organization.Organization{
		ID:             testOrg().ID,
		Name:           testOrg().Name,
		Slug:           testOrg().Slug,
		KVKNumber:      testOrg().KVKNumber,
		EUID:           testOrg().EUID,
		DigitalAddress: testOrg().DigitalAddress,
		Status:         testOrg().Status,
		BootstrappedAt: time.Date(2026, 1, 8, 11, 22, 31, 0, time.UTC),
	})
	ctx = organization.ContextWithRole(ctx, role)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestExportIsAdminOnly(t *testing.T) {
	h := testHandler(t, allWriters())

	rec := exportRequest(t, h, "", organization.RoleMember)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d for a non-admin member", rec.Code, http.StatusForbidden)
	}
	if rec.Body.Len() > 0 && bytes.Contains(rec.Body.Bytes(), []byte("PK")) {
		t.Error("a forbidden response carried bundle bytes")
	}
}

func TestExportServesACompleteZip(t *testing.T) {
	h := testHandler(t, allWriters())

	rec := exportRequest(t, h, "", organization.RoleAdmin)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", got)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(rec.Body.Len()) {
		t.Errorf("Content-Length = %q, want %d", got, rec.Body.Len())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="caesar-export-20260727T091402Z.zip"` {
		t.Errorf("Content-Disposition = %q, want the org- and time-stamped filename", got)
	}

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("response is not a readable zip: %v", err)
	}
	if firstName(zr) != manifestPath {
		t.Errorf("first entry = %q, want %q", firstName(zr), manifestPath)
	}
}

func TestExportRejectsAnUnknownSectionWithA400(t *testing.T) {
	h := testHandler(t, allWriters())

	rec := exportRequest(t, h, "sections=nope", organization.RoleAdmin)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error body %q: %v", rec.Body.String(), err)
	}
	if body.Code != "invalid_section" {
		t.Errorf("error code = %q, want invalid_section", body.Code)
	}
}

func TestExportPassesTheSectionFilterThrough(t *testing.T) {
	var seen []string
	svc := NewService(&fakeRecorder{}, allWriters())
	fixedClock(svc)
	h := NewHandler(sectionSpy{inner: svc, seen: &seen}, newFakeJobs(), passthrough, passthrough)

	if rec := exportRequest(t, h, "sections=qerds,auditRecords", organization.RoleAdmin); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(seen) != 2 || seen[0] != SectionQerds || seen[1] != SectionAuditRecords {
		t.Errorf("service saw sections %v, want [qerds auditRecords]", seen)
	}
}

type sectionSpy struct {
	inner *Service
	seen  *[]string
}

func (s sectionSpy) Export(ctx context.Context, org Organization, sections []string) (*Archive, error) {
	*s.seen = sections
	return s.inner.Export(ctx, org, sections)
}

func (s sectionSpy) Sections(requested []string) ([]string, error) {
	return s.inner.Sections(requested)
}

func TestExportManifestCarriesTheResolvedOrganization(t *testing.T) {
	h := testHandler(t, allWriters())

	rec := exportRequest(t, h, "", organization.RoleAdmin)

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("response is not a readable zip: %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(readEntry(t, zr, manifestPath), &manifest); err != nil {
		t.Fatalf("decoding manifest: %v", err)
	}
	if manifest.Organization != testOrg() {
		t.Errorf("organization = %+v, want %+v", manifest.Organization, testOrg())
	}
}

func TestExportCleansUpAfterAFailedWrite(t *testing.T) {
	svc := NewService(&fakeRecorder{}, allWriters())
	fixedClock(svc)
	archive, err := svc.Export(context.Background(), testOrg(), nil)
	if err != nil {
		t.Fatalf("Export() = %v, want nil", err)
	}
	dir := archive.dir

	h := NewHandler(handedArchive{archive: archive}, newFakeJobs(), passthrough, passthrough)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/orgs/caesar/export", nil)
	ctx := organization.ContextWithRole(
		organization.ContextWithOrg(req.Context(), organization.Organization{ID: uuid.New(), Slug: "caesar"}),
		organization.RoleAdmin)
	mux.ServeHTTP(failingWriter{ResponseWriter: httptest.NewRecorder()}, req.WithContext(ctx))

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the staging directory survived a failed write (%v)", err)
	}
}

type handedArchive struct{ archive *Archive }

func (h handedArchive) Export(context.Context, Organization, []string) (*Archive, error) {
	return h.archive, nil
}

func (handedArchive) Sections(requested []string) ([]string, error) { return requested, nil }

type failingWriter struct{ http.ResponseWriter }

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
