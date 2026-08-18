package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/entitlementrollout"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type EntitlementRolloutRepository struct{ pool *pgxpool.Pool }

func NewEntitlementRolloutRepository(pool *pgxpool.Pool) *EntitlementRolloutRepository {
	return &EntitlementRolloutRepository{pool: pool}
}

func (r *EntitlementRolloutRepository) EnsureRepairRollout(ctx context.Context, id string, now time.Time) (bool, error) {
	command, err := r.pool.Exec(ctx, `
		WITH effective_publication AS (
		  SELECT version,published_at FROM catalog_publications
		  WHERE state='published' AND published_at<=$2
		  ORDER BY published_at DESC,version DESC LIMIT 1
		)
		INSERT INTO entitlement_catalog_rollouts
		(id,target_catalog_version,source,state,effective_at,created_at)
		SELECT $1,c.version,'drift_repair','pending',$2,$2 FROM effective_publication c
		WHERE EXISTS (SELECT 1 FROM accounts a WHERE a.last_catalog_reconciled_version<>c.version)
		  AND NOT EXISTS (
		    SELECT 1 FROM entitlement_catalog_rollouts r
		    WHERE r.target_catalog_version=c.version AND r.state IN ('pending','seeding','processing','failed'))
		ON CONFLICT DO NOTHING`, id, now.UTC())
	return command.RowsAffected() == 1, err
}

