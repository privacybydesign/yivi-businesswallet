package export

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Job origins: who caused the export to exist.
const (
	// OriginRequest is an admin asking for the bundle.
	OriginRequest = "request"
	// OriginTermination is the provider's own Art 7(6)(f) obligation firing. Its
	// bundle is pushed to the organisation's admins rather than waiting to be
	// collected, because by then nobody there can necessarily sign in.
	OriginTermination = "termination"
)

// Job statuses.
const (
	JobQueued  = "queued"
	JobRunning = "running"
	JobReady   = "ready"
	JobFailed  = "failed"
)

// downloadTokenBytes sizes the download token. It is the only credential on the
// unauthenticated download route, so it carries the same entropy as a session.
const downloadTokenBytes = 32

// maxJobErrorLength bounds what a failed run records. The reason is for an
// operator reading the job, not a transcript.
const maxJobErrorLength = 500

var (
	// ErrJobNotFound is returned for a job id no organization owns.
	ErrJobNotFound = errors.New("export: job not found")
	// ErrBundleUnavailable is returned when a job carries no downloadable
	// bundle: it has not finished, it failed, it expired, or it was already
	// downloaded.
	ErrBundleUnavailable = errors.New("export: bundle unavailable")
)

// Job is one background export. Content is loaded only by the download path.
type Job struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organizationId"`
	Status         string     `json:"status"`
	Origin         string     `json:"origin"`
	Sections       []string   `json:"sections"`
	RequestedBy    *uuid.UUID `json:"requestedBy,omitempty"`
	BundleID       *uuid.UUID `json:"bundleId,omitempty"`
	Filename       string     `json:"filename,omitempty"`
	SizeBytes      int64      `json:"sizeBytes"`
	Checksum       string     `json:"checksum,omitempty"`
	DownloadedAt   *time.Time `json:"downloadedAt,omitempty"`
	Error          string     `json:"error,omitempty"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

const jobColumns = `id, organization_id, status, origin, sections, requested_by, bundle_id,
	filename, size_bytes, checksum, downloaded_at, error, expires_at, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.OrganizationID, &j.Status, &j.Origin, &j.Sections, &j.RequestedBy,
		&j.BundleID, &j.Filename, &j.SizeBytes, &j.Checksum, &j.DownloadedAt,
		&j.Error, &j.ExpiresAt, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

// Enqueue records a requested export. The bundle is assembled by the worker, so
// this only writes the request and its audit event.
func (s *Store) Enqueue(ctx context.Context, orgID uuid.UUID, sections []string, requestedBy *uuid.UUID) (Job, error) {
	var job Job
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		var err error
		job, err = s.enqueue(ctx, q, orgID, OriginRequest, sections, requestedBy)
		return err
	})
	return job, err
}

// EnqueueTx queues an export inside a caller's transaction, so a termination and
// the export it owes commit together — a termination recorded without its export
// would leave the obligation with nothing tracking it.
func (s *Store) EnqueueTx(ctx context.Context, q database.Querier, orgID uuid.UUID, origin string, requestedBy *uuid.UUID) error {
	_, err := s.enqueue(ctx, q, orgID, origin, nil, requestedBy)
	return err
}

func (s *Store) enqueue(ctx context.Context, q database.Querier, orgID uuid.UUID, origin string, sections []string, requestedBy *uuid.UUID) (Job, error) {
	// A nil slice writes NULL, which the column refuses. "Every section" is an
	// empty list here, resolved against the registered writers when the run
	// starts.
	if sections == nil {
		sections = []string{}
	}
	const insert = `INSERT INTO export_jobs (organization_id, origin, sections, requested_by)
		VALUES ($1, $2, $3, $4) RETURNING ` + jobColumns
	job, err := scanJob(q.QueryRow(ctx, insert, orgID, origin, sections, requestedBy))
	if err != nil {
		return Job{}, fmt.Errorf("export: enqueue job org %s: %w", orgID, err)
	}
	if err := s.audit.Record(ctx, q, audit.ExportRequested,
		audit.Target{Type: audit.TargetExport, ID: job.ID.String(), OrgID: &orgID},
		audit.Created(map[string]any{
			"jobId":         job.ID.String(),
			"schemaVersion": SchemaVersion,
			"sections":      sections,
			"origin":        origin,
			"mode":          "background",
		})); err != nil {
		return Job{}, err
	}
	return job, nil
}

// Claim takes the oldest queued job off the queue and marks it running. SKIP
// LOCKED is what lets more than one replica run a worker without two of them
// building the same bundle. Reports ErrJobNotFound when the queue is empty.
func (s *Store) Claim(ctx context.Context) (Job, error) {
	const claim = `UPDATE export_jobs SET status = $1, updated_at = now()
		WHERE id = (
			SELECT id FROM export_jobs WHERE status = $2
			ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
		)
		RETURNING ` + jobColumns
	job, err := scanJob(s.db.QueryRow(ctx, claim, JobRunning, JobQueued))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("export: claim job: %w", err)
	}
	return job, nil
}

// Complete stores a finished bundle and returns the raw download token, which is
// the only time it exists in the clear.
func (s *Store) Complete(ctx context.Context, jobID, bundleID uuid.UUID, filename string, content []byte, expiresAt time.Time) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	checksum := checksumOf(content)

	const update = `UPDATE export_jobs
		SET status = $2, bundle_id = $3, filename = $4, content = $5, size_bytes = $6,
		    checksum = $7, download_token_hash = $8, expires_at = $9, error = '', updated_at = now()
		WHERE id = $1`
	tag, err := s.db.Exec(ctx, update, jobID, JobReady, bundleID, filename, content,
		int64(len(content)), checksum.Value, hashToken(token), expiresAt)
	if err != nil {
		return "", fmt.Errorf("export: complete job %s: %w", jobID, err)
	}
	if tag.RowsAffected() == 0 {
		return "", ErrJobNotFound
	}
	return token, nil
}

