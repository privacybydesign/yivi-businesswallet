package export

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// jobHistoryLimit bounds the export history a screen shows. An export is a rare,
// deliberate act, so one page of them is the whole story for any real org.
const jobHistoryLimit = 25

type jobManager interface {
	Enqueue(ctx context.Context, orgID uuid.UUID, sections []string, requestedBy *uuid.UUID) (Job, error)
	GetJob(ctx context.Context, orgID, jobID uuid.UUID) (Job, error)
	ListJobs(ctx context.Context, orgID uuid.UUID, limit int) ([]Job, error)
	BundleForJob(ctx context.Context, orgID, jobID uuid.UUID) (Job, []byte, error)
	Bundle(ctx context.Context, token string) (Job, []byte, error)
	ReleaseToken(ctx context.Context, jobID uuid.UUID) error
}

// jobView is a job as the API reports it. DownloadPath is present only while the
// bundle can actually be fetched, so a screen never offers a spent or expired
// link.
type jobView struct {
	Job
	DownloadPath string `json:"downloadPath,omitempty"`
}

func (h *Handler) registerJobs(mux *http.ServeMux, admin func(http.Handler) http.Handler) {
	mux.Handle("POST /orgs/{slug}/export/jobs", admin(respond.HandlerFunc(h.createJob)))
	mux.Handle("GET /orgs/{slug}/export/jobs", admin(respond.HandlerFunc(h.listJobs)))
	mux.Handle("GET /orgs/{slug}/export/jobs/{id}", admin(respond.HandlerFunc(h.getJob)))
	mux.Handle("GET /orgs/{slug}/export/jobs/{id}/download", admin(respond.HandlerFunc(h.downloadJob)))
	// The token route carries no session: the token is the credential, because a
	// termination export has to reach an owner who can no longer sign in. An
	// admin who *can* sign in uses the route above and spends no token.
	mux.Handle("GET /export/download/{token}", respond.HandlerFunc(h.downloadByToken))
}

func (h *Handler) createJob(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	sections, err := h.service.Sections(ParseSections(r.URL.Query().Get("sections")))
	if err != nil {
		return err
	}

	// RequireOrgAdmin runs behind RequireUser, so the actor is always present.
	actor := auth.UserFromContext(r.Context()).ID

	job, err := h.jobs.Enqueue(r.Context(), org.ID, sections, &actor)
	if err != nil {
		return fmt.Errorf("queueing export: %w", err)
	}
	respond.JSON(w, r, http.StatusAccepted, viewOf(job, org.Slug))
	return nil
}

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) error {
	org := organization.OrgFromContext(r.Context())
	jobs, err := h.jobs.ListJobs(r.Context(), org.ID, jobHistoryLimit)
	if err != nil {
		return fmt.Errorf("listing export jobs: %w", err)
	}
	views := make([]jobView, 0, len(jobs))
	for _, job := range jobs {
		views = append(views, viewOf(job, org.Slug))
	}
	respond.JSON(w, r, http.StatusOK, views)
	return nil
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request) error {
	job, org, err := h.resolveJob(r)
	if err != nil {
		return err
	}
	respond.JSON(w, r, http.StatusOK, viewOf(job, org.Slug))
	return nil
}

// downloadJob serves the bundle to an admin of the owning org. It spends no
// one-time token: that token exists for the caller who cannot authenticate, and
// an admin who can should be able to fetch the bundle more than once while it
// lives.
func (h *Handler) downloadJob(w http.ResponseWriter, r *http.Request) error {
	jobID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return badRequest("invalid_id", "invalid job id")
	}
	org := organization.OrgFromContext(r.Context())

	job, content, err := h.jobs.BundleForJob(r.Context(), org.ID, jobID)
	if errors.Is(err, ErrJobNotFound) {
		return &respond.APIError{Status: http.StatusNotFound, Code: "job_not_found", Message: "export job not found"}
	}
	if errors.Is(err, ErrBundleUnavailable) {
		return &respond.APIError{
			Status:  http.StatusConflict,
			Code:    "bundle_unavailable",
			Message: "this export has no bundle to download",
		}
	}
	if err != nil {
		return fmt.Errorf("reading export bundle: %w", err)
	}
	writeBundle(w, r, job, content)
	return nil
}

func (h *Handler) downloadByToken(w http.ResponseWriter, r *http.Request) error {
	token := r.PathValue("token")
	if token == "" {
		return badRequest("invalid_token", "invalid download token")
	}

	job, content, err := h.jobs.Bundle(r.Context(), token)
	if errors.Is(err, ErrBundleUnavailable) {
		// Unknown, spent and expired are one answer on purpose: the caller is
		// unauthenticated, and distinguishing them would confirm a token exists.
		return &respond.APIError{
			Status:  http.StatusNotFound,
			Code:    "bundle_unavailable",
			Message: "this download link is no longer valid",
		}
	}
	if err != nil {
		return fmt.Errorf("resolving export download: %w", err)
	}

	if !writeBundle(w, r, job, content) {
		// The bundle never arrived, so the single-use token goes back rather than
		// being spent on a connection that dropped. The request context is already
		// cancelled, which is why the release runs on a detached one.
		if releaseErr := h.jobs.ReleaseToken(context.WithoutCancel(r.Context()), job.ID); releaseErr != nil {
			slog.ErrorContext(r.Context(), "export: releasing download token",
				slog.String("jobId", job.ID.String()), slog.String("error", releaseErr.Error()))
		}
	}
	return nil
}

// resolveJob reads the addressed job, scoped to the caller's organization.
func (h *Handler) resolveJob(r *http.Request) (Job, organization.Organization, error) {
	jobID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return Job{}, organization.Organization{}, badRequest("invalid_id", "invalid job id")
	}
	org := organization.OrgFromContext(r.Context())

	job, err := h.jobs.GetJob(r.Context(), org.ID, jobID)
	if errors.Is(err, ErrJobNotFound) {
		return Job{}, org, &respond.APIError{Status: http.StatusNotFound, Code: "job_not_found", Message: "export job not found"}
	}
	if err != nil {
		return Job{}, org, fmt.Errorf("getting export job: %w", err)
	}
	return job, org, nil
}

// writeBundle sends the ZIP and reports whether the body reached the client.
func writeBundle(w http.ResponseWriter, r *http.Request, job Job, content []byte) bool {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", job.Filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(content); err != nil {
		slog.ErrorContext(r.Context(), "export: writing bundle body",
			slog.String("jobId", job.ID.String()), slog.String("error", err.Error()))
		return false
	}
	return true
}

func viewOf(job Job, slug string) jobView {
	view := jobView{Job: job}
	if job.Status == JobReady && (job.ExpiresAt == nil || job.ExpiresAt.After(time.Now())) {
		view.DownloadPath = fmt.Sprintf("/api/v1/orgs/%s/export/jobs/%s/download", url.PathEscape(slug), job.ID)
	}
	return view
}

// DownloadURL is the absolute link handed to someone holding a raw token — the
// termination mail's only way back to the bundle.
func DownloadURL(appBaseURL, token string) string {
	return strings.TrimRight(appBaseURL, "/") + "/api/v1/export/download/" + url.PathEscape(token)
}
