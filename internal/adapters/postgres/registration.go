package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/placement"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type RegistrationRepository struct{ pool *pgxpool.Pool }

func NewRegistrationRepository(pool *pgxpool.Pool) *RegistrationRepository {
	return &RegistrationRepository{pool: pool}
}

func (r *RegistrationRepository) CreatePending(ctx context.Context, pending registration.Pending) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Expired challenges must not permanently reserve an email address. The
	// transaction and partial unique index also serialize concurrent retries.
	if _, err := tx.Exec(ctx, `
		UPDATE registration_challenges SET consumed_at=$2
		WHERE primary_email=$1 AND consumed_at IS NULL AND expires_at <= $2`,
		pending.User.PrimaryEmail, pending.CreatedAt); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO registration_challenges (
			id, proposed_user_id, primary_email, display_name, account_name, region,
			token_hash, expires_at, created_at
		) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9
		WHERE NOT EXISTS (SELECT 1 FROM users WHERE primary_email=$3)`,
		pending.ID, pending.User.ID, pending.User.PrimaryEmail, pending.User.DisplayName,
		pending.AccountName, pending.Region, pending.TokenHash[:], pending.ExpiresAt, pending.CreatedAt)
	if isUniqueConstraint(err, "registration_challenges_pending_email_unique") {
		return registration.ErrEmailExists
	}
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return registration.ErrEmailExists
	}
	return tx.Commit(ctx)
}

func (r *RegistrationRepository) DeletePending(ctx context.Context, id ids.RegistrationID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM registration_challenges WHERE id=$1 AND consumed_at IS NULL`, id)
	return err
}

func (r *RegistrationRepository) AvailableCells(ctx context.Context) ([]placement.Cell, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, region, state, assigned_accounts, soft_account_limit
		FROM cells
		WHERE state='active' AND assigned_accounts < soft_account_limit
		ORDER BY assigned_accounts ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cells := make([]placement.Cell, 0)
	for rows.Next() {
		var cell placement.Cell
		if err := rows.Scan(&cell.ID, &cell.Region, &cell.State, &cell.AssignedAccounts, &cell.SoftLimit); err != nil {
			return nil, err
		}
		cells = append(cells, cell)
	}
	return cells, rows.Err()
}

