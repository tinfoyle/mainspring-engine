package routereceiptworker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/routeretention"
)

func TestWorkerStatusIsContentFreeAndIncludesLifecycleCounters(t *testing.T) {
	processor := &processorStub{stats: routeretention.Stats{Scheduled: 8, Ready: 2, Leased: 1, Retrying: 1, OldestDueAge: 12 * time.Second}}
	worker := &Worker{processor: processor, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	worker.processed.Store(7)
	worker.pruned.Store(500)
	worker.failures.Store(1)
	value, err := worker.Status(context.Background())
	status := value.(Status)
	if err != nil || status.Scheduled != 8 || status.Ready != 2 || status.Pruned != 500 || status.OldestDueAgeSeconds != 12 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestWorkerRejectsPollBoundsBeforeOpeningDatabase(t *testing.T) {
	_, err := New(context.Background(), Config{DatabaseURL: "postgres://cell", PollInterval: time.Millisecond}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("expected unsafe poll interval to fail before database connection")
	}
}

type processorStub struct {
	result routeretention.Result
	err    error
	stats  routeretention.Stats
}

func (p *processorStub) ProcessOne(context.Context) (routeretention.Result, error) {
	return p.result, p.err
}
func (p *processorStub) Stats(context.Context) (routeretention.Stats, error) {
	if p.err != nil && errors.Is(p.err, context.Canceled) {
		return routeretention.Stats{}, p.err
	}
	return p.stats, nil
}
