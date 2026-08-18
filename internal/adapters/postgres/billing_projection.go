package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type BillingProjectionRepository struct{ pool *pgxpool.Pool }

func NewBillingProjectionRepository(pool *pgxpool.Pool) *BillingProjectionRepository {
	return &BillingProjectionRepository{pool: pool}
}

func (r *BillingProjectionRepository) ResolveSubscription(ctx context.Context, subscription billing.ProviderSubscription) (billing.MappedOffer, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT bp.account_id,opp.catalog_version,opp.offer_code,cp.content,cp.published_at
		FROM billing_profiles bp
		JOIN offer_provider_prices opp ON opp.provider='stripe' AND opp.provider_price_id=ANY($2)
		JOIN catalog_publications cp ON cp.version=opp.catalog_version
		WHERE bp.stripe_customer_id=$1 AND opp.mode=$3`, subscription.CustomerID, subscription.PriceIDs, subscription.Mode)
	if err != nil {
		return billing.MappedOffer{}, err
	}
	defer rows.Close()
	var result billing.MappedOffer
	var raw []byte
	var publishedAt time.Time
	found := false
	for rows.Next() {
		var accountID ids.AccountID
		var version uint64
		var offerCode string
		var content []byte
		var published time.Time
		if err := rows.Scan(&accountID, &version, &offerCode, &content, &published); err != nil {
			return billing.MappedOffer{}, err
		}
		if found && (result.AccountID != accountID || result.CatalogVersion != version || result.Offer.Code != offerCode) {
			return billing.MappedOffer{}, errors.New("subscription contains conflicting Spyglass offers")
		}
		found, result.AccountID, result.CatalogVersion = true, accountID, version
		result.Offer.Code, raw, publishedAt = offerCode, content, published
	}
	if err := rows.Err(); err != nil {
		return billing.MappedOffer{}, err
	}
	if !found {
		return billing.MappedOffer{}, billing.ErrUnmappedSubscription
	}
	if subscription.AccountID != "" && subscription.AccountID != result.AccountID {
		return billing.MappedOffer{}, billing.ErrSubscriptionMismatch
	}
	var publication catalog.PublishedCatalog
	if err := json.Unmarshal(raw, &publication); err != nil {
		return billing.MappedOffer{}, err
	}
	publication.PublishedAt = publishedAt.UTC()
	if err := publication.Validate(); err != nil {
		return billing.MappedOffer{}, err
	}
	for _, offer := range publication.Offers {
		if offer.Code == result.Offer.Code {
			result.Offer = offer
			break
		}
	}
	plan, ok := publication.Plan(result.Offer.PlanCode)
	if !ok || plan.Version != result.Offer.PlanVersion {
		return billing.MappedOffer{}, billing.ErrUnmappedSubscription
	}
	result.Plan = plan
	result.Packages = make(map[catalog.PackageCode]catalog.FeaturePackage, len(publication.Packages))
	for _, item := range publication.Packages {
		result.Packages[item.Code] = item
	}
	result.Catalog = publication
	return result, nil
}

func (r *BillingProjectionRepository) ApplyProjection(ctx context.Context, projection billing.Projection) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentVersion uint64
	if err := tx.QueryRow(ctx, `SELECT entitlement_version FROM accounts WHERE id=$1 FOR UPDATE`, projection.Mapping.AccountID).Scan(&currentVersion); err != nil {
		return err
	}
	var storedAccount ids.AccountID
	err = tx.QueryRow(ctx, `
		INSERT INTO subscriptions (
			id,account_id,provider,provider_mode,provider_customer_id,provider_subscription_id,state,
			offer_code,offer_version,current_period_start,current_period_end,cancel_at,
			provider_object_version,last_synced_at,created_at,updated_at
		) VALUES (gen_random_uuid(),$1,'stripe',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12,$12)
		ON CONFLICT (provider,provider_mode,provider_subscription_id) DO UPDATE SET
			provider_customer_id=EXCLUDED.provider_customer_id,state=EXCLUDED.state,
			offer_code=EXCLUDED.offer_code,offer_version=EXCLUDED.offer_version,
			current_period_start=EXCLUDED.current_period_start,current_period_end=EXCLUDED.current_period_end,
			cancel_at=EXCLUDED.cancel_at,provider_object_version=EXCLUDED.provider_object_version,
			last_synced_at=EXCLUDED.last_synced_at,updated_at=EXCLUDED.updated_at
		WHERE subscriptions.account_id=EXCLUDED.account_id
		RETURNING account_id`, projection.Mapping.AccountID, projection.Subscription.Mode, projection.Subscription.CustomerID, projection.Subscription.ID,
		projection.Subscription.State, projection.Mapping.Offer.Code, projection.Mapping.CatalogVersion,
		nullableTime(projection.Subscription.CurrentPeriodStart), nullableTime(projection.Subscription.CurrentPeriodEnd), projection.Subscription.CancelAt,
		projection.Subscription.ObjectVersion, projection.SyncedAt.UTC()).Scan(&storedAccount)
	if errors.Is(err, pgx.ErrNoRows) {
		return billing.ErrSubscriptionMismatch
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM entitlement_grants WHERE account_id=$1 AND source='subscription' AND source_reference=$2`, projection.Mapping.AccountID, projection.Subscription.ID); err != nil {
		return err
	}
	if projection.Subscription.State == "active" || projection.Subscription.State == "trialing" || projection.Subscription.State == "past_due" || projection.Subscription.State == "incomplete" {
		if _, err := tx.Exec(ctx, `UPDATE billing_checkout_attempts SET state='expired',updated_at=$2 WHERE account_id=$1 AND provider='stripe' AND mode=$3 AND state='active'`, projection.Mapping.AccountID, projection.SyncedAt.UTC(), projection.Subscription.Mode); err != nil {
			return err
		}
	}
	for _, grant := range projection.Grants {
		limits, marshalErr := json.Marshal(grant.Limits)
		if marshalErr != nil {
			return marshalErr
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO entitlement_grants (id,account_id,package_code,package_version,mode,source,source_reference,limits,starts_at,ends_at,priority,reason,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, grant.ID, grant.AccountID, grant.PackageCode, grant.PackageVersion, grant.Mode, grant.Source, grant.SourceReference, limits, grant.StartsAt, grant.EndsAt, grant.Priority, grant.Reason, projection.SyncedAt.UTC())
		if err != nil {
			return err
		}
	}
	grants, err := loadGrants(ctx, tx, projection.Mapping.AccountID)
	if err != nil {
		return err
	}
	var currentCatalogRaw []byte
	var currentCatalogPublishedAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT content,published_at FROM catalog_publications
		WHERE state='published' AND published_at<=$1
		ORDER BY published_at DESC,version DESC LIMIT 1`, projection.SyncedAt.UTC()).Scan(&currentCatalogRaw, &currentCatalogPublishedAt); err != nil {
		return err
	}
	var currentCatalog catalog.PublishedCatalog
	if err := json.Unmarshal(currentCatalogRaw, &currentCatalog); err != nil {
		return err
	}
	currentCatalog.PublishedAt = currentCatalogPublishedAt.UTC()
	if err := currentCatalog.Validate(); err != nil {
		return err
	}
	snapshot, err := entitlements.Evaluate(projection.Mapping.AccountID, currentVersion+1, currentCatalog, grants, projection.SyncedAt)
	if err != nil {
		return err
	}
	packages, err := json.Marshal(snapshot.Packages)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(packages)
	var previous []byte
	err = tx.QueryRow(ctx, `SELECT source_hash FROM entitlement_snapshots WHERE account_id=$1 ORDER BY version DESC LIMIT 1`, projection.Mapping.AccountID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	paid := false
	for _, grant := range grants {
		if grant.Source == entitlements.SourceSubscription && !grant.StartsAt.After(projection.SyncedAt) && (grant.EndsAt == nil || grant.EndsAt.After(projection.SyncedAt)) {
			paid = true
			break
		}
	}
	accountType := "free"
	if paid {
		accountType = "paid"
	}
	if bytes.Equal(previous, hash[:]) {
		_, err = tx.Exec(ctx, `UPDATE accounts SET account_type=$2 WHERE id=$1 AND account_type IN ('free','paid')`, projection.Mapping.AccountID, accountType)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET entitlement_version=$2,account_type=$3 WHERE id=$1`, projection.Mapping.AccountID, snapshot.Version, accountType); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO entitlement_snapshots (account_id,version,catalog_version,evaluated_at,source_hash,effective_packages) VALUES ($1,$2,$3,$4,$5,$6)`, snapshot.AccountID, snapshot.Version, snapshot.CatalogVersion, snapshot.EvaluatedAt, hash[:], packages); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func loadGrants(ctx context.Context, tx pgx.Tx, accountID ids.AccountID) ([]entitlements.Grant, error) {
	rows, err := tx.Query(ctx, `
		SELECT id,account_id,package_code,package_version,mode,source,source_reference,limits,starts_at,ends_at,priority,reason
		FROM entitlement_grants WHERE account_id=$1
		ORDER BY package_code,priority DESC,source,source_reference,id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]entitlements.Grant, 0)
	for rows.Next() {
		var grant entitlements.Grant
		var limits []byte
		if err := rows.Scan(&grant.ID, &grant.AccountID, &grant.PackageCode, &grant.PackageVersion, &grant.Mode, &grant.Source, &grant.SourceReference, &limits, &grant.StartsAt, &grant.EndsAt, &grant.Priority, &grant.Reason); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(limits, &grant.Limits); err != nil {
			return nil, fmt.Errorf("decode entitlement limits: %w", err)
		}
		result = append(result, grant)
	}
	return result, rows.Err()
}

