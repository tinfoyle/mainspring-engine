package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/catalogadmin"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
)

type CatalogAdminRepository struct{ pool *pgxpool.Pool }

func NewCatalogAdminRepository(pool *pgxpool.Pool) *CatalogAdminRepository {
	return &CatalogAdminRepository{pool: pool}
}

func (r *CatalogAdminRepository) CreateDraft(ctx context.Context, content catalog.PublishedCatalog, change catalogadmin.Change) (catalogadmin.Publication, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return catalogadmin.Publication{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('spyglass:catalog-version',0))`); err != nil {
		return catalogadmin.Publication{}, err
	}
	var version uint64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM catalog_publications`).Scan(&version); err != nil {
		return catalogadmin.Publication{}, err
	}
	content.Version = version
	if err := content.Validate(); err != nil {
		return catalogadmin.Publication{}, errors.Join(catalogadmin.ErrInvalidChange, err)
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return catalogadmin.Publication{}, err
	}
	hash := sha256.Sum256(raw)
	publication := catalogadmin.Publication{Version: version, State: catalogadmin.StateDraft, ContentHash: hash[:], CreatedAt: change.At, CreatedBy: change.Actor, ChangeReason: change.Reason}
	if _, err := tx.Exec(ctx, `
		INSERT INTO catalog_publications
		(version,state,published_at,content,content_hash,created_at,created_by,change_reason)
		VALUES ($1,'draft',NULL,$2,$3,$4,$5,$6)`, version, raw, hash[:], change.At, change.Actor, change.Reason); err != nil {
		return catalogadmin.Publication{}, err
	}
	if err := catalogAudit(ctx, tx, change, version, "draft_created", map[string]any{"content_hash": hash[:]}); err != nil {
		return catalogadmin.Publication{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return catalogadmin.Publication{}, err
	}
	return publication, nil
}

