package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/spyglass-engine/internal/application/trafficreport"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type OperationsTrafficAuthorizer struct {
	pool        *pgxpool.Pool
	environment string
}

func NewOperationsTrafficAuthorizer(pool *pgxpool.Pool, environment string) *OperationsTrafficAuthorizer {
	return &OperationsTrafficAuthorizer{pool: pool, environment: environment}
}

func (a *OperationsTrafficAuthorizer) AuthorizeTrafficRead(ctx context.Context, actor ids.UserID, query trafficreport.Query, audit operations.AuditReason) error {
	_, err := a.pool.Exec(ctx, `SELECT spyglass_operations_authorize_traffic_report($1,$2,$3,$4,$5,$6,$7,$8)`, ids.RandomGenerator{}.New(), actor, query.From.UTC(), query.To.UTC(), audit.Ticket, audit.Reason, a.environment, time.Now().UTC())
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "42501" {
		return operations.ErrStaffUnauthorized
	}
	if errors.As(err, &pgError) && pgError.Code == "22023" {
		return trafficreport.ErrInvalidQuery
	}
	return err
}
