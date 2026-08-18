package workreleaseadmin

import (
	"context"
	"errors"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	testBatch       = "10000000-0000-4000-8000-000000000001"
	testAccount     = "20000000-0000-4000-8000-000000000002"
	testItem        = "30000000-0000-4000-8000-000000000003"
	testReservation = "40000000-0000-4000-8000-000000000004"
)

func TestInspectDefaultsLimitAndCreatesAuditedChange(t *testing.T) {
	store := &storeStub{records: []DeadLetter{{Target: validTarget()}}}
	service, _ := NewService(store, fixedGenerator{})
	inspection, err := service.Inspect(context.Background(), 0, " operator@example.com ", " investigate terminal release ", "production")
	if err != nil || len(inspection.DeadLetters) != 1 || inspection.AuditBatchID != testBatch || store.limit != DefaultInspectLimit || store.change.BatchID != testBatch || store.change.Actor != "operator@example.com" {
		t.Fatalf("inspection=%+v limit=%d change=%+v err=%v", inspection, store.limit, store.change, err)
	}
}

func TestRequeueRequiresExactTargetAndMeaningfulChange(t *testing.T) {
	service, _ := NewService(&storeStub{}, fixedGenerator{})
	for name, run := range map[string]func() error{
		"target": func() error {
			_, err := service.Requeue(context.Background(), Target{}, "operator", "valid recovery reason", "production")
			return err
		},
		"reason": func() error {
			_, err := service.Requeue(context.Background(), validTarget(), "operator", "short", "production")
			return err
		},
		"environment": func() error {
			_, err := service.Requeue(context.Background(), validTarget(), "operator", "valid recovery reason", "Production!")
			return err
		},
		"limit": func() error {
			_, err := service.Inspect(context.Background(), MaximumInspectLimit+1, "operator", "valid inspection reason", "production")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, ErrInvalidChange) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func validTarget() Target {
	return Target{AccountID: ids.AccountID(testAccount), WorkItemID: ids.WorkItemID(testItem), ReservationID: testReservation}
}

type storeStub struct {
	records []DeadLetter
	limit   int
	change  Change
}

func (s *storeStub) Inspect(_ context.Context, limit int, change Change) ([]DeadLetter, error) {
	s.limit, s.change = limit, change
	return s.records, nil
}

func (s *storeStub) Requeue(_ context.Context, target Target, change Change) (DeadLetter, error) {
	s.change = change
	return DeadLetter{Target: target}, nil
}

type fixedGenerator struct{}

func (fixedGenerator) New() string { return testBatch }
