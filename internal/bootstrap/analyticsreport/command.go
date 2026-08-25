package analyticsreport

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	application "github.com/tinfoyle/spyglass-engine/internal/application/analyticsreport"
)

type Config struct {
	DatabaseURL      string
	Query            application.Query
	MaxDatabaseConns int32
	Output           io.Writer
}

func Run(ctx context.Context, config Config) error {
	if config.DatabaseURL == "" || config.Output == nil {
		return errors.New("analytics report database and output are required")
	}
	if err := application.Validate(config.Query); err != nil {
		return err
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
	service, err := application.NewService(postgres.NewAnalyticsReportRepository(pool))
	if err != nil {
		return err
	}
	report, err := service.Report(ctx, config.Query)
	if err != nil {
		return err
	}
	return writeReport(config.Output, report)
}

func writeReport(output io.Writer, report application.Report) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
