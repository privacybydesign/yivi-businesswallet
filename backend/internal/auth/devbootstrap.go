package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// DevBootstrap makes the first Yivi login of a local dev stack (DEV_MODE)
// whose e-mail address the wallet does not know its admin: it creates the
// user and makes them a platform admin, so a fresh database needs no
// PLATFORM_ADMIN_EMAILS entry or seeded account. Every later unknown address
// is refused as usual. Only wired when the config's DevMode is on, which
// refuses to start unless APP_BASE_URL is a localhost URL.
type DevBootstrap struct {
	db     database.DB
	users  *user.Store
	admins PlatformAdmins
}

func NewDevBootstrap(db database.DB, users *user.Store, admins PlatformAdmins) *DevBootstrap {
	return &DevBootstrap{db: db, users: users, admins: admins}
}

// Load makes an admin claimed before a restart a platform admin again.
func (b *DevBootstrap) Load(ctx context.Context) error {
	var email user.Email
	err := b.db.QueryRow(ctx, `SELECT u.email FROM dev_bootstrap_admin b JOIN users u ON u.id = b.user_id`).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("auth: load dev bootstrap admin: %w", err)
	}
	b.admins.add(email)
	return nil
}

// errBootstrapClaimed is a bootstrap already claimed, by an earlier login or a
// concurrent one.
var errBootstrapClaimed = errors.New("auth: dev bootstrap admin already claimed")

// claim creates email's user as the bootstrap admin. ok is false when someone
// already claimed it: the login is then refused as any unknown one is.
func (b *DevBootstrap) claim(ctx context.Context, email user.Email) (user.User, bool, error) {
	err := database.InTx(ctx, b.db, func(q database.Querier) error {
		var id uuid.UUID
		given, _, _ := strings.Cut(string(email), "@")
		if err := q.QueryRow(ctx, `INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, '') RETURNING id`,
			email, given).Scan(&id); err != nil {
			return fmt.Errorf("auth: create dev bootstrap admin: %w", err)
		}
		tag, err := q.Exec(ctx, `INSERT INTO dev_bootstrap_admin (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, id)
		if err != nil {
			return fmt.Errorf("auth: claim dev bootstrap admin: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return errBootstrapClaimed
		}
		return nil
	})
	if errors.Is(err, errBootstrapClaimed) {
		return user.User{}, false, nil
	}
	if err != nil {
		return user.User{}, false, err
	}
	u, err := b.users.FindByEmail(ctx, email)
	if err != nil {
		return user.User{}, false, err
	}
	b.admins.add(email)
	slog.WarnContext(ctx, "DEV_MODE: the first login became the platform admin", slog.String("user_id", u.ID.String()))
	return u, true, nil
}
