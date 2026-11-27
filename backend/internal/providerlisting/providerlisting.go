// Package providerlisting revokes the validity of every wallet we provide while
// we as Business Wallet Provider are not on the trusted list of wallet providers
// (Art 12(5), Art 6(2)(f)): each wallet unit is revoked, and the wallet is
// suspended, so read-only with its data kept. Back on the list, those wallets
// are active again with a new unit; nobody registers again.
//
// Only a list that was read and does not name us counts: a list that cannot be
// read changes nothing, and the wallets stop only once we have been off the list
// for the whole grace period. We are alerted the first time we are off it; the
// orgs are told when their wallet is revoked and when it is back (Annex 6(2)
// gives 24 hours). Each notice and alert is queued in the transaction of what
// caused it and retried every check until it is sent.
package providerlisting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// noticeWarnAfter is how long a notice may stay unsent before we are alerted,
// half the 24 hours Annex 6(2) gives.
const noticeWarnAfter = 12 * time.Hour

const (
	noticeDelisted = "delisted"
	noticeRelisted = "relisted"
)

// List is the trusted list of wallet providers.
type List interface {
	Ping(ctx context.Context) error
	// ProviderListed reports whether we are on the list. An error means the list
	// could not be read, and says nothing about whether we are on it.
	ProviderListed(ctx context.Context) (bool, error)
}

// Wallets suspends and resumes the wallets we provide. Each runs then in its own
// transaction with the orgs it changed, and returns them.
type Wallets interface {
	ProviderDelisted(ctx context.Context, then func(ctx context.Context, q database.Querier, changed []uuid.UUID) error) ([]uuid.UUID, error)
	ProviderRelisted(ctx context.Context, then func(ctx context.Context, q database.Querier, resumed []uuid.UUID) error) ([]uuid.UUID, error)
}

// Notifier tells an org's admins its wallet was stopped or is back, and why.
type Notifier interface {
	ProviderDelisted(ctx context.Context, orgID uuid.UUID) error
	ProviderRelisted(ctx context.Context, orgID uuid.UUID) error
}

// Alerter tells us, Yivi, what happened. An error means it reached nobody.
type Alerter interface {
	Alert(ctx context.Context, text string) error
}

// Watcher checks the list, stops the wallets once we are off it for grace and
// brings them back when we are on it again.
type Watcher struct {
	db      database.DB
	list    List
	wallets Wallets
	notify  Notifier
	alert   Alerter
	grace   time.Duration
	now     func() time.Time
}

func NewWatcher(db database.DB, list List, wallets Wallets, notify Notifier, alert Alerter, grace time.Duration) *Watcher {
	return &Watcher{db: db, list: list, wallets: wallets, notify: notify, alert: alert, grace: grace, now: time.Now}
}

// Check reads the list once, sends the notices and alerts still due and reports
// how many wallets it suspended or resumed. While we stay off the list every run
// also suspends a wallet that became active since.
func (w *Watcher) Check(ctx context.Context) (int64, error) {
	n, err := w.check(ctx)
	return n, errors.Join(err, w.sendNotices(ctx), w.sendAlerts(ctx))
}

func (w *Watcher) check(ctx context.Context) (int64, error) {
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
	orgs, err := w.wallets.ProviderDelisted(ctx, func(ctx context.Context, q database.Querier, changed []uuid.UUID) error {
		if err := w.setDelisted(ctx, q, true); err != nil {
			return err
		}
		if len(changed) == 0 {
			return nil
		}
		if err := w.queue(ctx, q, changed, noticeDelisted); err != nil {
			return err
		}
		return w.tellUs(ctx, q, fmt.Sprintf("We are still not on the trusted list of wallet providers, so we revoked the wallet units and suspended the wallets of the orgs we provide (%d). Their admins are being mailed.", len(changed)))
	})
	if err != nil {
		return 0, fmt.Errorf("providerlisting: suspend wallets: %w", err)
	}
	return int64(len(orgs)), nil
}