func (r *EntitlementRolloutRepository) SeedBatch(ctx context.Context, now time.Time, limit int) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var rolloutID string
	var catalogVersion uint64
	var cursorTime *time.Time
	var cursorAccount *ids.AccountID
	var seeded, completed, failed int64
	err = tx.QueryRow(ctx, `
		SELECT id,target_catalog_version,cursor_created_at,cursor_account_id,seeded_count,completed_count,failed_count
		FROM entitlement_catalog_rollouts
		WHERE state IN ('pending','seeding') AND effective_at<=$1
		ORDER BY effective_at,created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`, now.UTC()).Scan(
		&rolloutID, &catalogVersion, &cursorTime, &cursorAccount, &seeded, &completed, &failed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE entitlement_catalog_rollouts SET state='seeding' WHERE id=$1`, rolloutID); err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id,created_at FROM accounts
		WHERE last_catalog_reconciled_version<>$1
		  AND ($2::timestamptz IS NULL OR (created_at,id)>($2,$3::uuid))
		ORDER BY created_at,id LIMIT $4`, catalogVersion, cursorTime, cursorAccount, limit)
	if err != nil {
		return false, err
	}
	type candidate struct {
		id      ids.AccountID
		created time.Time
	}
	candidates := make([]candidate, 0, limit)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.created); err != nil {
			rows.Close()
			return false, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()
	for _, item := range candidates {
		if _, err := tx.Exec(ctx, `INSERT INTO entitlement_recompute_queue (rollout_id,account_id,processing_state,created_at) VALUES ($1,$2,'pending',$3) ON CONFLICT DO NOTHING`, rolloutID, item.id, now.UTC()); err != nil {
			return false, err
		}
	}
	seeded += int64(len(candidates))
	if len(candidates) == limit {
		last := candidates[len(candidates)-1]
		_, err = tx.Exec(ctx, `UPDATE entitlement_catalog_rollouts SET cursor_created_at=$2,cursor_account_id=$3,seeded_count=$4 WHERE id=$1`, rolloutID, last.created.UTC(), last.id, seeded)
	} else {
		state := "processing"
		var completedAt any
		if completed+failed >= seeded {
			state, completedAt = "completed", now.UTC()
			if failed > 0 {
				state = "failed"
			}
		}
		_, err = tx.Exec(ctx, `UPDATE entitlement_catalog_rollouts SET state=$2,seeded_count=$3,seeded_at=$4,completed_at=$5 WHERE id=$1`, rolloutID, state, seeded, now.UTC(), completedAt)
	}
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (r *EntitlementRolloutRepository) Claim(ctx context.Context, now time.Time, lease time.Duration) (entitlementrollout.Work, bool, error) {
	var work entitlementrollout.Work
	err := r.pool.QueryRow(ctx, `
		WITH candidate AS (
		  SELECT q.rollout_id,q.account_id FROM entitlement_recompute_queue q
		  JOIN entitlement_catalog_rollouts r ON r.id=q.rollout_id
		  WHERE r.state IN ('seeding','processing') AND (
		    (q.processing_state IN ('pending','failed') AND COALESCE(q.next_attempt_at,q.created_at)<=$1)
		    OR (q.processing_state='processing' AND q.lease_expires_at<=$1))
		  ORDER BY COALESCE(q.next_attempt_at,q.created_at),q.rollout_id,q.account_id
		  FOR UPDATE OF q SKIP LOCKED LIMIT 1
		)
		UPDATE entitlement_recompute_queue q
		SET processing_state='processing',attempt_count=q.attempt_count+1,
		    lease_expires_at=$1+($2*interval '1 second'),last_error_code=NULL
		FROM candidate c,entitlement_catalog_rollouts r
		WHERE q.rollout_id=c.rollout_id AND q.account_id=c.account_id AND r.id=q.rollout_id
		RETURNING q.rollout_id,q.account_id,r.target_catalog_version,q.attempt_count`, now.UTC(), int64(lease/time.Second)).Scan(
		&work.RolloutID, &work.AccountID, &work.CatalogVersion, &work.AttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return entitlementrollout.Work{}, false, nil
	}
	return work, err == nil, err
}

func (r *EntitlementRolloutRepository) Apply(ctx context.Context, work entitlementrollout.Work, now time.Time, build func(entitlementrollout.Input) (entitlementrollout.Output, error)) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var queueState string
	var attemptCount int
	if err := tx.QueryRow(ctx, `SELECT processing_state,attempt_count FROM entitlement_recompute_queue WHERE rollout_id=$1 AND account_id=$2 FOR UPDATE`, work.RolloutID, work.AccountID).Scan(&queueState, &attemptCount); err != nil {
		return err
	}
	if queueState != "processing" || attemptCount != work.AttemptCount {
		return errors.New("entitlement rollout lease was lost")
	}
	var currentVersion, reconciledVersion uint64
	if err := tx.QueryRow(ctx, `SELECT entitlement_version,last_catalog_reconciled_version FROM accounts WHERE id=$1 FOR UPDATE`, work.AccountID).Scan(&currentVersion, &reconciledVersion); err != nil {
		return err
	}
	if reconciledVersion == work.CatalogVersion {
		return completeEntitlementWork(ctx, tx, work, now)
	}
	var raw []byte
	var publishedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT content,published_at FROM catalog_publications WHERE version=$1`, work.CatalogVersion).Scan(&raw, &publishedAt); err != nil {
		return err
	}
	var publication catalog.PublishedCatalog
	if err := json.Unmarshal(raw, &publication); err != nil {
		return err
	}
	if publishedAt != nil {
		publication.PublishedAt = publishedAt.UTC()
	}
	if err := publication.Validate(); err != nil {
		return errors.Join(entitlementrollout.ErrInvalidCatalog, err)
	}
	allGrants, err := loadGrants(ctx, tx, work.AccountID)
	if err != nil {
		return err
	}
	other := make([]entitlements.Grant, 0, len(allGrants))
	for _, grant := range allGrants {
		if grant.Source != entitlements.SourceFreePlan {
			other = append(other, grant)
		}
	}
	output, err := build(entitlementrollout.Input{AccountID: work.AccountID, CurrentVersion: currentVersion, Publication: publication, OtherGrants: other})
	if err != nil {
		return err
	}
	if output.Snapshot.AccountID != work.AccountID || output.Snapshot.CatalogVersion != work.CatalogVersion || output.Snapshot.Version != currentVersion+1 {
		return errors.New("entitlement rollout output is inconsistent")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM entitlement_grants WHERE account_id=$1 AND source='free_plan'`, work.AccountID); err != nil {
		return err
	}
	for _, grant := range output.FreeGrants {
		limits, err := json.Marshal(grant.Limits)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO entitlement_grants
			(id,account_id,package_code,package_version,mode,source,source_reference,limits,starts_at,ends_at,priority,reason,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, grant.ID, grant.AccountID, grant.PackageCode, grant.PackageVersion, grant.Mode, grant.Source, grant.SourceReference, limits, grant.StartsAt, grant.EndsAt, grant.Priority, grant.Reason, now.UTC()); err != nil {
			return err
		}
	}
	packages, err := json.Marshal(output.Snapshot.Packages)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(packages)
	var previous []byte
	err = tx.QueryRow(ctx, `SELECT source_hash FROM entitlement_snapshots WHERE account_id=$1 ORDER BY version DESC LIMIT 1`, work.AccountID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if bytes.Equal(previous, hash[:]) {
		if _, err := tx.Exec(ctx, `UPDATE accounts SET last_catalog_reconciled_version=$2 WHERE id=$1`, work.AccountID, work.CatalogVersion); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE accounts SET entitlement_version=$2,last_catalog_reconciled_version=$3 WHERE id=$1`, work.AccountID, output.Snapshot.Version, work.CatalogVersion); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO entitlement_snapshots (account_id,version,catalog_version,evaluated_at,source_hash,effective_packages) VALUES ($1,$2,$3,$4,$5,$6)`, output.Snapshot.AccountID, output.Snapshot.Version, output.Snapshot.CatalogVersion, output.Snapshot.EvaluatedAt, hash[:], packages); err != nil {
			return err
		}
	}
	return completeEntitlementWork(ctx, tx, work, now)
}

