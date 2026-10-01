package export

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// fakeJobs is an in-memory job store: enough to drive the handler and the worker
// without a database.
type fakeJobs struct {
	queued    []Job
	byID      map[uuid.UUID]Job
	content   map[uuid.UUID][]byte
	tokens    map[string]uuid.UUID
	released  []uuid.UUID
	completes int
	failures  []string
	enqueued  [][]string
}

func newFakeJobs() *fakeJobs {
	return &fakeJobs{
		byID:    map[uuid.UUID]Job{},
		content: map[uuid.UUID][]byte{},
		tokens:  map[string]uuid.UUID{},
	}
}

func (f *fakeJobs) ensure() {
	if f.byID == nil {
		f.byID = map[uuid.UUID]Job{}
		f.content = map[uuid.UUID][]byte{}
		f.tokens = map[string]uuid.UUID{}
	}
}

func (f *fakeJobs) Enqueue(_ context.Context, orgID uuid.UUID, sections []string, requestedBy *uuid.UUID) (Job, error) {
	f.ensure()
	job := Job{
		ID: uuid.New(), OrganizationID: orgID, Status: JobQueued,
		Sections: sections, RequestedBy: requestedBy, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.byID[job.ID] = job
	f.queued = append(f.queued, job)
	f.enqueued = append(f.enqueued, sections)
	return job, nil
}

func (f *fakeJobs) Claim(context.Context) (Job, error) {
	if len(f.queued) == 0 {
		return Job{}, ErrJobNotFound
	}
	job := f.queued[0]
	f.queued = f.queued[1:]
	job.Status = JobRunning
	f.byID[job.ID] = job
	return job, nil
}

func (f *fakeJobs) Complete(_ context.Context, jobID, bundleID uuid.UUID, filename string, content []byte, expiresAt time.Time) (string, error) {
	f.ensure()
	f.completes++
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	job := f.byID[jobID]
	job.Status = JobReady
	job.BundleID = &bundleID
	job.Filename = filename
	job.SizeBytes = int64(len(content))
	job.Checksum = checksumOf(content).Value
	job.ExpiresAt = &expiresAt
	f.byID[jobID] = job
	f.content[jobID] = content
	f.tokens[token] = jobID
	return token, nil
}

func (f *fakeJobs) Fail(_ context.Context, jobID uuid.UUID, reason string) error {
	f.failures = append(f.failures, reason)
	job := f.byID[jobID]
	job.Status = JobFailed
	job.Error = reason
	f.byID[jobID] = job
	return nil
}

func (f *fakeJobs) GetJob(_ context.Context, orgID, jobID uuid.UUID) (Job, error) {
	job, ok := f.byID[jobID]
	if !ok || job.OrganizationID != orgID {
		return Job{}, ErrJobNotFound
	}
	return job, nil
}

func (f *fakeJobs) ListJobs(_ context.Context, orgID uuid.UUID, _ int) ([]Job, error) {
	jobs := []Job{}
	for _, job := range f.byID {
		if job.OrganizationID == orgID {
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

func (f *fakeJobs) BundleForJob(_ context.Context, orgID, jobID uuid.UUID) (Job, []byte, error) {
	job, ok := f.byID[jobID]
	if !ok || job.OrganizationID != orgID {
		return Job{}, nil, ErrJobNotFound
	}
	content, ok := f.content[jobID]
	if !ok || job.Status != JobReady {
		return Job{}, nil, ErrBundleUnavailable
	}
	return job, content, nil
}

func (f *fakeJobs) Bundle(_ context.Context, token string) (Job, []byte, error) {
	jobID, ok := f.tokens[token]
	if !ok {
		return Job{}, nil, ErrBundleUnavailable
	}
	delete(f.tokens, token)
	job := f.byID[jobID]
	now := time.Now()
	job.DownloadedAt = &now
	f.byID[jobID] = job
	return job, f.content[jobID], nil
}

func (f *fakeJobs) ReleaseToken(_ context.Context, jobID uuid.UUID) error {
	f.released = append(f.released, jobID)
	return nil
}

type fakeOrgResolver struct{ err error }

func (f fakeOrgResolver) Resolve(context.Context, uuid.UUID) (Organization, error) {
	return testOrg(), f.err
}

func testWorker(jobs jobStore, orgs orgResolver) *Worker {
	svc := NewService(&fakeRecorder{}, allWriters())
	fixedClock(svc)
	return NewWorker(jobs, orgs, svc)
}

// A queued export is audited when it is requested, so the worker assembling it
// must not record the same act again.
func TestWorkerAssemblesWithoutReauditing(t *testing.T) {
	jobs := newFakeJobs()
	audit := &fakeRecorder{}
	svc := NewService(audit, allWriters())
	fixedClock(svc)
	worker := NewWorker(jobs, fakeOrgResolver{}, svc)

	if _, err := jobs.Enqueue(context.Background(), testOrg().ID, SectionOrder, nil); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if !worker.runOnce(context.Background()) {
		t.Fatal("runOnce() = false, want it to claim the queued job")
	}

	if jobs.completes != 1 {
		t.Errorf("completed %d jobs, want 1", jobs.completes)
	}
	if len(audit.calls) != 0 {
		t.Errorf("worker recorded %d audit events, want none", len(audit.calls))
	}
}

func TestWorkerReportsNoWorkOnAnEmptyQueue(t *testing.T) {
	if testWorker(newFakeJobs(), fakeOrgResolver{}).runOnce(context.Background()) {
		t.Error("runOnce() = true on an empty queue")
	}
}

// A run that cannot produce a bundle records why, so an operator reading the job
// learns something other than "not ready".
func TestWorkerRecordsWhyARunFailed(t *testing.T) {
	jobs := newFakeJobs()
	worker := testWorker(jobs, fakeOrgResolver{err: errors.New("org vanished")})
	if _, err := jobs.Enqueue(context.Background(), testOrg().ID, nil, nil); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	worker.runOnce(context.Background())

	if len(jobs.failures) != 1 || !strings.Contains(jobs.failures[0], "org vanished") {
		t.Errorf("failures = %v, want the store's reason", jobs.failures)
	}
	if jobs.completes != 0 {
		t.Error("a failed run completed a job")
	}
}

// The finished bundle's token exists in the clear exactly once, and the handler
// registered for it is how a termination export reaches its owner.
func TestWorkerHandsTheTokenToTheReadyHandler(t *testing.T) {
	jobs := newFakeJobs()
	worker := testWorker(jobs, fakeOrgResolver{})
	var seen string
	worker.OnReady(func(_ context.Context, _ Job, token string) { seen = token })

	if _, err := jobs.Enqueue(context.Background(), testOrg().ID, nil, nil); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	worker.runOnce(context.Background())

	if seen == "" {
		t.Fatal("the ready handler was not given a token")
	}
	if _, _, err := jobs.Bundle(context.Background(), seen); err != nil {
		t.Errorf("the handed token does not resolve: %v", err)
	}
}

func jobRequest(t *testing.T, h *Handler, method, target, role string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(method, target, nil)
	ctx := organization.ContextWithOrg(req.Context(), organization.Organization{
		ID: testOrg().ID, Slug: testOrg().Slug, Name: testOrg().Name,
	})
	ctx = organization.ContextWithRole(ctx, role)
	ctx = auth.ContextWithUser(ctx, user.User{ID: uuid.New()})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestCreateJobQueuesAnExport(t *testing.T) {
	jobs := newFakeJobs()
	svc := NewService(&fakeRecorder{}, allWriters())
	h := NewHandler(svc, jobs, passthrough, passthrough)

	rec := jobRequest(t, h, http.MethodPost, "/orgs/caesar/export/jobs?sections=attestations", organization.RoleAdmin)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var view jobView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	if view.Status != JobQueued {
		t.Errorf("status = %q, want %q", view.Status, JobQueued)
	}
	// A queued job has nothing to download yet.
	if view.DownloadPath != "" {
		t.Errorf("downloadPath = %q, want none on a queued job", view.DownloadPath)
	}
	if len(jobs.enqueued) != 1 || len(jobs.enqueued[0]) != 1 || jobs.enqueued[0][0] != SectionAttestations {
		t.Errorf("enqueued %v, want the attestations section only", jobs.enqueued)
	}
}

// The filter is validated before anything is queued: a typo must not produce a
// job that fails minutes later in the worker.
func TestCreateJobRejectsAnUnknownSection(t *testing.T) {
	jobs := newFakeJobs()
	h := NewHandler(NewService(&fakeRecorder{}, allWriters()), jobs, passthrough, passthrough)

	rec := jobRequest(t, h, http.MethodPost, "/orgs/caesar/export/jobs?sections=nope", organization.RoleAdmin)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(jobs.enqueued) != 0 {
		t.Errorf("queued %v despite the bad filter", jobs.enqueued)
	}
}

func TestJobRoutesAreAdminOnly(t *testing.T) {
	h := NewHandler(NewService(&fakeRecorder{}, allWriters()), newFakeJobs(), passthrough, passthrough)

	for _, target := range []string{"/orgs/caesar/export/jobs", "/orgs/caesar/export/jobs/" + uuid.New().String()} {
		if rec := jobRequest(t, h, http.MethodGet, target, organization.RoleMember); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s as a member = %d, want 403", target, rec.Code)
		}
	}
	if rec := jobRequest(t, h, http.MethodPost, "/orgs/caesar/export/jobs", organization.RoleMember); rec.Code != http.StatusForbidden {
		t.Errorf("POST as a member = %d, want 403", rec.Code)
	}
}

// A ready job offers a download path; the admin route it points at spends no
// one-time token, so the bundle stays fetchable while it lives.
func TestReadyJobOffersAnAdminDownload(t *testing.T) {
	jobs := newFakeJobs()
	h := NewHandler(NewService(&fakeRecorder{}, allWriters()), jobs, passthrough, passthrough)
	job, _ := jobs.Enqueue(context.Background(), testOrg().ID, nil, nil)
	if _, err := jobs.Complete(context.Background(), job.ID, uuid.New(), "caesar-export.zip",
		[]byte("PK\x03\x04bundle"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	rec := jobRequest(t, h, http.MethodGet, "/orgs/caesar/export/jobs/"+job.ID.String(), organization.RoleAdmin)
	var view jobView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	want := "/api/v1/orgs/caesar/export/jobs/" + job.ID.String() + "/download"
	if view.DownloadPath != want {
		t.Fatalf("downloadPath = %q, want %q", view.DownloadPath, want)
	}

	// The view reports the client-facing path; the feature mux sees it stripped.
	mounted := strings.TrimPrefix(want, "/api/v1")
	for i := range 2 {
		rec = jobRequest(t, h, http.MethodGet, mounted, organization.RoleAdmin)
		if rec.Code != http.StatusOK {
			t.Fatalf("download %d = %d, want 200: %s", i, rec.Code, rec.Body.String())
		}
		if rec.Body.String() != "PK\x03\x04bundle" {
			t.Errorf("body = %q, want the stored bundle", rec.Body.String())
		}
	}
}

// The token route is single-use, and unknown, spent and expired are one answer:
// the caller is unauthenticated, so distinguishing them would confirm a token.
func TestTokenDownloadIsSingleUse(t *testing.T) {
	jobs := newFakeJobs()
	h := NewHandler(NewService(&fakeRecorder{}, allWriters()), jobs, passthrough, passthrough)
	job, _ := jobs.Enqueue(context.Background(), testOrg().ID, nil, nil)
	token, err := jobs.Complete(context.Background(), job.ID, uuid.New(), "caesar-export.zip",
		[]byte("PK\x03\x04bundle"), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/export/download/"+token, nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first download = %d, want 200: %s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	mux.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/export/download/"+token, nil))
	if second.Code != http.StatusNotFound {
		t.Errorf("second download = %d, want 404", second.Code)
	}

	unknown := httptest.NewRecorder()
	mux.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/export/download/deadbeef", nil))
	if unknown.Code != second.Code {
		t.Errorf("unknown token = %d, spent token = %d, want one answer", unknown.Code, second.Code)
	}
}

// A connection that drops mid-transfer never delivered the bundle, so the token
// goes back rather than being spent on it.
func TestTokenIsReleasedWhenTheBodyFails(t *testing.T) {
	jobs := newFakeJobs()
	h := NewHandler(NewService(&fakeRecorder{}, allWriters()), jobs, passthrough, passthrough)
	job, _ := jobs.Enqueue(context.Background(), testOrg().ID, nil, nil)
	token, err := jobs.Complete(context.Background(), job.ID, uuid.New(), "caesar-export.zip",
		[]byte("PK\x03\x04bundle"), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	mux := http.NewServeMux()
	h.Register(mux)
	mux.ServeHTTP(failingWriter{ResponseWriter: httptest.NewRecorder()},
		httptest.NewRequest(http.MethodGet, "/export/download/"+token, nil))

	if len(jobs.released) != 1 || jobs.released[0] != job.ID {
		t.Errorf("released = %v, want the job whose download failed", jobs.released)
	}
}

func TestDownloadURLIsAbsolute(t *testing.T) {
	got := DownloadURL("https://wallet.example.org/", "abc123")
	if want := "https://wallet.example.org/api/v1/export/download/abc123"; got != want {
		t.Errorf("DownloadURL() = %q, want %q", got, want)
	}
}