func (r *RegistrationRepository) Complete(ctx context.Context, tokenHash [32]byte, now time.Time, build func(registration.Pending) (registration.Provisioned, error)) (registration.Provisioned, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return registration.Provisioned{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	pending, err := loadPendingForUpdate(ctx, tx, tokenHash)
	if err != nil {
		return registration.Provisioned{}, err
	}
	if pending.ConsumedAt != nil {
		return registration.Provisioned{}, registration.ErrRegistrationConsumed
	}
	if !pending.ExpiresAt.After(now) {
		return registration.Provisioned{}, registration.ErrRegistrationExpired
	}
	provisioned, err := build(pending)
	if err != nil {
		return registration.Provisioned{}, err
	}

	capacity, err := tx.Exec(ctx, `
		UPDATE cells SET assigned_accounts=assigned_accounts+1
		WHERE id=$1 AND state='active' AND assigned_accounts < soft_account_limit`, provisioned.Assignment.CellID)
	if err != nil {
		return registration.Provisioned{}, err
	}
	if capacity.RowsAffected() != 1 {
		return registration.Provisioned{}, errors.New("selected cell no longer has placement capacity")
	}

	if err := insertProvisioned(ctx, tx, provisioned, now); err != nil {
		if isUniqueConstraint(err, "users_primary_email_unique") {
			return registration.Provisioned{}, registration.ErrEmailExists
		}
		return registration.Provisioned{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE registration_challenges SET consumed_at=$2 WHERE id=$1`, pending.ID, now.UTC()); err != nil {
		return registration.Provisioned{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return registration.Provisioned{}, err
	}
	return provisioned, nil
}

func loadPendingForUpdate(ctx context.Context, tx pgx.Tx, tokenHash [32]byte) (registration.Pending, error) {
	var pending registration.Pending
	var tokenBytes []byte
	var userID ids.UserID
	err := tx.QueryRow(ctx, `
		SELECT id, proposed_user_id, primary_email, display_name, account_name, region,
		       token_hash, expires_at, created_at, consumed_at
		FROM registration_challenges WHERE token_hash=$1 FOR UPDATE`, tokenHash[:]).Scan(
		&pending.ID, &userID, &pending.User.PrimaryEmail, &pending.User.DisplayName,
		&pending.AccountName, &pending.Region, &tokenBytes, &pending.ExpiresAt,
		&pending.CreatedAt, &pending.ConsumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return registration.Pending{}, registration.ErrRegistrationNotFound
	}
	if err != nil {
		return registration.Pending{}, err
	}
	if len(tokenBytes) != sha256.Size {
		return registration.Pending{}, errors.New("registration token hash is corrupt")
	}
	copy(pending.TokenHash[:], tokenBytes)
	pending.User.ID = userID
	pending.User.State = identity.UserPendingVerification
	pending.User.SecurityVersion = 1
	pending.User.CreatedAt = pending.CreatedAt
	return pending, nil
}

func insertProvisioned(ctx context.Context, tx pgx.Tx, value registration.Provisioned, now time.Time) error {
	if _, err := tx.Exec(ctx, `INSERT INTO users (id,primary_email,display_name,state,email_verified_at,security_version,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, value.User.ID, value.User.PrimaryEmail, value.User.DisplayName, value.User.State, value.User.EmailVerifiedAt, value.User.SecurityVersion, value.User.CreatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO authentication_identities (user_id,provider,identifier,secret_hash,created_at,updated_at) VALUES ($1,'local',$2,$3,$4,$5)`, value.Credential.UserID, value.User.PrimaryEmail, value.Credential.PasswordHash, value.Credential.CreatedAt, value.Credential.UpdatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO accounts (id,slug,display_name,account_type,state,cell_id,placement_generation,entitlement_version,last_catalog_reconciled_version,created_by_user_id,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, value.Account.ID, value.Account.Slug, value.Account.DisplayName, value.Account.Type, value.Account.State, value.Account.CellID, value.Account.PlacementGeneration, value.Account.EntitlementVersion, value.Snapshot.CatalogVersion, value.Account.CreatedByUserID, value.Account.CreatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO memberships (id,account_id,user_id,role,state,version,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, value.Membership.ID, value.Membership.AccountID, value.Membership.UserID, value.Membership.Role, value.Membership.State, value.Membership.Version, value.Membership.CreatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO account_directory (account_id,cell_id,placement_generation,state,data_region,updated_at) SELECT $1,$2,$3,$4,region,$5 FROM cells WHERE id=$2`, value.Assignment.AccountID, value.Assignment.CellID, value.Assignment.PlacementGeneration, value.Assignment.State, now.UTC()); err != nil {
		return err
	}
	for _, grant := range value.Grants {
		limits, err := json.Marshal(grant.Limits)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO entitlement_grants (id,account_id,package_code,package_version,mode,source,source_reference,limits,starts_at,ends_at,priority,reason,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, grant.ID, grant.AccountID, grant.PackageCode, grant.PackageVersion, grant.Mode, grant.Source, grant.SourceReference, limits, grant.StartsAt, grant.EndsAt, grant.Priority, grant.Reason, now.UTC()); err != nil {
			return err
		}
	}
	packages, err := json.Marshal(value.Snapshot.Packages)
	if err != nil {
		return err
	}
	sourceHash := sha256.Sum256(packages)
	if _, err := tx.Exec(ctx, `INSERT INTO entitlement_snapshots (account_id,version,catalog_version,evaluated_at,source_hash,effective_packages) VALUES ($1,$2,$3,$4,$5,$6)`, value.Snapshot.AccountID, value.Snapshot.Version, value.Snapshot.CatalogVersion, value.Snapshot.EvaluatedAt, sourceHash[:], packages); err != nil {
		return err
	}
	return nil
}

func isUniqueConstraint(err error, names ...string) bool {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23505" {
		return false
	}
	for _, name := range names {
		if postgresError.ConstraintName == name {
			return true
		}
	}
	return false
}

var _ registration.Repository = (*RegistrationRepository)(nil)
var _ registration.CellSource = (*RegistrationRepository)(nil)
