package billingworker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

type processorStub struct {
	calls  int
	cancel context.CancelFunc
}

func (p *processorStub) ProcessOne(context.Context) (bool, error) {
	p.calls++
	if p.calls == 2 && p.cancel != nil {
		p.cancel()
	}
	return p.calls == 1, nil
}

type reconcilerStub struct{ calls int }

func (r *reconcilerStub) ProcessOne(context.Context) (bool, error) { r.calls++; return false, nil }

func TestRunDrainsWorkThenStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := &processorStub{cancel: cancel}
	reconciliations := &reconcilerStub{}
	worker := &Worker{processor: events, reconciler: reconciliations, poll: time.Millisecond, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if events.calls != 2 || reconciliations.calls != 2 {
		t.Fatalf("unexpected fairness: events=%d reconciliations=%d", events.calls, reconciliations.calls)
	}
	status, err := worker.Status(context.Background())
	if err != nil || status.(Status).EventsProcessed != 1 || status.(Status).Failures != 0 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}