func (r *BillingProjectionRepository) QueueReconciliation(ctx context.Context, subscriptionID, reason string, now time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO billing_reconciliation_queue (provider_subscription_id,reason,requested_at,next_attempt_at)
		VALUES ($1,$2,$3,$3)
		ON CONFLICT (provider_subscription_id) DO UPDATE SET reason=EXCLUDED.reason,requested_at=EXCLUDED.requested_at,next_attempt_at=LEAST(billing_reconciliation_queue.next_attempt_at,EXCLUDED.next_attempt_at),processing_state='pending',completed_at=NULL`, subscriptionID, reason, now.UTC())
	return err
}

func (r *BillingProjectionRepository) ClaimReconciliation(ctx context.Context, now time.Time, lease time.Duration) (string, bool, error) {
	var subscriptionID string
	err := r.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT provider_subscription_id FROM billing_reconciliation_queue
			WHERE (processing_state IN ('pending','failed') AND next_attempt_at<=$1)
			   OR (processing_state='processing' AND lease_expires_at<=$1)
			ORDER BY next_attempt_at,provider_subscription_id FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE billing_reconciliation_queue q SET processing_state='processing',attempt_count=q.attempt_count+1,lease_expires_at=$1+($2*interval '1 second'),last_error_code=NULL
		FROM candidate c WHERE q.provider_subscription_id=c.provider_subscription_id RETURNING q.provider_subscription_id`, now.UTC(), int64(lease/time.Second)).Scan(&subscriptionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return subscriptionID, err == nil, err
}

func (r *BillingProjectionRepository) CompleteReconciliation(ctx context.Context, subscriptionID string, now time.Time) error {
	command, err := r.pool.Exec(ctx, `UPDATE billing_reconciliation_queue SET processing_state='completed',completed_at=$2,lease_expires_at=NULL,last_error_code=NULL WHERE provider_subscription_id=$1 AND processing_state='processing'`, subscriptionID, now.UTC())
	if err == nil && command.RowsAffected() != 1 {
		return errors.New("billing reconciliation lease was lost")
	}
	return err
}

func (r *BillingProjectionRepository) FailReconciliation(ctx context.Context, subscriptionID string, next time.Time, code string) error {
	command, err := r.pool.Exec(ctx, `UPDATE billing_reconciliation_queue SET processing_state='failed',next_attempt_at=$2,lease_expires_at=NULL,last_error_code=$3 WHERE provider_subscription_id=$1 AND processing_state='processing'`, subscriptionID, next.UTC(), code)
	if err == nil && command.RowsAffected() != 1 {
		return errors.New("billing reconciliation lease was lost")
	}
	return err
}

var _ billing.ProjectionRepository = (*BillingProjectionRepository)(nil)
var _ billing.ReconciliationQueue = (*BillingProjectionRepository)(nil)
