package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type IntegrationExecutionRepository struct{ pool *pgxpool.Pool }

func NewIntegrationExecutionRepository(pool *pgxpool.Pool) (*IntegrationExecutionRepository, error) {
	if pool == nil {
		return nil, errors.New("Integration execution pool is required")
	}
	return &IntegrationExecutionRepository{pool: pool}, nil
}

func (repository *IntegrationExecutionRepository) Claim(ctx context.Context, attemptID ids.IntegrationAttemptID, now, leaseExpiresAt time.Time) (integrationexecution.Claim, bool, error) {
	var claim integrationexecution.Claim
	var digest []byte
	err := repository.pool.QueryRow(ctx, `SELECT account_id,execution_id,attempt_id,mode,capability,release_id,release_version,approval_id,
		connection_id,connection_revision_id,connection_revision,credential_id,credential_generation,payload_sha256,idempotency_key,lease_expires_at
		FROM public.spyglass_claim_integration_execution($1,$2,$3)`, attemptID, now.UTC(), leaseExpiresAt.UTC()).Scan(&claim.AccountID,
		&claim.ExecutionID, &claim.AttemptID, &claim.Mode, &claim.Capability, &claim.ReleaseID, &claim.ReleaseVersion, &claim.ApprovalID,
		&claim.ConnectionID, &claim.ConnectionRevisionID, &claim.ConnectionRevision, &claim.CredentialID, &claim.CredentialGeneration,
		&digest, &claim.IdempotencyKey, &claim.LeaseExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return integrationexecution.Claim{}, false, nil
	}
	if err != nil {
		return integrationexecution.Claim{}, false, errors.Join(integrationexecution.ErrUnavailable, err)
	}
	if len(digest) != len(claim.ManifestSHA256) {
		return claim, true, integrationexecution.ErrInvalid
	}
	copy(claim.ManifestSHA256[:], digest)
	return claim, true, nil
}

func (repository *IntegrationExecutionRepository) Complete(ctx context.Context, completion integrationexecution.Completion) error {
	var errorCode any
	if completion.ErrorCode != "" {
		errorCode = completion.ErrorCode
	}
	_, err := repository.pool.Exec(ctx, `SELECT public.spyglass_complete_integration_execution($1,$2,$3,$4,$5,$6,$7)`, completion.Claim.AccountID,
		completion.Claim.ExecutionID, completion.Claim.AttemptID, completion.Outcome, errorCode, completion.RetryAt, completion.CompletedAt.UTC())
	if err != nil {
		return errors.Join(integrationexecution.ErrUnavailable, err)
	}
	return nil
}

func (repository *IntegrationExecutionRepository) LoadDelivery(ctx context.Context, claim integrationexecution.Claim) (integrationexecution.DeliverySnapshot, error) {
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return integrationexecution.DeliverySnapshot{}, errors.Join(integrationexecution.ErrUnavailable, err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.account_id',$1::text,true)`, claim.AccountID); err != nil {
		return integrationexecution.DeliverySnapshot{}, errors.Join(integrationexecution.ErrUnavailable, err)
	}
	var releaseVersion uint64
	var approvalID ids.ConsequentialApprovalID
	var releaseState string
	if err := tx.QueryRow(ctx, `SELECT version,approval_id,state FROM spyglass.marketing_release_plans WHERE account_id=$1 AND id=$2`,
		claim.AccountID, claim.ReleaseID).Scan(&releaseVersion, &approvalID, &releaseState); err != nil {
		return integrationexecution.DeliverySnapshot{}, errors.Join(integrationexecution.ErrUnavailable, err)
	}
	if releaseVersion != claim.ReleaseVersion || approvalID != claim.ApprovalID || releaseState != "approved" {
		return integrationexecution.DeliverySnapshot{}, integrationexecution.ErrInvalid
	}
	var kind integrationsdomain.ConnectorKind
	if err := tx.QueryRow(ctx, `SELECT connector_kind FROM spyglass.integration_connections WHERE account_id=$1 AND id=$2`,
		claim.AccountID, claim.ConnectionID).Scan(&kind); err != nil {
		return integrationexecution.DeliverySnapshot{}, errors.Join(integrationexecution.ErrUnavailable, err)
	}
	revision, err := loadIntegrationRevision(ctx, tx, claim.AccountID, claim.ConnectionRevisionID)
	if err != nil {
		return integrationexecution.DeliverySnapshot{}, errors.Join(integrationexecution.ErrUnavailable, err)
	}
	if revision.ConnectionID != claim.ConnectionID || revision.Revision != claim.ConnectionRevision {
		return integrationexecution.DeliverySnapshot{}, integrationexecution.ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT asset_revision_id FROM spyglass.marketing_release_assets
		WHERE account_id=$1 AND release_id=$2 ORDER BY asset_revision_id`, claim.AccountID, claim.ReleaseID)
	if err != nil {
		return integrationexecution.DeliverySnapshot{}, errors.Join(integrationexecution.ErrUnavailable, err)
	}
	var assetIDs []ids.MarketingAssetRevisionID
	for rows.Next() {
		var assetID ids.MarketingAssetRevisionID
		if err := rows.Scan(&assetID); err != nil {
			rows.Close()
			return integrationexecution.DeliverySnapshot{}, errors.Join(integrationexecution.ErrUnavailable, err)
		}
		assetIDs = append(assetIDs, assetID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return integrationexecution.DeliverySnapshot{}, errors.Join(integrationexecution.ErrUnavailable, err)
	}
	snapshot := integrationexecution.DeliverySnapshot{ConnectorKind: kind, Revision: revision, Assets: make([]marketingdomain.AssetRevision, 0, len(assetIDs))}
	for _, assetID := range assetIDs {
		asset, err := loadMarketingAssetRevision(ctx, tx, claim.AccountID, assetID)
		if err != nil {
			return integrationexecution.DeliverySnapshot{}, errors.Join(integrationexecution.ErrUnavailable, err)
		}
		snapshot.Assets = append(snapshot.Assets, asset)
	}
	if err := tx.Commit(ctx); err != nil {
		return integrationexecution.DeliverySnapshot{}, errors.Join(integrationexecution.ErrUnavailable, err)
	}
	return snapshot, nil
}

var _ integrationexecution.Repository = (*IntegrationExecutionRepository)(nil)
var _ integrationexecution.DeliverySource = (*IntegrationExecutionRepository)(nil)
