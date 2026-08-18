package runnercontroller

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
)

func TestCycleReconcilesTerminalsAndLaunchesFairWork(t *testing.T) {
	processor := &processorStub{completed: 2, launched: true}
	worker := &Worker{processor: processor, inspectionBatch: 25}
	worked, err := worker.cycle(context.Background())
	if err != nil || !worked || processor.batch != 25 || processor.launchCalls != 1 {
		t.Fatalf("worked=%v batch=%d launches=%d err=%v", worked, processor.batch, processor.launchCalls, err)
	}
}

func TestCyclePreservesBothReconcileAndLaunchFailures(t *testing.T) {
	reconcileErr, launchErr := errors.New("inspect"), errors.New("launch")
	processor := &processorStub{reconcileErr: reconcileErr, launchErr: launchErr}
	worker := &Worker{processor: processor, inspectionBatch: 10}
	if _, err := worker.cycle(context.Background()); !errors.Is(err, reconcileErr) || !errors.Is(err, launchErr) {
		t.Fatalf("cycle error=%v", err)
	}
}

func TestTerminalPayloadCleanupUsesConfiguredBounds(t *testing.T) {
	processor := &processorStub{pruned: 4}
	worker := &Worker{processor: processor, payloadRetention: 48 * time.Hour, pruneBatch: 75, logger: slog.New(slog.NewTextHandler(os.Stderr, nil))}
	worker.pruneTerminalPayloads(context.Background())
	if processor.retention != 48*time.Hour || processor.pruneBatch != 75 {
		t.Fatalf("retention=%v batch=%d", processor.retention, processor.pruneBatch)
	}
}

type processorStub struct {
	completed               int
	launched                bool
	reconcileErr, launchErr error
	batch, launchCalls      int
	pruned                  int64
	retention               time.Duration
	pruneBatch              int
}

func (p *processorStub) ProcessOne(context.Context) (bool, error) {
	p.launchCalls++
	return p.launched, p.launchErr
}
func (p *processorStub) ReconcileJobs(_ context.Context, batch int) (int, error) {
	p.batch = batch
	return p.completed, p.reconcileErr
}
func (p *processorStub) PruneTerminalPayloads(_ context.Context, retention time.Duration, batch int) (int64, error) {
	p.retention, p.pruneBatch = retention, batch
	return p.pruned, nil
}
func (*processorStub) Stats(context.Context) (runnercontrol.Stats, error) {
	return runnercontrol.Stats{}, nil
}