// Fail records why a run produced no bundle.
func (s *Store) Fail(ctx context.Context, jobID uuid.UUID, reason string) error {
	const update = `UPDATE export_jobs SET status = $2, error = $3, updated_at = now() WHERE id = $1`
	if _, err := s.db.Exec(ctx, update, jobID, JobFailed, truncate(reason, maxJobErrorLength)); err != nil {
		return fmt.Errorf("export: fail job %s: %w", jobID, err)
	}
	return nil
}

// GetJob reads one job, scoped to its organization so a job id from another org
// is not found rather than forbidden.
func (s *Store) GetJob(ctx context.Context, orgID, jobID uuid.UUID) (Job, error) {
	const query = `SELECT ` + jobColumns + ` FROM export_jobs WHERE id = $1 AND organization_id = $2`
	job, err := scanJob(s.db.QueryRow(ctx, query, jobID, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("export: get job %s: %w", jobID, err)
	}
	return job, nil
}

// ListJobs reports an organization's export history, newest first.
func (s *Store) ListJobs(ctx context.Context, orgID uuid.UUID, limit int) ([]Job, error) {
	const query = `SELECT ` + jobColumns + ` FROM export_jobs
		WHERE organization_id = $1 ORDER BY created_at DESC LIMIT $2`
	rows, err := s.db.Query(ctx, query, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("export: list jobs org %s: %w", orgID, err)
	}
	defer rows.Close()

	jobs := []Job{}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("export: list jobs scan: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export: list jobs rows: %w", err)
	}
	return jobs, nil
}

// BundleForJob reads a ready bundle for an admin of the owning org. It consumes
// no token: that token is for the caller who cannot authenticate.
func (s *Store) BundleForJob(ctx context.Context, orgID, jobID uuid.UUID) (Job, []byte, error) {
	const query = `SELECT ` + jobColumns + `, content FROM export_jobs
		WHERE id = $1 AND organization_id = $2`
	var job Job
	var content []byte
	err := s.db.QueryRow(ctx, query, jobID, orgID).Scan(&job.ID, &job.OrganizationID, &job.Status,
		&job.Origin, &job.Sections, &job.RequestedBy, &job.BundleID, &job.Filename, &job.SizeBytes,
		&job.Checksum, &job.DownloadedAt, &job.Error, &job.ExpiresAt, &job.CreatedAt,
		&job.UpdatedAt, &content)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, nil, ErrJobNotFound
	}
	if err != nil {
		return Job{}, nil, fmt.Errorf("export: read bundle for job %s: %w", jobID, err)
	}
	if job.Status != JobReady || len(content) == 0 {
		return Job{}, nil, ErrBundleUnavailable
	}
	return job, content, nil
}

// Bundle resolves a raw download token to its bundle and consumes the token in
// the same statement, so two simultaneous requests cannot both be served. The
// caller is unauthenticated — the token is the credential — so a token that is
// unknown, spent or expired is one indistinguishable error.
func (s *Store) Bundle(ctx context.Context, token string) (Job, []byte, error) {
	const update = `UPDATE export_jobs SET downloaded_at = now(), updated_at = now()
		WHERE download_token_hash = $1 AND status = $2 AND downloaded_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		RETURNING ` + jobColumns + `, content`

	var job Job
	var content []byte
	row := s.db.QueryRow(ctx, update, hashToken(token), JobReady)
	err := row.Scan(&job.ID, &job.OrganizationID, &job.Status, &job.Origin, &job.Sections,
		&job.RequestedBy, &job.BundleID, &job.Filename, &job.SizeBytes, &job.Checksum,
		&job.DownloadedAt, &job.Error, &job.ExpiresAt, &job.CreatedAt, &job.UpdatedAt, &content)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, nil, ErrBundleUnavailable
	}
	if err != nil {
		return Job{}, nil, fmt.Errorf("export: resolve download token: %w", err)
	}
	return job, content, nil
}

// ReleaseToken puts a consumed token back. The download route calls it when the
// response body could not be written: a connection that dropped mid-transfer
// never delivered the bundle, and spending the token on it would leave the owner
// with no way to fetch what they asked for.
func (s *Store) ReleaseToken(ctx context.Context, jobID uuid.UUID) error {
	const update = `UPDATE export_jobs SET downloaded_at = NULL, updated_at = now() WHERE id = $1`
	if _, err := s.db.Exec(ctx, update, jobID); err != nil {
		return fmt.Errorf("export: release download token %s: %w", jobID, err)
	}
	return nil
}

// PruneExpired drops the stored bytes of bundles nobody can download any more.
// The row stays: the job is part of the org's export history, and only the
// payload is transient.
func (s *Store) PruneExpired(ctx context.Context) (int64, error) {
	const update = `UPDATE export_jobs SET content = NULL, download_token_hash = NULL, updated_at = now()
		WHERE content IS NOT NULL
		  AND (downloaded_at IS NOT NULL OR (expires_at IS NOT NULL AND expires_at <= now()))`
	tag, err := s.db.Exec(ctx, update)
	if err != nil {
		return 0, fmt.Errorf("export: prune expired bundles: %w", err)
	}
	return tag.RowsAffected(), nil
}

func randomToken() (string, error) {
	buf := make([]byte, downloadTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("export: generating download token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
