package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountprovisioning"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AccountProvisionQueue struct{ pool *pgxpool.Pool }

func NewAccountProvisionQueue(pool *pgxpool.Pool) (*AccountProvisionQueue, error) {
	if pool == nil {
		return nil, errors.New("global pool is required")
	}
	return &AccountProvisionQueue{pool: pool}, nil
}

func (q *AccountProvisionQueue) Claim(ctx context.Context, cellID ids.CellID, now time.Time, lease time.Duration) (accountprovisioning.Work, bool, error) {
	var work accountprovisioning.Work
	err := q.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT account_id FROM account_cell_provision_queue
			WHERE cell_id=$1 AND ((processing_state IN ('pending','failed') AND available_at<=$2) OR
				(processing_state='processing' AND lease_expires_at<=$2))
			ORDER BY available_at,account_id FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE account_cell_provision_queue queue SET processing_state='processing',attempt_count=queue.attempt_count+1,
			lease_expires_at=$2+($3*interval '1 second'),completed_at=NULL,last_error_code=NULL,updated_at=$2
		FROM candidate WHERE queue.account_id=candidate.account_id
		RETURNING queue.account_id,queue.cell_id,queue.placement_generation,queue.attempt_count`, cellID, now.UTC(), int64(lease/time.Second)).Scan(&work.AccountID, &work.CellID, &work.PlacementGeneration, &work.AttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountprovisioning.Work{}, false, nil
	}
	return work, err == nil, err
}

func (q *AccountProvisionQueue) Complete(ctx context.Context, work accountprovisioning.Work, now time.Time) error {
	result, err := q.pool.Exec(ctx, `UPDATE account_cell_provision_queue SET processing_state='completed',lease_expires_at=NULL,completed_at=$4,last_error_code=NULL,updated_at=$4 WHERE account_id=$1 AND cell_id=$2 AND placement_generation=$3 AND processing_state='processing' AND attempt_count=$5`, work.AccountID, work.CellID, work.PlacementGeneration, now.UTC(), work.AttemptCount)
	if err == nil && result.RowsAffected() != 1 {
		return errors.New("Account provisioning lease was lost")
	}
	return err
}

func (q *AccountProvisionQueue) Retry(ctx context.Context, work accountprovisioning.Work, code string, now time.Time) error {
	delay := time.Duration(1<<min(work.AttemptCount, 8)) * time.Second
	result, err := q.pool.Exec(ctx, `UPDATE account_cell_provision_queue SET processing_state='failed',available_at=$4::timestamptz+($6*interval '1 second'),lease_expires_at=NULL,completed_at=NULL,last_error_code=$5,updated_at=$4 WHERE account_id=$1 AND cell_id=$2 AND placement_generation=$3 AND processing_state='processing' AND attempt_count=$7`, work.AccountID, work.CellID, work.PlacementGeneration, now.UTC(), code, int64(delay/time.Second), work.AttemptCount)
	if err == nil && result.RowsAffected() != 1 {
		return errors.New("Account provisioning lease was lost")
	}
	return err
}

type AccountCellProvisioner struct{ cell *database.CellPool }

func NewAccountCellProvisioner(cell *database.CellPool) (*AccountCellProvisioner, error) {
	if cell == nil {
		return nil, errors.New("cell pool is required")
	}
	return &AccountCellProvisioner{cell: cell}, nil
}

func (p *AccountCellProvisioner) Provision(ctx context.Context, accountID ids.AccountID, generation uint64, now time.Time) error {
	return p.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		var existingGeneration uint64
		var state string
		err := tx.QueryRow(ctx, `SELECT placement_generation,state FROM spyglass.account_namespaces WHERE account_id=$1 FOR UPDATE`, accountID).Scan(&existingGeneration, &state)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES($1,$2,'active',$3)`, accountID, generation, now.UTC())
			return err
		}
		if err != nil {
			return err
		}
		if existingGeneration != generation || state != "active" {
			return fmt.Errorf("cell namespace conflicts with authoritative placement")
		}
		return nil
	})
}

var _ accountprovisioning.Queue = (*AccountProvisionQueue)(nil)
var _ accountprovisioning.Cell = (*AccountCellProvisioner)(nil)
