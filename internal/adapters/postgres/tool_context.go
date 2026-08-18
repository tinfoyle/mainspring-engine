package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/platform/toolcontext"
)

type ToolContextReceiptRepository struct{ pool *pgxpool.Pool }

func NewToolContextReceiptRepository(pool *pgxpool.Pool) (*ToolContextReceiptRepository, error) {
	if pool == nil {
		return nil, errors.New("global pool is required")
	}
	return &ToolContextReceiptRepository{pool: pool}, nil
}

func (r *ToolContextReceiptRepository) Consume(ctx context.Context, claims toolcontext.Claims, now time.Time) error {
	targetDigest := sha256.Sum256([]byte(claims.Binding.Target))
	bodyDigest, err := hex.DecodeString(claims.Binding.BodySHA256)
	if err != nil || len(bodyDigest) != sha256.Size {
		return toolcontext.ErrInvalid
	}
	authority := claims.Authority
	result, err := r.pool.Exec(ctx, `INSERT INTO tool_context_receipts
		(request_id,account_id,invocation_id,pod_uid,operation_id,capability,method,target_sha256,body_sha256,issued_at,expires_at,consumed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (request_id) DO NOTHING`,
		authority.RequestID, authority.AccountID, authority.InvocationID, authority.PodUID, authority.OperationID,
		authority.Capability, claims.Binding.Method, targetDigest[:], bodyDigest,
		time.Unix(claims.IssuedAt, 0).UTC(), time.Unix(claims.ExpiresAt, 0).UTC(), now.UTC())
	if err == nil {
		if result.RowsAffected() == 1 {
			return nil
		}
		return toolcontext.ErrReplay
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "23514" || pgErr.Code == "22P02") {
		return fmt.Errorf("%w: tool receipt constraint", toolcontext.ErrInvalid)
	}
	return fmt.Errorf("%w: %v", toolcontext.ErrReceiptStore, err)
}

var _ toolcontext.ReceiptStore = (*ToolContextReceiptRepository)(nil)