func (w *Watcher) listed(ctx context.Context) (int64, error) {
	var wasDelisted bool
	err := w.db.QueryRow(ctx, `SELECT delisted_at IS NOT NULL FROM provider_listing_state`).Scan(&wasDelisted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("providerlisting: read state: %w", err)
	}
	if !wasDelisted {
		return 0, w.recordListed(ctx, w.db)
	}
	orgs, err := w.wallets.ProviderRelisted(ctx, func(ctx context.Context, q database.Querier, resumed []uuid.UUID) error {
		if err := w.recordListed(ctx, q); err != nil {
			return err
		}
		if err := w.setDelisted(ctx, q, false); err != nil {
			return err
		}
		if err := w.queue(ctx, q, resumed, noticeRelisted); err != nil {
			return err
		}
		return w.tellUs(ctx, q, fmt.Sprintf("We are back on the trusted list of wallet providers. The wallets we suspended (%d) are active again with a new wallet unit; their admins are being mailed.", len(resumed)))
	})
	if err != nil {
		return 0, fmt.Errorf("providerlisting: resume wallets: %w", err)
	}
	return int64(len(orgs)), nil
}

func (w *Watcher) recordListed(ctx context.Context, q database.Querier) error {
	const upsert = `INSERT INTO provider_listing_state (missing_since, checked_at) VALUES (NULL, $1)
		ON CONFLICT (id) DO UPDATE SET missing_since = NULL, checked_at = $1`
	if _, err := q.Exec(ctx, upsert, w.now()); err != nil {
		return fmt.Errorf("providerlisting: record listed: %w", err)
	}
	return nil
}

// missing records that we are off the list and returns since when; the first
// time, it queues the alert to us in the same transaction.
func (w *Watcher) missing(ctx context.Context) (time.Time, error) {
	const upsert = `INSERT INTO provider_listing_state (missing_since, checked_at) VALUES ($1, $1)
		ON CONFLICT (id) DO UPDATE SET missing_since = COALESCE(provider_listing_state.missing_since, $1), checked_at = $1
		RETURNING missing_since, missing_since = $1`
	var since time.Time
	err := database.InTx(ctx, w.db, func(q database.Querier) error {
		var first bool
		if err := q.QueryRow(ctx, upsert, w.now()).Scan(&since, &first); err != nil {
			return fmt.Errorf("providerlisting: record missing: %w", err)
		}
		if !first {
			return nil
		}
		return w.tellUs(ctx, q, fmt.Sprintf("We are not on the trusted list of wallet providers. If we are still off it at %s, every wallet unit is revoked and the orgs are told.",
			since.Add(w.grace).UTC().Format(time.RFC3339)))
	})
	return since, err
}

func (w *Watcher) setDelisted(ctx context.Context, q database.Querier, delisted bool) error {
	const update = `UPDATE provider_listing_state
		SET delisted_at = CASE WHEN $1 THEN COALESCE(delisted_at, $2) END`
	if _, err := q.Exec(ctx, update, delisted, w.now()); err != nil {
		return fmt.Errorf("providerlisting: record delisted %v: %w", delisted, err)
	}
	return nil
}

// queue records the notice each org is due. A newer notice replaces one not yet
// sent: back on the list, an org that never heard it was revoked only hears that
// its wallet is active again.
func (w *Watcher) queue(ctx context.Context, q database.Querier, orgs []uuid.UUID, kind string) error {
	const upsert = `INSERT INTO provider_listing_notices (organization_id, kind, queued_at)
		SELECT unnest($1::uuid[]), $2, $3
		ON CONFLICT (organization_id) DO UPDATE SET kind = EXCLUDED.kind, queued_at = EXCLUDED.queued_at, warned_at = NULL`
	if _, err := q.Exec(ctx, upsert, orgs, kind, w.now()); err != nil {
		return fmt.Errorf("providerlisting: queue %s notices: %w", kind, err)
	}
	return nil
}

