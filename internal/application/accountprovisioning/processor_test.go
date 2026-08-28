package accountprovisioning

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

type testQueue struct {
	work                            Work
	found                           bool
	claimErr, completeErr, retryErr error
	completed, retried              bool
}

func (q *testQueue) Claim(context.Context, ids.CellID, time.Time, time.Duration) (Work, bool, error) {
	return q.work, q.found, q.claimErr
}
func (q *testQueue) Complete(context.Context, Work, time.Time) error {
	q.completed = true
	return q.completeErr
}
func (q *testQueue) Retry(context.Context, Work, string, time.Time) error {
	q.retried = true
	return q.retryErr
}

type testCell struct {
	err         error
	provisioned bool
}

func (c *testCell) Provision(context.Context, ids.AccountID, uint64, time.Time) error {
	c.provisioned = true
	return c.err
}

func TestProcessorProvisionsAndCompletes(t *testing.T) {
	q := &testQueue{found: true, work: Work{AccountID: ids.AccountID("10000000-0000-4000-8000-000000000001"), CellID: "cell-a", PlacementGeneration: 1}}
	c := &testCell{}
	p, err := NewProcessor(q, c, testClock{time.Now()}, DefaultLease)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := p.ProcessOne(context.Background(), "cell-a")
	if err != nil || !worked || !c.provisioned || !q.completed || q.retried {
		t.Fatalf("worked=%v err=%v cell=%v complete=%v retry=%v", worked, err, c.provisioned, q.completed, q.retried)
	}
}

func TestProcessorRetriesCellFailure(t *testing.T) {
	q := &testQueue{found: true, work: Work{AccountID: ids.AccountID("10000000-0000-4000-8000-000000000001"), CellID: "cell-a", PlacementGeneration: 1}}
	c := &testCell{err: errors.New("cell unavailable")}
	p, _ := NewProcessor(q, c, testClock{time.Now()}, DefaultLease)
	worked, err := p.ProcessOne(context.Background(), "cell-a")
	if !worked || err == nil || !q.retried || q.completed {
		t.Fatalf("worked=%v err=%v complete=%v retry=%v", worked, err, q.completed, q.retried)
	}
}

func TestProcessorNoWork(t *testing.T) {
	p, _ := NewProcessor(&testQueue{}, &testCell{}, testClock{time.Now()}, DefaultLease)
	worked, err := p.ProcessOne(context.Background(), "cell-a")
	if err != nil || worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
}
