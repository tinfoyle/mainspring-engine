package identitymaintenanceworker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsretention"
	"github.com/tinfoyle/spyglass-engine/internal/application/identitymaintenance"
)

func TestRunOnceTracksPruningAndBacklog(t *testing.T) {
	stub := &processorStub{pruned: 9, stats: identitymaintenance.Stats{Total: 12, Eligible: 10, OldestEligibleAge: 49 * time.Hour},
		networkPruned: 5, networkStats: identitymaintenance.Stats{Total: 7, Eligible: 6, OldestEligibleAge: 49 * time.Hour}}
	analyticsStub := &analyticsProcessorStub{pruned: 4, stats: analyticsretention.Stats{Total: 8, Eligible: 3, OldestEligibleAge: 800 * 24 * time.Hour}}
	worker := &Worker{processor: stub, analyticsProcessor: analyticsStub, retention: 24 * time.Hour, alertBacklog: 100,
		analyticsRetention: 395 * 24 * time.Hour, analyticsAlertBacklog: 100, networkLimitRetention: 24 * time.Hour,
		networkLimitPruneBatch: 100, networkLimitAlertBacklog: 100, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	worker.runOnce(context.Background())
	if worker.pruned.Load() != 9 || worker.failures.Load() != 0 || worker.analyticsPruned.Load() != 4 || worker.analyticsFailures.Load() != 0 ||
		worker.networkLimitsPruned.Load() != 5 || worker.networkLimitFailures.Load() != 0 {
		t.Fatalf("worker counters=%d/%d analytics=%d/%d network=%d/%d", worker.pruned.Load(), worker.failures.Load(),
			worker.analyticsPruned.Load(), worker.analyticsFailures.Load(), worker.networkLimitsPruned.Load(), worker.networkLimitFailures.Load())
	}
	statusValue, err := worker.Status(context.Background())
	status, ok := statusValue.(Status)
	if err != nil || !ok || status.TotalNetworkActorLimits != 7 || status.EligibleNetworkActorLimits != 6 ||
		status.PrunedNetworkActorLimits != 5 || status.NetworkLimitFailures != 0 {
		t.Fatalf("status=%+v ok=%v err=%v", statusValue, ok, err)
	}
}

type processorStub struct {
	pruned        int64
	stats         identitymaintenance.Stats
	networkPruned int64
	networkStats  identitymaintenance.Stats
}

func (p *processorStub) Process(context.Context) (int64, error) { return p.pruned, nil }
func (p *processorStub) Stats(context.Context) (identitymaintenance.Stats, error) {
	return p.stats, nil
}
func (p *processorStub) ProcessNetworkLimits(context.Context, time.Duration, int) (int64, error) {
	return p.networkPruned, nil
}
func (p *processorStub) NetworkLimitStats(context.Context, time.Duration) (identitymaintenance.Stats, error) {
	return p.networkStats, nil
}

type analyticsProcessorStub struct {
	pruned int64
	stats  analyticsretention.Stats
}

func (p *analyticsProcessorStub) Process(context.Context) (int64, error) { return p.pruned, nil }
func (p *analyticsProcessorStub) Stats(context.Context) (analyticsretention.Stats, error) {
	return p.stats, nil
}
