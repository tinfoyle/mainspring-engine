package integrationconnectorworker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type scriptedProcessor struct {
	results []struct {
		worked bool
		err    error
	}
}

func (processor *scriptedProcessor) ProcessOne(context.Context) (bool, error) {
	if len(processor.results) == 0 {
		return false, nil
	}
	value := processor.results[0]
	processor.results = processor.results[1:]
	return value.worked, value.err
}

func TestWorkerCountsContentFreeOutcomesAndStopsOnCancellation(t *testing.T) {
	processor := &scriptedProcessor{results: []struct {
		worked bool
		err    error
	}{{worked: true}, {worked: true, err: errors.New("connector failed")}}}
	worker := &Worker{processor: processor, poll: 10 * time.Millisecond, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := worker.Status(context.Background())
	value := status.(Status)
	if err != nil || value.Processed != 2 || value.Failures != 1 {
		t.Fatalf("status=%+v err=%v", value, err)
	}
}

func TestWorkerRejectsIncompleteCompositionBeforeDatabaseAccess(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := New(context.Background(), Config{}, nil, nil, nil, nil, logger); err == nil {
		t.Fatal("incomplete worker composition succeeded")
	}
}
