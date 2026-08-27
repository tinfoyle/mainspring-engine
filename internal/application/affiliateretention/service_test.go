package affiliateretention_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateretention"
)

type retentionClock struct{ now time.Time }

func (c retentionClock) Now() time.Time { return c.now }

type retentionStore struct {
	now   time.Time
	batch int
	count int64
	stats affiliateretention.Stats
}

func (s *retentionStore) Minimize(_ context.Context, now time.Time, batch int) (int64, error) {
	s.now, s.batch = now, batch
	return s.count, nil
}

func (s *retentionStore) Stats(_ context.Context, now time.Time) (affiliateretention.Stats, error) {
	s.now = now
	return s.stats, nil
}

func TestProcessorUsesOneUTCPolicyClockAndBoundedBatch(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.FixedZone("test", -4*60*60))
	store := &retentionStore{count: 3, stats: affiliateretention.Stats{Total: 4, Eligible: 1, OldestEligibleAge: 8 * 365 * 24 * time.Hour}}
	processor, err := affiliateretention.NewProcessor(store, retentionClock{now}, 25)
	if err != nil {
		t.Fatal(err)
	}
	count, err := processor.Process(context.Background())
	if err != nil || count != 3 || store.batch != 25 || store.now.Location() != time.UTC {
		t.Fatalf("count=%d batch=%d now=%v err=%v", count, store.batch, store.now, err)
	}
	stats, err := processor.Stats(context.Background())
	if err != nil || stats.Eligible != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
}

func TestProcessorRejectsInvalidBoundsAndStoreResults(t *testing.T) {
	if _, err := affiliateretention.NewProcessor(&retentionStore{}, retentionClock{}, 0); !errors.Is(err, affiliateretention.ErrInvalidRetention) {
		t.Fatalf("invalid batch error=%v", err)
	}
	store := &retentionStore{count: 11, stats: affiliateretention.Stats{Total: 1, Eligible: 2}}
	processor, _ := affiliateretention.NewProcessor(store, retentionClock{time.Now()}, 10)
	if _, err := processor.Process(context.Background()); !errors.Is(err, affiliateretention.ErrInvalidRetention) {
		t.Fatalf("invalid count error=%v", err)
	}
	if _, err := processor.Stats(context.Background()); !errors.Is(err, affiliateretention.ErrInvalidRetention) {
		t.Fatalf("invalid stats error=%v", err)
	}
}
