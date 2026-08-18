package runnercontroller

import (
	"context"
	"errors"
	"testing"

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

type processorStub struct {
	completed               int
	launched                bool
	reconcileErr, launchErr error
	batch, launchCalls      int
}

func (p *processorStub) ProcessOne(context.Context) (bool, error) {
	p.launchCalls++
	return p.launched, p.launchErr
}
func (p *processorStub) ReconcileLaunched(_ context.Context, batch int) (int, error) {
	p.batch = batch
	return p.completed, p.reconcileErr
}
func (*processorStub) Stats(context.Context) (runnercontrol.Stats, error) {
	return runnercontrol.Stats{}, nil
}
