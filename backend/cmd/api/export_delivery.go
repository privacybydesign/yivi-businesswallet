package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/export"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// exportDeliveryTimeout bounds pushing a finished bundle to the org's admins. It
// runs on the worker's own goroutine after the job is stored, so a slow relay
// must not hold the queue.
const exportDeliveryTimeout = 30 * time.Second

// exportDelivery pushes a termination bundle to the organisation's admins. An
// export somebody asked for interactively needs nothing here — they are looking
// at the status page — but a termination export exists precisely because nobody
// there can necessarily sign in any more, so it has to be sent.
type exportDelivery struct {
	orgs       *organization.Store
	email      *email.Service
	appBaseURL string
	ttl        time.Duration
}

func (d exportDelivery) notify(ctx context.Context, job export.Job, token string) {
	if job.Origin != export.OriginTermination {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), exportDeliveryTimeout)
	defer cancel()

	org, err := d.orgs.GetByID(ctx, job.OrganizationID)
	if err != nil {
		slog.ErrorContext(ctx, "export: resolving org for termination delivery",
			slog.String("orgId", job.OrganizationID.String()), slog.String("error", err.Error()))
		return
	}
	admins, err := d.orgs.ListAdminEmails(ctx, job.OrganizationID)
	if err != nil {
		slog.ErrorContext(ctx, "export: listing admins for termination delivery",
			slog.String("orgId", job.OrganizationID.String()), slog.String("error", err.Error()))
		return
	}
	if len(admins) == 0 {
		// Nothing to do and nothing to fix silently: the bundle is still stored
		// and reachable by its token, which an operator can hand over.
		slog.WarnContext(ctx, "export: termination bundle has no admin to send to",
			slog.String("orgId", job.OrganizationID.String()), slog.String("jobId", job.ID.String()))
		return
	}

	if err := d.email.SendExportReady(ctx, job.OrganizationID, admins, org.Name,
		export.DownloadURL(d.appBaseURL, token), d.ttl.String()); err != nil {
		// Delivery is best-effort: the obligation is recorded either way, and the
		// bundle stays downloadable until it expires.
		slog.ErrorContext(ctx, "export: sending termination bundle",
			slog.String("orgId", job.OrganizationID.String()),
			slog.String("jobId", job.ID.String()), slog.String("error", err.Error()))
	}
}