// tellUs logs text as an error, so it stands out in the logs too, and queues it
// as an alert to us.
func (w *Watcher) tellUs(ctx context.Context, q database.Querier, text string) error {
	slog.ErrorContext(ctx, "providerlisting: "+text)
	if _, err := q.Exec(ctx, `INSERT INTO provider_listing_alerts (text, queued_at) VALUES ($1, $2)`, text, w.now()); err != nil {
		return fmt.Errorf("providerlisting: queue alert: %w", err)
	}
	return nil
}

type notice struct {
	orgID    uuid.UUID
	kind     string
	queuedAt time.Time
	warned   bool
}

// sendNotices sends every notice still due, and alerts us once about each that
// stays unsent for noticeWarnAfter. The rows stay locked while they are sent, so
// a second replica skips them instead of mailing twice.
func (w *Watcher) sendNotices(ctx context.Context) error {
	var failed []error
	err := database.InTx(ctx, w.db, func(q database.Querier) error {
		failed = nil
		rows, err := q.Query(ctx, `SELECT organization_id, kind, queued_at, warned_at IS NOT NULL
			FROM provider_listing_notices ORDER BY queued_at, organization_id FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return fmt.Errorf("providerlisting: read notices: %w", err)
		}
		due, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (notice, error) {
			var n notice
			return n, row.Scan(&n.orgID, &n.kind, &n.queuedAt, &n.warned)
		})
		if err != nil {
			return fmt.Errorf("providerlisting: read notices: %w", err)
		}
		var late []uuid.UUID
		for _, n := range due {
			send := w.notify.ProviderDelisted
			if n.kind == noticeRelisted {
				send = w.notify.ProviderRelisted
			}
			if err := send(ctx, n.orgID); err != nil {
				failed = append(failed, fmt.Errorf("providerlisting: tell admins of org %s: %w", n.orgID, err))
				if !n.warned && w.now().Sub(n.queuedAt) >= noticeWarnAfter {
					late = append(late, n.orgID)
				}
				continue
			}
			if _, err := q.Exec(ctx, `DELETE FROM provider_listing_notices WHERE organization_id = $1`, n.orgID); err != nil {
				return fmt.Errorf("providerlisting: mark notice to org %s sent: %w", n.orgID, err)
			}
		}
		if len(late) == 0 {
			return nil
		}
		if _, err := q.Exec(ctx, `UPDATE provider_listing_notices SET warned_at = $2 WHERE organization_id = ANY($1)`, late, w.now()); err != nil {
			return fmt.Errorf("providerlisting: record late notices: %w", err)
		}
		return w.tellUs(ctx, q, fmt.Sprintf("Orgs whose admins have still not been mailed about their wallet after %s: %d. They must be told within 24 hours; retrying every check.",
			noticeWarnAfter, len(late)))
	})
	return errors.Join(err, errors.Join(failed...))
}

// sendAlerts sends every alert to us still due, oldest first, locked like the
// notices.
func (w *Watcher) sendAlerts(ctx context.Context) error {
	var failed []error
	err := database.InTx(ctx, w.db, func(q database.Querier) error {
		failed = nil
		rows, err := q.Query(ctx, `SELECT id, text FROM provider_listing_alerts ORDER BY id FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return fmt.Errorf("providerlisting: read alerts: %w", err)
		}
		type alert struct {
			ID   int64
			Text string
		}
		due, err := pgx.CollectRows(rows, pgx.RowToStructByPos[alert])
		if err != nil {
			return fmt.Errorf("providerlisting: read alerts: %w", err)
		}
		for _, a := range due {
			if err := w.alert.Alert(ctx, a.Text); err != nil {
				failed = append(failed, fmt.Errorf("providerlisting: alert us: %w", err))
				continue
			}
			if _, err := q.Exec(ctx, `DELETE FROM provider_listing_alerts WHERE id = $1`, a.ID); err != nil {
				return fmt.Errorf("providerlisting: mark alert sent: %w", err)
			}
		}
		return nil
	})
	return errors.Join(err, errors.Join(failed...))
}
