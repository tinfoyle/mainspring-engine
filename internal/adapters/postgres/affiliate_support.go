package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliatesupport"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AffiliateSupportRepository struct{ pool *pgxpool.Pool }

func NewAffiliateSupportRepository(pool *pgxpool.Pool) *AffiliateSupportRepository {
	return &AffiliateSupportRepository{pool: pool}
}

func (r *AffiliateSupportRepository) EnrollmentByUser(ctx context.Context, userID ids.UserID) (affiliates.Enrollment, error) {
	return NewAffiliateProgramRepository(r.pool).EnrollmentByUser(ctx, userID)
}

func (r *AffiliateSupportRepository) CommissionBelongs(ctx context.Context, affiliateID ids.AffiliateID, entryID ids.CommissionEntryID) (bool, error) {
	var belongs bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM affiliate_commission_entries WHERE affiliate_id=$1 AND entry_id=$2
	)`, affiliateID, entryID).Scan(&belongs)
	return belongs, err
}

func (r *AffiliateSupportRepository) Create(ctx context.Context, request affiliates.SupportRequest, eventID ids.AffiliateSupportEventID) error {
	if request.Validate() != nil || ids.Validate(string(eventID)) != nil {
		return affiliates.ErrInvalidSupportRequest
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var commission any
	if request.CommissionEntryID != "" {
		commission = request.CommissionEntryID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO affiliate_support_requests
			(request_id,affiliate_id,user_id,kind,commission_entry_id,state,outcome,version,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,NULL,$7,$8,$9)`, request.ID, request.AffiliateID, request.UserID, request.Kind,
		commission, request.State, request.Version, request.CreatedAt, request.UpdatedAt)
	if err != nil {
		return classifyAffiliateSupportWriteError(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO affiliate_support_request_events
			(event_id,request_id,version,action,state,outcome,actor,reason,environment,occurred_at)
		VALUES ($1,$2,$3,'submitted',$4,NULL,$5,$6,'application',$7)`, eventID, request.ID, request.Version,
		request.State, "user:"+string(request.UserID), "Submitted structured Affiliate support request", request.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *AffiliateSupportRepository) List(ctx context.Context, userID ids.UserID) ([]affiliates.SupportRequest, error) {
	rows, err := r.pool.Query(ctx, affiliateSupportSelect+` WHERE user_id=$1 AND EXISTS (
		SELECT 1 FROM affiliate_retention_controls retention
		WHERE retention.affiliate_id=affiliate_support_requests.affiliate_id AND retention.restricted_at IS NULL
	) ORDER BY created_at DESC,request_id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]affiliates.SupportRequest, 0)
	for rows.Next() {
		value, err := scanAffiliateSupportRequest(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *AffiliateSupportRepository) Cancel(ctx context.Context, requestID ids.AffiliateSupportRequestID, userID ids.UserID, eventID ids.AffiliateSupportEventID, now time.Time) (affiliates.SupportRequest, error) {
	if ids.Validate(string(requestID)) != nil || ids.Validate(string(userID)) != nil || ids.Validate(string(eventID)) != nil || now.IsZero() {
		return affiliates.SupportRequest{}, affiliates.ErrInvalidSupportRequest
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return affiliates.SupportRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanAffiliateSupportRequest(tx.QueryRow(ctx, affiliateSupportSelect+` WHERE request_id=$1 AND user_id=$2 AND EXISTS (
		SELECT 1 FROM affiliate_retention_controls retention
		WHERE retention.affiliate_id=affiliate_support_requests.affiliate_id AND retention.restricted_at IS NULL
	) FOR UPDATE`, requestID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return affiliates.SupportRequest{}, affiliatesupport.ErrNotFound
	}
	if err != nil {
		return affiliates.SupportRequest{}, err
	}
	canceled, err := current.Cancel(now)
	if err != nil {
		return affiliates.SupportRequest{}, affiliatesupport.ErrNotCancelable
	}
	command, err := tx.Exec(ctx, `UPDATE affiliate_support_requests SET state=$2,version=$3,updated_at=$4
		WHERE request_id=$1 AND version=$5`, canceled.ID, canceled.State, canceled.Version, canceled.UpdatedAt, current.Version)
	if err != nil {
		return affiliates.SupportRequest{}, err
	}
	if command.RowsAffected() != 1 {
		return affiliates.SupportRequest{}, affiliatesupport.ErrNotCancelable
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO affiliate_support_request_events
			(event_id,request_id,version,action,state,outcome,actor,reason,environment,occurred_at)
		VALUES ($1,$2,$3,'canceled',$4,NULL,$5,$6,'application',$7)`, eventID, canceled.ID, canceled.Version,
		canceled.State, "user:"+string(userID), "Canceled Affiliate support request", canceled.UpdatedAt)
	if err != nil {
		return affiliates.SupportRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return affiliates.SupportRequest{}, err
	}
	return canceled, nil
}

const affiliateSupportSelect = `
	SELECT request_id,affiliate_id,user_id,kind,commission_entry_id::text,state,outcome,version,created_at,updated_at
	FROM affiliate_support_requests`

func scanAffiliateSupportRequest(row pgx.Row) (affiliates.SupportRequest, error) {
	var value affiliates.SupportRequest
	var commission, outcome *string
	err := row.Scan(&value.ID, &value.AffiliateID, &value.UserID, &value.Kind, &commission, &value.State, &outcome,
		&value.Version, &value.CreatedAt, &value.UpdatedAt)
	if err != nil {
		return affiliates.SupportRequest{}, err
	}
	if commission != nil {
		value.CommissionEntryID = ids.CommissionEntryID(*commission)
	}
	if outcome != nil {
		value.Outcome = affiliates.SupportOutcome(*outcome)
	}
	if err := value.Validate(); err != nil {
		return affiliates.SupportRequest{}, fmt.Errorf("invalid Affiliate support state: %w", err)
	}
	return value, nil
}

func classifyAffiliateSupportWriteError(err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23505":
			return affiliatesupport.ErrAlreadyOpen
		case "23503", "23514":
			return affiliates.ErrInvalidSupportRequest
		}
	}
	return err
}

var _ affiliatesupport.Repository = (*AffiliateSupportRepository)(nil)
