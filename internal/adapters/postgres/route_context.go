package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type RouteContextReceiptRepository struct{ cell *database.CellPool }

func NewRouteContextReceiptRepository(cell *database.CellPool) (*RouteContextReceiptRepository, error) {
	if cell == nil {
		return nil, errors.New("cell pool is required")
	}
	return &RouteContextReceiptRepository{cell: cell}, nil
}

func (r *RouteContextReceiptRepository) Consume(ctx context.Context, claims routecontext.Claims, now time.Time) error {
	authority := claims.Authority
	err := r.cell.WithAccountTx(ctx, authority.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var generation uint64
		var state string
		if err := tx.QueryRow(ctx, `SELECT placement_generation,state FROM spyglass.account_namespaces WHERE account_id=$1`, authority.AccountID).Scan(&generation, &state); errors.Is(err, pgx.ErrNoRows) {
			return routecontext.ErrUnavailable
		} else if err != nil {
			return err
		}
		if generation != authority.PlacementGeneration {
			return routecontext.ErrPlacement
		}
		if state != "active" {
			if (state != "draining" && state != "frozen") || (claims.Binding.Method != "GET" && claims.Binding.Method != "HEAD") {
				return routecontext.ErrUnavailable
			}
		}
		targetDigest := sha256.Sum256([]byte(claims.Binding.Target))
		bodyDigest, err := hex.DecodeString(claims.Binding.BodySHA256)
		if err != nil || len(bodyDigest) != sha256.Size {
			return routecontext.ErrInvalid
		}
		result, err := tx.Exec(ctx, `INSERT INTO spyglass.route_context_receipts
			(account_id,request_id,placement_generation,entitlement_version,actor_kind,actor_id,method,target_sha256,body_sha256,issued_at,expires_at,consumed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (account_id,request_id) DO NOTHING`,
			authority.AccountID, authority.RequestID, authority.PlacementGeneration, authority.EntitlementVersion, authority.ActorKind, authority.ActorID, claims.Binding.Method, targetDigest[:], bodyDigest, time.Unix(claims.IssuedAt, 0).UTC(), time.Unix(claims.ExpiresAt, 0).UTC(), now.UTC())
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return routecontext.ErrReplay
		}
		return nil
	})
	if err == nil || errors.Is(err, routecontext.ErrReplay) || errors.Is(err, routecontext.ErrPlacement) || errors.Is(err, routecontext.ErrUnavailable) || errors.Is(err, routecontext.ErrInvalid) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "23514") {
		return fmt.Errorf("%w: route receipt constraint", routecontext.ErrInvalid)
	}
	return fmt.Errorf("%w: %v", routecontext.ErrReceiptStore, err)
}

var _ routecontext.ReceiptStore = (*RouteContextReceiptRepository)(nil)
