package identitymaintenance_test

import (
	"context"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/identitymaintenance"
)

func TestProcessorUsesBoundedRetentionPolicy(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	repository := &repository{}
	processor, err := identitymaintenance.NewProcessor(repository, clock{now}, 24*time.Hour, 250)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := processor.Process(context.Background()); err != nil || count != 7 {
		t.Fatalf("process=%d,%v", count, err)
	}
	if repository.now != now || repository.retention != 24*time.Hour || repository.batch != 250 {
		t.Fatalf("unexpected policy: %+v", repository)
	}
	if _, err := identitymaintenance.NewProcessor(repository, clock{now}, time.Minute, 250); err == nil {
		t.Fatal("unsafe retention was accepted")
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
	return 7, nil
}
func (r *repository) Stats(context.Context, time.Time, time.Duration) (identitymaintenance.Stats, error) {
	return identitymaintenance.Stats{}, nil
}
