package subscriptionlifecycle_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/subscriptionlifecycle"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

type generator struct{ next int }

func (g *generator) New() string {
	g.next++
	return "00000000-0000-4000-8000-00000000000" + string(rune('0'+g.next))
}

type repository struct {
	work        subscriptionlifecycle.Work
	notice      subscriptionlifecycle.Notice
	termination subscriptionlifecycle.Termination
	advanced    bool
	emitted     []subscriptionlifecycle.PreparedNotification
	completed   bool
	failed      bool
}

func (r *repository) Claim(context.Context, time.Time, time.Duration) (subscriptionlifecycle.Work, bool, error) {
	return r.work, r.work.LifecycleID != "", nil
}
func (r *repository) Advance(_ context.Context, w subscriptionlifecycle.Work, _ time.Time, _ time.Duration) (subscriptionlifecycle.State, error) {
	r.advanced = w.LifecycleID == r.work.LifecycleID
	return subscriptionlifecycle.StateRestricted, nil
}
func (r *repository) ClaimNotice(context.Context, time.Time, time.Duration) (subscriptionlifecycle.Notice, bool, error) {
	return r.notice, r.notice.NoticeID != "", nil
}
func (r *repository) EmitNotice(_ context.Context, _ subscriptionlifecycle.Notice, p []subscriptionlifecycle.PreparedNotification, _ time.Time) error {
	r.emitted = p
	return nil
}
func (r *repository) FailNotice(context.Context, subscriptionlifecycle.Notice, time.Time, string, bool) error {
	r.failed = true
	return nil
}
func (r *repository) ClaimTermination(context.Context, time.Time, time.Duration) (subscriptionlifecycle.Termination, bool, error) {
	return r.termination, r.termination.LifecycleID != "", nil
}
func (r *repository) CompleteTermination(context.Context, subscriptionlifecycle.Termination, time.Time) error {
	r.completed = true
	return nil
}
func (r *repository) FailTermination(context.Context, subscriptionlifecycle.Termination, time.Time, string, bool) error {
	r.failed = true
	return nil
}

type preparer struct{}

func (preparer) PrepareSubscriptionLifecycle(id string, m subscriptionlifecycle.Message) (subscriptionlifecycle.PreparedNotification, error) {
	return subscriptionlifecycle.PreparedNotification{ID: id, AccountID: m.AccountID, Ciphertext: []byte("sealed"), Nonce: []byte("nonce"), KeyVersion: 1, CreatedAt: m.DueAt}, nil
}

type terminator struct {
	id, key string
	err     error
}

func (t *terminator) CancelSubscription(_ context.Context, id, key string) error {
	t.id, t.key = id, key
	return t.err
}

func TestProcessorsAdvanceNotifyAndTerminate(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("10000000-0000-4000-8000-000000000001")
	repo := &repository{work: subscriptionlifecycle.Work{LifecycleID: "20000000-0000-4000-8000-000000000002", LeaseID: "30000000-0000-4000-8000-000000000003", AccountID: accountID}, notice: subscriptionlifecycle.Notice{NoticeID: "40000000-0000-4000-8000-000000000004", LeaseID: "50000000-0000-4000-8000-000000000005", AccountID: accountID, AccountName: "Northwind", Kind: "payment_day23", DueAt: now, DeleteAt: now.Add(7 * 24 * time.Hour), Recipients: []subscriptionlifecycle.Recipient{{Email: "owner@example.com", DisplayName: "Owner"}}}, termination: subscriptionlifecycle.Termination{LifecycleID: "20000000-0000-4000-8000-000000000002", LeaseID: "60000000-0000-4000-8000-000000000006", ProviderSubscriptionID: "sub_test"}}
	processor, err := subscriptionlifecycle.NewProcessor(repo, clock{now}, 2*time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := processor.ProcessOne(context.Background()); err != nil || !worked || !repo.advanced {
		t.Fatalf("advance=%v worked=%v err=%v", repo.advanced, worked, err)
	}
	notices, err := subscriptionlifecycle.NewNoticeProcessor(repo, preparer{}, &generator{}, clock{now}, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := notices.ProcessOne(context.Background()); err != nil || !worked || len(repo.emitted) != 1 || repo.emitted[0].AccountID != accountID {
		t.Fatalf("emitted=%+v worked=%v err=%v", repo.emitted, worked, err)
	}
	provider := &terminator{}
	terminations, err := subscriptionlifecycle.NewTerminationProcessor(repo, provider, clock{now}, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := terminations.ProcessOne(context.Background()); err != nil || !worked || !repo.completed || provider.id != "sub_test" || provider.key != "subscription-lifecycle/20000000-0000-4000-8000-000000000002" {
		t.Fatalf("completed=%v provider=%+v worked=%v err=%v", repo.completed, provider, worked, err)
	}
}

func TestTerminationFailureIsPersistedWithoutProviderDetails(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	repo := &repository{termination: subscriptionlifecycle.Termination{LifecycleID: "20000000-0000-4000-8000-000000000002", LeaseID: "60000000-0000-4000-8000-000000000006", ProviderSubscriptionID: "sub_test", Attempt: 1}}
	processor, err := subscriptionlifecycle.NewTerminationProcessor(repo, &terminator{err: errors.New("provider recipient details")}, clock{now}, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := processor.ProcessOne(context.Background()); !errors.Is(err, subscriptionlifecycle.ErrProviderTermination) || !worked || !repo.failed {
		t.Fatalf("failed=%v worked=%v err=%v", repo.failed, worked, err)
	}
}
