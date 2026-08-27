package affiliateretentionworker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateretention"
)

type fakeProcessor struct {
	count int64
	stats affiliateretention.Stats
	err   error
}

func (p fakeProcessor) Process(context.Context) (int64, error) { return p.count, p.err }
func (p fakeProcessor) Stats(context.Context) (affiliateretention.Stats, error) {
	return p.stats, p.err
}

func TestRunOncePublishesOnlyContentFreeCounters(t *testing.T) {
	worker := &Worker{processor: fakeProcessor{count: 2, stats: affiliateretention.Stats{Total: 5, Eligible: 1}},
		alertBacklog: 100, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	worker.runOnce(context.Background())
	statusValue, err := worker.Status(context.Background())
	status := statusValue.(Status)
	if err != nil || status.Total != 5 || status.Eligible != 1 || status.Minimized != 2 || status.Failures != 0 || status.Alerting {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestAlertingUsesBacklogOrOneYearOverdueBoundary(t *testing.T) {
	if !alerting(affiliateretention.Stats{Total: 10, Eligible: 10}, 10) {
		t.Fatal("Affiliate retention backlog threshold did not alert")
	}
	if !alerting(affiliateretention.Stats{Total: 1, Eligible: 1, OldestEligibleAge: overdueAlertAge + time.Second}, 10) {
		t.Fatal("overdue Affiliate retention item did not alert")
	}
	if alerting(affiliateretention.Stats{Total: 1, Eligible: 1, OldestEligibleAge: overdueAlertAge}, 10) {
		t.Fatal("Affiliate retention alert fired before either boundary")
	}
}