func completeEntitlementWork(ctx context.Context, tx pgx.Tx, work entitlementrollout.Work, now time.Time) error {
	command, err := tx.Exec(ctx, `UPDATE entitlement_recompute_queue SET processing_state='completed',completed_at=$4,lease_expires_at=NULL,next_attempt_at=NULL,last_error_code=NULL WHERE rollout_id=$1 AND account_id=$2 AND processing_state='processing' AND attempt_count=$3`, work.RolloutID, work.AccountID, work.AttemptCount, now.UTC())
	if err != nil || command.RowsAffected() != 1 {
		if err == nil {
			err = errors.New("entitlement rollout lease was lost")
		}
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE entitlement_catalog_rollouts
		SET completed_count=completed_count+1,
		    state=CASE WHEN state='processing' AND completed_count+failed_count+1>=seeded_count
		               THEN CASE WHEN failed_count>0 THEN 'failed' ELSE 'completed' END ELSE state END,
		    completed_at=CASE WHEN state='processing' AND completed_count+failed_count+1>=seeded_count THEN $2 ELSE completed_at END
		WHERE id=$1`, work.RolloutID, now.UTC())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *EntitlementRolloutRepository) MarkFailed(ctx context.Context, work entitlementrollout.Work, now time.Time, next time.Time, code string, terminal bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	state := "failed"
	if terminal {
		state = "dead_letter"
	}
	command, err := tx.Exec(ctx, `
		UPDATE entitlement_recompute_queue
		SET processing_state=$4,next_attempt_at=CASE WHEN $4='failed' THEN $5::timestamptz ELSE NULL END,
		    lease_expires_at=NULL,last_error_code=$6
		WHERE rollout_id=$1 AND account_id=$2 AND processing_state='processing' AND attempt_count=$3`, work.RolloutID, work.AccountID, work.AttemptCount, state, next.UTC(), code)
	if err != nil || command.RowsAffected() != 1 {
		if err == nil {
			err = errors.New("entitlement rollout lease was lost")
		}
		return err
	}
	if terminal {
		if _, err := tx.Exec(ctx, `
			UPDATE entitlement_catalog_rollouts
			SET failed_count=failed_count+1,last_error_code=$2,
			    state=CASE WHEN state='processing' AND completed_count+failed_count+1>=seeded_count THEN 'failed' ELSE state END,
			    completed_at=CASE WHEN state='processing' AND completed_count+failed_count+1>=seeded_count THEN $3 ELSE completed_at END
			WHERE id=$1`, work.RolloutID, code, now.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

var _ entitlementrollout.Store = (*EntitlementRolloutRepository)(nil)
