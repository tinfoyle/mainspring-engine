package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type UsageAdmissionRepository struct{ pool *pgxpool.Pool }

func NewUsageAdmissionRepository(pool *pgxpool.Pool) *UsageAdmissionRepository {
	return &UsageAdmissionRepository{pool: pool}
}

func (r *UsageAdmissionRepository) Reserve(ctx context.Context, command usageadmission.PersistCommand) (usageadmission.Reservation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return usageadmission.Reservation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var entitlementVersion uint64
	if err := tx.QueryRow(ctx, `SELECT spyglass_lock_account_entitlement_version($1)`, command.AccountID).Scan(&entitlementVersion); err != nil {
		return usageadmission.Reservation{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO entitlement_usage_counters (account_id,package_code,limit_code,current_value,version,updated_at)
		VALUES ($1,$2,$3,0,1,$4) ON CONFLICT DO NOTHING`, command.AccountID, command.PackageCode, command.LimitCode, command.Now.UTC()); err != nil {
		return usageadmission.Reservation{}, err
	}
	var current int64
	if err := tx.QueryRow(ctx, `SELECT current_value FROM entitlement_usage_counters WHERE account_id=$1 AND package_code=$2 AND limit_code=$3 FOR UPDATE`, command.AccountID, command.PackageCode, command.LimitCode).Scan(&current); err != nil {
		return usageadmission.Reservation{}, err
	}

	expired, err := expireUsageReservations(ctx, tx, command, command.Now)
	if err != nil {
		return usageadmission.Reservation{}, err
	}
	if expired > current {
		return usageadmission.Reservation{}, usageadmission.ErrCorruptUsage
	}
	if expired > 0 {
		current -= expired
		if _, err := tx.Exec(ctx, `UPDATE entitlement_usage_counters SET current_value=$4,version=version+1,updated_at=$5 WHERE account_id=$1 AND package_code=$2 AND limit_code=$3`, command.AccountID, command.PackageCode, command.LimitCode, current, command.Now.UTC()); err != nil {
			return usageadmission.Reservation{}, err
		}
	}

	reservation, found, err := usageReservation(ctx, tx, command.AccountID, command.RequestID, true)
	if err != nil {
		return usageadmission.Reservation{}, err
	}
	if found {
		if reservation.PackageCode != command.PackageCode || reservation.LimitCode != command.LimitCode || reservation.Amount != command.Amount {
			return usageadmission.Reservation{}, usageadmission.ErrReservationConflict
		}
		reservation.Current = current
		if err := tx.Commit(ctx); err != nil {
			return usageadmission.Reservation{}, err
		}
		return reservation, nil
	}
	if entitlementVersion != command.ExpectedEntitlementVersion {
		return usageadmission.Reservation{}, usageadmission.ErrEntitlementChanged
	}
	if command.Amount <= 0 || command.Maximum <= 0 || current > command.Maximum || command.Amount > command.Maximum-current {
		return usageadmission.Reservation{}, &usageadmission.CapacityExceededError{Current: current, Maximum: command.Maximum}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO entitlement_usage_reservations
		(id,account_id,request_id,package_code,limit_code,amount,maximum_at_admission,entitlement_version,state,expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'active',$9,$10)`, command.ID, command.AccountID, command.RequestID, command.PackageCode, command.LimitCode, command.Amount, command.Maximum, command.ExpectedEntitlementVersion, command.ExpiresAt, command.Now.UTC()); err != nil {
		return usageadmission.Reservation{}, err
	}
	current += command.Amount
	if _, err := tx.Exec(ctx, `UPDATE entitlement_usage_counters SET current_value=$4,version=version+1,updated_at=$5 WHERE account_id=$1 AND package_code=$2 AND limit_code=$3`, command.AccountID, command.PackageCode, command.LimitCode, current, command.Now.UTC()); err != nil {
		return usageadmission.Reservation{}, err
	}
	reservation = usageadmission.Reservation{ID: command.ID, AccountID: command.AccountID, RequestID: command.RequestID, PackageCode: command.PackageCode, LimitCode: command.LimitCode, Amount: command.Amount, Current: current, Maximum: command.Maximum, EntitlementVersion: command.ExpectedEntitlementVersion, State: usageadmission.ReservationActive, ExpiresAt: command.ExpiresAt, CreatedAt: command.Now.UTC(), NewlyCreated: true}
	if err := tx.Commit(ctx); err != nil {
		return usageadmission.Reservation{}, err
	}
	return reservation, nil
}

func expireUsageReservations(ctx context.Context, tx pgx.Tx, command usageadmission.PersistCommand, now time.Time) (int64, error) {
	rows, err := tx.Query(ctx, `
		UPDATE entitlement_usage_reservations SET state='expired',closed_at=$4
		WHERE account_id=$1 AND package_code=$2 AND limit_code=$3 AND state='active' AND expires_at<=$4
		RETURNING amount`, command.AccountID, command.PackageCode, command.LimitCode, now.UTC())
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var amount int64
		if err := rows.Scan(&amount); err != nil {
			return 0, err
		}
		if amount > math.MaxInt64-total {
			return 0, usageadmission.ErrCorruptUsage
		}
		total += amount
	}
	return total, rows.Err()
}

