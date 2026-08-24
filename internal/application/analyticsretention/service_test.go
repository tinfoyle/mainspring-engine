package analyticsretention_test

import (
	"context"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsretention"
)

func TestProcessorUsesBoundedRetentionPolicy(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repository := &repository{}
	processor, err := analyticsretention.NewProcessor(repository, clock{now}, 395*24*time.Hour, 750)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := processor.Process(context.Background()); err != nil || count != 11 {
		t.Fatalf("process=%d,%v", count, err)
	}
	if repository.now != now || repository.retention != 395*24*time.Hour || repository.batch != 750 {
		t.Fatalf("unexpected policy: %+v", repository)
	}
	if _, err := analyticsretention.NewProcessor(repository, clock{now}, 29*24*time.Hour, 750); err == nil {
		t.Fatal("unsafe retention was accepted")
	}
	if _, err := analyticsretention.NewProcessor(repository, clock{now}, 395*24*time.Hour, 5001); err == nil {
		t.Fatal("unsafe batch was accepted")
	}
}

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

type repository struct {
	now       time.Time
	retention time.Duration
	batch     int
}

func (r *repository) Prune(_ context.Context, now time.Time, retention time.Duration, batch int) (int64, error) {
	r.now, r.retention, r.batch = now, retention, batch
	return 11, nil
}

func (r *repository) Stats(context.Context, time.Time, time.Duration) (analyticsretention.Stats, error) {
	return analyticsretention.Stats{}, nil
}
