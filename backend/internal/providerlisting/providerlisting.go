// Package providerlisting stops every org's wallet when we as Business Wallet
// Provider are no longer on the trusted list of wallet providers (Art 12(5)):
// Art 6(2)(f) then revokes the validity of every wallet we provide.
//
// Revoking is final, so only a list that was read and does not name us counts.
// A list that cannot be read changes nothing, and the wallets stop only once we
// have been off the list for the whole grace period.
package providerlisting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// List is the trusted list of wallet providers.
type List interface {
	Ping(ctx context.Context) error
	// ProviderListed reports whether we are on the list. An error means the list
	// could not be read, and says nothing about whether we are on it.
	ProviderListed(ctx context.Context) (bool, error)
}

// Wallets revokes the wallets we provide.
type Wallets interface {
	LiveOrganizations(ctx context.Context) ([]uuid.UUID, error)
	ProviderDelisted(ctx context.Context, orgID uuid.UUID) error
}

// Notifier tells an org's admins its wallet was revoked, with the reason.
type Notifier interface {
	ProviderDelisted(ctx context.Context, orgID uuid.UUID) error
}

// Watcher checks the list and stops the wallets once we are off it for grace.
type Watcher struct {
	db      database.DB
	list    List
	wallets Wallets
	notify  Notifier
	grace   time.Duration
	now     func() time.Time
}

func NewWatcher(db database.DB, list List, wallets Wallets, notify Notifier, grace time.Duration) *Watcher {
	return &Watcher{db: db, list: list, wallets: wallets, notify: notify, grace: grace, now: time.Now}
}

// Check reads the list once and reports how many wallets it revoked. While we
// stay off the list every run revokes what an earlier run did not get to.
func (w *Watcher) Check(ctx context.Context) (int64, error) {
	listed, err := w.list.ProviderListed(ctx)
	if err != nil {
		return 0, fmt.Errorf("providerlisting: read trusted list: %w", err)
	}
	if listed {
		return 0, w.listed(ctx)
	}
	missingSince, err := w.missing(ctx)
	if err != nil {
		return 0, err
	}
	if w.now().Sub(missingSince) < w.grace {
		return 0, nil
	}
	if err := w.markDelisted(ctx); err != nil {
		return 0, err
	}
	return w.stopAll(ctx)
}

func (w *Watcher) listed(ctx context.Context) error {
	const update = `INSERT INTO provider_listing_state (missing_since, checked_at) VALUES (NULL, now())
		ON CONFLICT (id) DO UPDATE SET missing_since = NULL, checked_at = now()
		RETURNING delisted_at`
	var delistedAt *time.Time
	if err := w.db.QueryRow(ctx, update).Scan(&delistedAt); err != nil {
		return fmt.Errorf("providerlisting: record listed: %w", err)
	}
	if delistedAt != nil {
		slog.WarnContext(ctx, "providerlisting: back on the trusted list; the wallets revoked when we were off it stay revoked",
			slog.Time("delisted_at", *delistedAt))
	}
	return nil
}

// missing records that we are off the list and returns since when.
func (w *Watcher) missing(ctx context.Context) (time.Time, error) {
	const upsert = `INSERT INTO provider_listing_state (missing_since, checked_at) VALUES ($1, $1)
		ON CONFLICT (id) DO UPDATE SET missing_since = COALESCE(provider_listing_state.missing_since, $1), checked_at = $1
		RETURNING missing_since, missing_since = $1`
	var (
		since time.Time
		first bool
	)
	if err := w.db.QueryRow(ctx, upsert, w.now()).Scan(&since, &first); err != nil {
		return time.Time{}, fmt.Errorf("providerlisting: record missing: %w", err)
	}
	if first {
		// Logged as an error so we hear of it while the grace still runs.
		slog.ErrorContext(ctx, "providerlisting: we are not on the trusted list of wallet providers; every wallet is revoked if we stay off it",
			slog.Time("revoke_at", since.Add(w.grace)))
	}
	return since, nil
}

func (w *Watcher) markDelisted(ctx context.Context) error {
	const update = `UPDATE provider_listing_state SET delisted_at = COALESCE(delisted_at, $1)`
	if _, err := w.db.Exec(ctx, update, w.now()); err != nil {
		return fmt.Errorf("providerlisting: record delisted: %w", err)
	}
	return nil
}

func (w *Watcher) stopAll(ctx context.Context) (int64, error) {
	orgs, err := w.wallets.LiveOrganizations(ctx)
	if err != nil {
		return 0, fmt.Errorf("providerlisting: list wallets: %w", err)
	}
	var (
		stopped int64
		errs    []error
	)
	for _, orgID := range orgs {
		if err := w.wallets.ProviderDelisted(ctx, orgID); err != nil {
			errs = append(errs, fmt.Errorf("providerlisting: revoke org %s: %w", orgID, err))
			continue
		}
		stopped++
		if err := w.notify.ProviderDelisted(ctx, orgID); err != nil {
			errs = append(errs, fmt.Errorf("providerlisting: tell admins of org %s: %w", orgID, err))
		}
	}
	if stopped > 0 {
		slog.ErrorContext(ctx, "providerlisting: revoked wallets because we are off the trusted list", slog.Int64("count", stopped))
	}
	return stopped, errors.Join(errs...)
}
