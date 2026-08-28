// Package accountprovisioningworker composes one cell-bounded Account
// namespace provisioner. Each instance can mutate only its configured cell.
package accountprovisioningworker

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountprovisioning"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	GlobalDatabaseURL string
	CellDatabaseURL   string
	CellID            ids.CellID
	MaxDatabaseConns  int32
	PollInterval      time.Duration
	Lease             time.Duration
}

type Status struct {
	Processed uint64 `json:"processed"`
	Failures  uint64 `json:"failures"`
}

type Worker struct {
	global, cell *pgxpool.Pool
	processor    *accountprovisioning.Processor
	cellID       ids.CellID
	poll         time.Duration
	logger       *slog.Logger
	processed    atomic.Uint64
	failures     atomic.Uint64
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Worker, error) {
	if config.GlobalDatabaseURL == "" || config.CellDatabaseURL == "" || config.CellID == "" || logger == nil {
		return nil, errors.New("Account provisioning worker databases, cell ID and logger are required")
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.Lease == 0 {
		config.Lease = accountprovisioning.DefaultLease
	}
	if config.PollInterval < 100*time.Millisecond || config.PollInterval > time.Minute {
		return nil, errors.New("Account provisioning poll interval is out of bounds")
	}
	global, err := openPool(ctx, config.GlobalDatabaseURL, config.MaxDatabaseConns)
	if err != nil {
		return nil, err
	}
	cell, err := openPool(ctx, config.CellDatabaseURL, config.MaxDatabaseConns)
	if err != nil {
		global.Close()
		return nil, err
	}
	cellPool, err := database.NewCellPool(cell)
	if err != nil {
		global.Close()
		cell.Close()
		return nil, err
	}
	queue, err := postgres.NewAccountProvisionQueue(global)
	if err != nil {
		global.Close()
		cell.Close()
		return nil, err
	}
	provisioner, err := postgres.NewAccountCellProvisioner(cellPool)
	if err != nil {
		global.Close()
		cell.Close()
		return nil, err
	}
	processor, err := accountprovisioning.NewProcessor(queue, provisioner, registration.SystemClock{}, config.Lease)
	if err != nil {
		global.Close()
		cell.Close()
		return nil, err
	}
	return &Worker{global: global, cell: cell, processor: processor, cellID: config.CellID, poll: config.PollInterval, logger: logger}, nil
}

func openPool(ctx context.Context, databaseURL string, maximum int32) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	if maximum > 0 {
		config.MaxConns = maximum
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		worked, err := w.processor.ProcessOne(ctx, w.cellID)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.failures.Add(1)
			w.logger.Error("Account cell provisioning failed", "cell_id", w.cellID, "error", err)
		}
		if worked {
			w.processed.Add(1)
			continue
		}
		timer := time.NewTimer(w.poll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (w *Worker) Ready(ctx context.Context) error {
	if err := w.global.Ping(ctx); err != nil {
		return err
	}
	return w.cell.Ping(ctx)
}
func (w *Worker) Status(context.Context) (any, error) {
	return Status{Processed: w.processed.Load(), Failures: w.failures.Load()}, nil
}
func (w *Worker) Close() { w.global.Close(); w.cell.Close() }