func (r *CatalogAdminRepository) MapPrice(ctx context.Context, version uint64, offerCode, mode, priceID string, change catalogadmin.Change) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state catalogadmin.State
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT state,content FROM catalog_publications WHERE version=$1 FOR UPDATE`, version).Scan(&state, &raw); err != nil {
		return transitionError(err)
	}
	if state != catalogadmin.StateDraft {
		return catalogadmin.ErrInvalidTransition
	}
	var content catalog.PublishedCatalog
	if err := json.Unmarshal(raw, &content); err != nil {
		return err
	}
	paidOffer := false
	for _, offer := range content.Offers {
		if offer.Code == offerCode && offer.AmountMinor > 0 && offer.BillingInterval != "none" {
			paidOffer = true
			break
		}
	}
	if !paidOffer {
		return catalogadmin.ErrOfferMapping
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO offer_provider_prices
		(catalog_version,offer_code,provider,mode,provider_price_id,active,created_at)
		VALUES ($1,$2,'stripe',$3,$4,true,$5)
		ON CONFLICT (catalog_version,offer_code,provider,mode) DO UPDATE
		SET provider_price_id=EXCLUDED.provider_price_id,active=true`, version, offerCode, mode, priceID, change.At); err != nil {
		return err
	}
	if err := catalogAudit(ctx, tx, change, version, "price_mapped", map[string]any{"offer_code": offerCode, "provider": "stripe", "mode": mode, "provider_price_id": priceID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *CatalogAdminRepository) RequestReview(ctx context.Context, version uint64, change catalogadmin.Change) (catalogadmin.Publication, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return catalogadmin.Publication{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var raw []byte
	var state catalogadmin.State
	if err := tx.QueryRow(ctx, `SELECT state,content FROM catalog_publications WHERE version=$1 FOR UPDATE`, version).Scan(&state, &raw); err != nil {
		return catalogadmin.Publication{}, transitionError(err)
	}
	if state != catalogadmin.StateDraft {
		return catalogadmin.Publication{}, catalogadmin.ErrInvalidTransition
	}
	complete, err := offerMappingsComplete(ctx, tx, version, raw)
	if err != nil {
		return catalogadmin.Publication{}, err
	}
	if !complete {
		return catalogadmin.Publication{}, catalogadmin.ErrOfferMapping
	}
	row := tx.QueryRow(ctx, `
		UPDATE catalog_publications SET state='in_review',review_requested_at=$2,review_requested_by=$3
		WHERE version=$1 RETURNING version,state,content_hash,created_at,created_by,published_at,COALESCE(reviewed_by,''),COALESCE(published_by,''),change_reason`, version, change.At, change.Actor)
	publication, err := scanPublication(row)
	if err != nil {
		return catalogadmin.Publication{}, err
	}
	if err := catalogAudit(ctx, tx, change, version, "review_requested", nil); err != nil {
		return catalogadmin.Publication{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return catalogadmin.Publication{}, err
	}
	return publication, nil
}

func (r *CatalogAdminRepository) Approve(ctx context.Context, version uint64, change catalogadmin.Change) (catalogadmin.Publication, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return catalogadmin.Publication{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state catalogadmin.State
	var creator string
	if err := tx.QueryRow(ctx, `SELECT state,created_by FROM catalog_publications WHERE version=$1 FOR UPDATE`, version).Scan(&state, &creator); err != nil {
		return catalogadmin.Publication{}, transitionError(err)
	}
	if state != catalogadmin.StateInReview {
		return catalogadmin.Publication{}, catalogadmin.ErrInvalidTransition
	}
	if creator == change.Actor {
		return catalogadmin.Publication{}, catalogadmin.ErrReviewSeparation
	}
	row := tx.QueryRow(ctx, `
		UPDATE catalog_publications SET state='approved',reviewed_at=$2,reviewed_by=$3
		WHERE version=$1 RETURNING version,state,content_hash,created_at,created_by,published_at,COALESCE(reviewed_by,''),COALESCE(published_by,''),change_reason`, version, change.At, change.Actor)
	publication, err := scanPublication(row)
	if err != nil {
		return catalogadmin.Publication{}, err
	}
	if err := catalogAudit(ctx, tx, change, version, "approved", nil); err != nil {
		return catalogadmin.Publication{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return catalogadmin.Publication{}, err
	}
	return publication, nil
}

func (r *CatalogAdminRepository) Publish(ctx context.Context, version uint64, effectiveAt time.Time, change catalogadmin.Change) (catalogadmin.Publication, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return catalogadmin.Publication{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state catalogadmin.State
	if err := tx.QueryRow(ctx, `SELECT state FROM catalog_publications WHERE version=$1 FOR UPDATE`, version).Scan(&state); err != nil {
		return catalogadmin.Publication{}, transitionError(err)
	}
	action := "published"
	if state == catalogadmin.StateRetired {
		action = "republished"
	} else if state != catalogadmin.StateApproved {
		return catalogadmin.Publication{}, catalogadmin.ErrInvalidTransition
	}
	row := tx.QueryRow(ctx, `
		UPDATE catalog_publications SET state='published',published_at=$2,published_by=$3,retired_at=NULL,retired_by=NULL
		WHERE version=$1 RETURNING version,state,content_hash,created_at,created_by,published_at,COALESCE(reviewed_by,''),COALESCE(published_by,''),change_reason`, version, effectiveAt, change.Actor)
	publication, err := scanPublication(row)
	if err != nil {
		return catalogadmin.Publication{}, err
	}
	if err := catalogAudit(ctx, tx, change, version, action, map[string]any{"effective_at": effectiveAt}); err != nil {
		return catalogadmin.Publication{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return catalogadmin.Publication{}, err
	}
	return publication, nil
}

func (r *CatalogAdminRepository) Retire(ctx context.Context, version uint64, change catalogadmin.Change) (catalogadmin.Publication, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return catalogadmin.Publication{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state catalogadmin.State
	if err := tx.QueryRow(ctx, `SELECT state FROM catalog_publications WHERE version=$1 FOR UPDATE`, version).Scan(&state); err != nil {
		return catalogadmin.Publication{}, transitionError(err)
	}
	if state != catalogadmin.StatePublished {
		return catalogadmin.Publication{}, catalogadmin.ErrInvalidTransition
	}
	var fallback bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_publications WHERE version<>$1 AND state='published' AND published_at<=$2)`, version, change.At).Scan(&fallback); err != nil {
		return catalogadmin.Publication{}, err
	}
	if !fallback {
		return catalogadmin.Publication{}, catalogadmin.ErrInvalidTransition
	}
	row := tx.QueryRow(ctx, `
		UPDATE catalog_publications SET state='retired',retired_at=$2,retired_by=$3
		WHERE version=$1 RETURNING version,state,content_hash,created_at,created_by,published_at,COALESCE(reviewed_by,''),COALESCE(published_by,''),change_reason`, version, change.At, change.Actor)
	publication, err := scanPublication(row)
	if err != nil {
		return catalogadmin.Publication{}, err
	}
	if err := catalogAudit(ctx, tx, change, version, "retired", nil); err != nil {
		return catalogadmin.Publication{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return catalogadmin.Publication{}, err
	}
	return publication, nil
}

func offerMappingsComplete(ctx context.Context, tx pgx.Tx, version uint64, raw []byte) (bool, error) {
	var content catalog.PublishedCatalog
	if err := json.Unmarshal(raw, &content); err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT offer_code FROM offer_provider_prices WHERE catalog_version=$1 AND active=true`, version)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	mapped := map[string]bool{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return false, err
		}
		mapped[code] = true
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	for _, offer := range content.Offers {
		if offer.AmountMinor > 0 && !mapped[offer.Code] {
			return false, nil
		}
	}
	return true, nil
}

func catalogAudit(ctx context.Context, tx pgx.Tx, change catalogadmin.Change, version uint64, action string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO catalog_operator_events (id,catalog_version,action,actor,reason,details,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, change.EventID, version, action, change.Actor, change.Reason, raw, change.At)
	return err
}

type rowScanner interface{ Scan(...any) error }

func scanPublication(row rowScanner) (catalogadmin.Publication, error) {
	var result catalogadmin.Publication
	err := row.Scan(&result.Version, &result.State, &result.ContentHash, &result.CreatedAt, &result.CreatedBy, &result.PublishedAt, &result.ReviewedBy, &result.PublishedBy, &result.ChangeReason)
	return result, err
}

func transitionError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return catalogadmin.ErrInvalidTransition
	}
	return err
}

var _ catalogadmin.Store = (*CatalogAdminRepository)(nil)