func (r *UsageAdmissionRepository) Release(ctx context.Context, accountID ids.AccountID, requestID string, now time.Time) (usageadmission.Reservation, error) {
	return releaseUsageReservation(ctx, r.pool, accountID, requestID, now)
}

// UsageReleaseRepository is the worker-facing, release-only view of the
// global capacity store. Database grants remain the authority boundary.
type UsageReleaseRepository struct{ pool *pgxpool.Pool }

func NewUsageReleaseRepository(pool *pgxpool.Pool) *UsageReleaseRepository {
	return &UsageReleaseRepository{pool: pool}
}

func (r *UsageReleaseRepository) Release(ctx context.Context, accountID ids.AccountID, requestID string, now time.Time) (usageadmission.Reservation, error) {
	return releaseUsageReservation(ctx, r.pool, accountID, requestID, now)
}

func releaseUsageReservation(ctx context.Context, pool *pgxpool.Pool, accountID ids.AccountID, requestID string, now time.Time) (usageadmission.Reservation, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return usageadmission.Reservation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var accountExists int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM accounts WHERE id=$1`, accountID).Scan(&accountExists); err != nil {
		return usageadmission.Reservation{}, err
	}
	initial, found, err := usageReservation(ctx, tx, accountID, requestID, false)
	if err != nil {
		return usageadmission.Reservation{}, err
	}
	if !found {
		return usageadmission.Reservation{}, usageadmission.ErrInvalidRequest
	}
	var current int64
	if err := tx.QueryRow(ctx, `SELECT current_value FROM entitlement_usage_counters WHERE account_id=$1 AND package_code=$2 AND limit_code=$3 FOR UPDATE`, accountID, initial.PackageCode, initial.LimitCode).Scan(&current); err != nil {
		return usageadmission.Reservation{}, err
	}
	reservation, found, err := usageReservation(ctx, tx, accountID, requestID, true)
	if err != nil || !found {
		if err == nil {
			err = usageadmission.ErrCorruptUsage
		}
		return usageadmission.Reservation{}, err
	}
	if reservation.State == usageadmission.ReservationActive {
		if reservation.Amount > current {
			return usageadmission.Reservation{}, usageadmission.ErrCorruptUsage
		}
		current -= reservation.Amount
		if _, err := tx.Exec(ctx, `UPDATE entitlement_usage_counters SET current_value=$4,version=version+1,updated_at=$5 WHERE account_id=$1 AND package_code=$2 AND limit_code=$3`, accountID, reservation.PackageCode, reservation.LimitCode, current, now.UTC()); err != nil {
			return usageadmission.Reservation{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE entitlement_usage_reservations SET state='released',closed_at=$3 WHERE account_id=$1 AND request_id=$2`, accountID, requestID, now.UTC()); err != nil {
			return usageadmission.Reservation{}, err
		}
		closed := now.UTC()
		reservation.State, reservation.ClosedAt = usageadmission.ReservationReleased, &closed
	}
	reservation.Current = current
	if err := tx.Commit(ctx); err != nil {
		return usageadmission.Reservation{}, err
	}
	return reservation, nil
}

func usageReservation(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, requestID string, lock bool) (usageadmission.Reservation, bool, error) {
	query := `
		SELECT id,account_id,request_id,package_code,limit_code,amount,maximum_at_admission,
		       entitlement_version,state,expires_at,created_at,closed_at
		FROM entitlement_usage_reservations WHERE account_id=$1 AND request_id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var reservation usageadmission.Reservation
	err := tx.QueryRow(ctx, query, accountID, requestID).Scan(&reservation.ID, &reservation.AccountID, &reservation.RequestID, &reservation.PackageCode, &reservation.LimitCode, &reservation.Amount, &reservation.Maximum, &reservation.EntitlementVersion, &reservation.State, &reservation.ExpiresAt, &reservation.CreatedAt, &reservation.ClosedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return usageadmission.Reservation{}, false, nil
	}
	if err != nil {
		return usageadmission.Reservation{}, false, fmt.Errorf("load usage reservation: %w", err)
	}
	return reservation, true, nil
}

var _ usageadmission.Store = (*UsageAdmissionRepository)(nil)
