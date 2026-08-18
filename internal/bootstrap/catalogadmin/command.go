package catalogadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	application "github.com/tinfoyle/spyglass-engine/internal/application/catalogadmin"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	DatabaseURL, Action, Actor, Reason string
	CatalogJSON                        []byte
	Version                            uint64
	OfferCode, StripeMode, PriceID     string
	EffectiveAt                        time.Time
	MaxDatabaseConns                   int32
}

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if config.DatabaseURL == "" || config.Action == "" || config.Actor == "" || config.Reason == "" || logger == nil {
		return errors.New("catalog operator database, action, actor, reason, and logger are required")
	}
	poolConfig, err := pgxpool.ParseConfig(config.DatabaseURL)
	if err != nil {
		return err
	}
	if config.MaxDatabaseConns > 0 {
		poolConfig.MaxConns = config.MaxDatabaseConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	service, err := application.NewService(postgres.NewCatalogAdminRepository(pool), ids.RandomGenerator{}, registration.SystemClock{})
	if err != nil {
		return err
	}
	var publication application.Publication
	switch config.Action {
	case "draft":
		content, err := decodeCatalog(config.CatalogJSON)
		if err != nil {
			return err
		}
		publication, err = service.CreateDraft(ctx, content, config.Actor, config.Reason)
	case "map-price":
		err = service.MapStripePrice(ctx, config.Version, config.OfferCode, config.StripeMode, config.PriceID, config.Actor, config.Reason)
		publication.Version = config.Version
	case "request-review":
		publication, err = service.RequestReview(ctx, config.Version, config.Actor, config.Reason)
	case "approve":
		publication, err = service.Approve(ctx, config.Version, config.Actor, config.Reason)
	case "publish":
		publication, err = service.Publish(ctx, config.Version, config.EffectiveAt, config.Actor, config.Reason)
	case "retire":
		publication, err = service.Retire(ctx, config.Version, config.Actor, config.Reason)
	default:
		return fmt.Errorf("unsupported catalog operator action %q", config.Action)
	}
	if err != nil {
		return err
	}
	logger.Info("Spyglass catalog operator action complete", "action", config.Action, "catalog_version", publication.Version, "state", publication.State, "actor", config.Actor)
	return nil
}

func decodeCatalog(raw []byte) (catalog.PublishedCatalog, error) {
	if len(raw) == 0 {
		return catalog.PublishedCatalog{}, errors.New("catalog JSON is required for draft creation")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var content catalog.PublishedCatalog
	if err := decoder.Decode(&content); err != nil {
		return catalog.PublishedCatalog{}, fmt.Errorf("decode catalog JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return catalog.PublishedCatalog{}, errors.New("catalog JSON must contain exactly one document")
	}
	return content, nil
}
