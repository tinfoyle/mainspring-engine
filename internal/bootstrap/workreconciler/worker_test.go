package workreconciler

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/workreconciliation"
)

func TestWorkerDelegatesBoundedCompletedJobPruning(t *testing.T) {
	processor := &processorStub{}
	worker := &Worker{processor: processor, completedRetention: 30 * 24 * time.Hour, pruneBatch: 200, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	worker.pruneCompleted(context.Background())
	if processor.prunes.Load() == 0 || processor.retention != worker.completedRetention || processor.limit != worker.pruneBatch {
		t.Fatalf("prunes=%d retention=%v limit=%d", processor.prunes.Load(), processor.retention, processor.limit)
	}
}

func TestWorkerRejectsCleanupConfigurationOutsideProductionBounds(t *testing.T) {
	_, err := New(context.Background(), Config{CellDatabaseURL: "postgres://cell", GlobalDatabaseURL: "postgres://global", CleanupInterval: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("expected cleanup interval rejection before opening database pools")
	}
}

type processorStub struct {
	prunes    atomic.Int64
	retention time.Duration
	limit     int
}

func (*processorStub) ProcessOne(context.Context) (bool, error) { return false, nil }
func (*processorStub) Stats(context.Context) (workreconciliation.Stats, error) {
	return workreconciliation.Stats{}, nil
}
func (p *processorStub) PruneCompleted(_ context.Context, retention time.Duration, limit int) (int64, error) {
	p.retention, p.limit = retention, limit
	p.prunes.Add(1)
	return 1, nil
}
