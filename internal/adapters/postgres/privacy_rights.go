package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/privacyrights"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type PrivacyRightsRepository struct{ pool *pgxpool.Pool }

func NewPrivacyRightsRepository(pool *pgxpool.Pool) *PrivacyRightsRepository {
	return &PrivacyRightsRepository{pool: pool}
}

func (r *PrivacyRightsRepository) Create(ctx context.Context, request privacy.RightsRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	eventID, err := ids.Derive(string(request.ID), "privacy-rights-submitted")
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO privacy_rights_requests
			(request_id,user_id,version,kind,scope,state,verified_at,requested_at,response_due_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, request.ID, request.UserID, request.Version, request.Kind, request.Scope,
		request.State, request.VerifiedAt, request.RequestedAt, request.ResponseDueAt, request.UpdatedAt)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return privacyrights.ErrAlreadyOpen
		}
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO privacy_rights_request_events (event_id,request_id,action,state,version,occurred_at) VALUES ($1,$2,'submitted',$3,$4,$5)`,
		eventID, request.ID, request.State, request.Version, request.RequestedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PrivacyRightsRepository) List(ctx context.Context, userID ids.UserID, limit int) ([]privacy.RightsRequest, error) {
	if limit < 1 || limit > 100 {
		return nil, privacy.ErrInvalidRightsRequest
	}
	rows, err := r.pool.Query(ctx, `
		SELECT request_id,user_id,version,kind,scope,state,verified_at,requested_at,response_due_at,updated_at
		FROM privacy_rights_requests WHERE user_id=$1
		ORDER BY requested_at DESC,request_id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requests := make([]privacy.RightsRequest, 0)
	for rows.Next() {
		var request privacy.RightsRequest
		if err := rows.Scan(&request.ID, &request.UserID, &request.Version, &request.Kind, &request.Scope, &request.State, &request.VerifiedAt,
			&request.RequestedAt, &request.ResponseDueAt, &request.UpdatedAt); err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (r *PrivacyRightsRepository) Cancel(ctx context.Context, requestID ids.PrivacyRightsRequestID, userID ids.UserID, now time.Time) (privacy.RightsRequest, error) {
	eventID, err := ids.Derive(string(requestID), "privacy-rights-canceled")
	if err != nil {
		return privacy.RightsRequest{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return privacy.RightsRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var request privacy.RightsRequest
	err = tx.QueryRow(ctx, `
		SELECT request_id,user_id,version,kind,scope,state,verified_at,requested_at,response_due_at,updated_at
		FROM privacy_rights_requests WHERE request_id=$1 AND user_id=$2 FOR UPDATE`, requestID, userID).Scan(
		&request.ID, &request.UserID, &request.Version, &request.Kind, &request.Scope, &request.State, &request.VerifiedAt,
		&request.RequestedAt, &request.ResponseDueAt, &request.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return privacy.RightsRequest{}, privacyrights.ErrNotFound
	}
	if err != nil {
		return privacy.RightsRequest{}, err
	}
	if request.State != privacy.RightsSubmitted {
		return privacy.RightsRequest{}, privacyrights.ErrNotCancelable
	}
	request.State, request.Version, request.UpdatedAt = privacy.RightsCanceled, request.Version+1, now.UTC()
	if _, err := tx.Exec(ctx, `UPDATE privacy_rights_requests SET state=$1,version=$2,updated_at=$3 WHERE request_id=$4`, request.State, request.Version, request.UpdatedAt, request.ID); err != nil {
		return privacy.RightsRequest{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO privacy_rights_request_events (event_id,request_id,action,state,version,occurred_at) VALUES ($1,$2,'canceled',$3,$4,$5)`,
		eventID, request.ID, request.State, request.Version, request.UpdatedAt); err != nil {
		return privacy.RightsRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return privacy.RightsRequest{}, err
	}
	return request, nil
}

var _ privacyrights.Repository = (*PrivacyRightsRepository)(nil)
