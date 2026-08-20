package identitymaintenanceworker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/identitymaintenance"
)

func TestRunOnceTracksPruningAndBacklog(t *testing.T) {
	stub := &processorStub{pruned: 9, stats: identitymaintenance.Stats{Total: 12, Eligible: 10, OldestEligibleAge: 49 * time.Hour}}
	worker := &Worker{processor: stub, retention: 24 * time.Hour, alertBacklog: 100, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	worker.runOnce(context.Background())
	if worker.pruned.Load() != 9 || worker.failures.Load() != 0 {
		t.Fatalf("worker counters=%d/%d", worker.pruned.Load(), worker.failures.Load())
	}
}

type processorStub struct {
	pruned int64
	stats  identitymaintenance.Stats
}

func (p *processorStub) Process(context.Context) (int64, error) { return p.pruned, nil }
func (p *processorStub) Stats(context.Context) (identitymaintenance.Stats, error) {
	return p.stats, nil
}
