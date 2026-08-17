package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
)

type CatalogRepository struct{ pool *pgxpool.Pool }

func NewCatalogRepository(pool *pgxpool.Pool) *CatalogRepository {
	return &CatalogRepository{pool: pool}
}

func (r *CatalogRepository) Published(ctx context.Context) (catalog.PublishedCatalog, error) {
	var raw []byte
	var publishedAt time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT content, published_at
		FROM catalog_publications
		WHERE state='published' AND published_at <= statement_timestamp()
		ORDER BY version DESC LIMIT 1`).Scan(&raw, &publishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return catalog.PublishedCatalog{}, errors.New("no published catalog is available")
	}
	if err != nil {
		return catalog.PublishedCatalog{}, err
	}
	var result catalog.PublishedCatalog
	if err := json.Unmarshal(raw, &result); err != nil {
		return catalog.PublishedCatalog{}, err
	}
	result.PublishedAt = publishedAt.UTC()
	for index := range result.Offers {
		result.Offers[index].Published = true
		if result.Offers[index].EffectiveFrom.IsZero() {
			result.Offers[index].EffectiveFrom = result.PublishedAt
		}
	}
	if result.Version == 0 {
		return catalog.PublishedCatalog{}, errors.New("published catalog has no version")
	}
	return result, nil
}
