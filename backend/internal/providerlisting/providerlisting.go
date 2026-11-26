// Package providerlisting suspends every org's wallet while we as Business Wallet
// Provider are not on the trusted list of wallet providers (Art 12(5)), as Art
// 6(2)(f) takes away the validity of every wallet we provide, and resumes them
// once we are back on it. Suspended, a wallet is read-only and its units show as
// suspended on the status list.
//
// Only a list that was read and does not name us counts: a list that cannot be
// read changes nothing, and the wallets stop only once we have been off the list
// for the whole grace period.
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

// Wallets suspends and resumes the wallets we provide. Each returns the orgs it
// changed.
type Wallets interface {
	ProviderDelisted(ctx context.Context) ([]uuid.UUID, error)
	ProviderRelisted(ctx context.Context) ([]uuid.UUID, error)
}

// Notifier tells an org's admins its wallet was suspended or resumed, and why.
type Notifier interface {
	ProviderDelisted(ctx context.Context, orgID uuid.UUID) error
	ProviderRelisted(ctx context.Context, orgID uuid.UUID) error
}

// Watcher checks the list, suspends the wallets once we are off it for grace and
// resumes them when we are back.
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

// Check reads the list once and reports how many wallets it suspended or
// resumed. While we stay off the list every run also suspends a wallet that
// became active since.
func (w *Watcher) Check(ctx context.Context) (int64, error) {
	listed, err := w.list.ProviderListed(ctx)
	if err != nil {
		return 0, fmt.Errorf("providerlisting: read trusted list: %w", err)
	}
	if listed {
		return w.listed(ctx)
	}
	missingSince, err := w.missing(ctx)
	if err != nil {
		return 0, err
	}
	if w.now().Sub(missingSince) < w.grace {
		return 0, nil
	}
	if err := w.setDelisted(ctx, true); err != nil {
		return 0, err
	}
	orgs, err := w.wallets.ProviderDelisted(ctx)
	if err != nil {
		return 0, fmt.Errorf("providerlisting: suspend wallets: %w", err)
	}
	if len(orgs) > 0 {
		slog.ErrorContext(ctx, "providerlisting: suspended wallets because we are off the trusted list", slog.Int("count", len(orgs)))
	}
	return int64(len(orgs)), w.tell(ctx, orgs, w.notify.ProviderDelisted)
}

func (w *Watcher) listed(ctx context.Context) (int64, error) {
	const update = `INSERT INTO provider_listing_state (missing_since, checked_at) VALUES (NULL, now())
		ON CONFLICT (id) DO UPDATE SET missing_since = NULL, checked_at = now()
		RETURNING delisted_at IS NOT NULL`
	var wasDelisted bool
	if err := w.db.QueryRow(ctx, update).Scan(&wasDelisted); err != nil {
		return 0, fmt.Errorf("providerlisting: record listed: %w", err)
	}
	if !wasDelisted {
		return 0, nil
	}
	orgs, err := w.wallets.ProviderRelisted(ctx)
	if err != nil {
		return 0, fmt.Errorf("providerlisting: resume wallets: %w", err)
	}
	slog.WarnContext(ctx, "providerlisting: back on the trusted list; resumed the wallets suspended while we were off it",
		slog.Int("count", len(orgs)))
	if err := w.setDelisted(ctx, false); err != nil {
		return int64(len(orgs)), err
	}
	return int64(len(orgs)), w.tell(ctx, orgs, w.notify.ProviderRelisted)
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
		slog.ErrorContext(ctx, "providerlisting: we are not on the trusted list of wallet providers; every wallet is suspended if we stay off it",
			slog.Time("suspend_at", since.Add(w.grace)))
	}
	return since, nil
}

func (w *Watcher) setDelisted(ctx context.Context, delisted bool) error {
	const update = `UPDATE provider_listing_state
		SET delisted_at = CASE WHEN $1 THEN COALESCE(delisted_at, $2) END`
	if _, err := w.db.Exec(ctx, update, delisted, w.now()); err != nil {
		return fmt.Errorf("providerlisting: record delisted %v: %w", delisted, err)
	}
	return nil
}

func (w *Watcher) tell(ctx context.Context, orgs []uuid.UUID, notify func(context.Context, uuid.UUID) error) error {
	var errs []error
	for _, orgID := range orgs {
		if err := notify(ctx, orgID); err != nil {
			errs = append(errs, fmt.Errorf("providerlisting: tell admins of org %s: %w", orgID, err))
		}
	}
	return errors.Join(errs...)
}
