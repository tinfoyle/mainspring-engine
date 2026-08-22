package baselinemaintenance

import (
	"context"
	"errors"
	"testing"
	"time"

	baselineapp "github.com/tinfoyle/spyglass-engine/internal/application/baseline"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type maintenanceClock struct{ now time.Time }

func (clock *maintenanceClock) Now() time.Time { return clock.now }

type maintenanceIDs struct{ value string }

func (generator maintenanceIDs) New() string { return generator.value }

type maintenanceQueue struct {
	claim     Claim
	found     bool
	completed int
	failed    int
	retry     bool
	code      string
}

func (queue *maintenanceQueue) Claim(context.Context, string, time.Time, time.Duration) (Claim, bool, error) {
	return queue.claim, queue.found, nil
}
func (queue *maintenanceQueue) Complete(context.Context, Claim, time.Time) error {
	queue.completed++
	return nil
}
func (queue *maintenanceQueue) Fail(_ context.Context, _ Claim, retry bool, _ time.Time, code string, _ time.Time, _ int) (string, error) {
	queue.failed, queue.retry, queue.code = queue.failed+1, retry, code
	if retry {
		return "retry", nil
	}
	return "dead_letter", nil
}
func (queue *maintenanceQueue) Stats(context.Context, time.Time) (Stats, error) { return Stats{}, nil }

type maintenanceMaterializer struct {
	command baselineapp.MaterializeMaintenanceWorkloadCommand
	err     error
}

func (materializer *maintenanceMaterializer) MaterializeMaintenanceWorkload(_ context.Context, command baselineapp.MaterializeMaintenanceWorkloadCommand) ([]workdomain.Item, error) {
	materializer.command = command
	if materializer.err != nil {
		return nil, materializer.err
	}
	return []workdomain.Item{{ID: "e5000000-0000-4000-8000-000000000005"}}, nil
}

func TestProcessorClaimsExactCycleAsWorkloadAndCompletes(t *testing.T) {
	scheduled := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	queue := &maintenanceQueue{found: true, claim: Claim{AccountID: "e1000000-0000-4000-8000-000000000001", AssessmentID: "e2000000-0000-4000-8000-000000000002", AssessmentVersion: 9, ScheduledFor: scheduled, LeaseID: "e3000000-0000-4000-8000-000000000003", Attempt: 1}}
	materializer := &maintenanceMaterializer{}
	processor, err := NewProcessor(queue, materializer, &maintenanceClock{now: scheduled.Add(-30 * 24 * time.Hour)}, maintenanceIDs{value: "e3000000-0000-4000-8000-000000000003"}, DefaultLease, DefaultMaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessOne(context.Background())
	if err != nil || !result.Completed || result.Materialized != 1 || queue.completed != 1 || materializer.command.Actor.WorkloadID != baselineapp.MaintenanceWorkloadID || materializer.command.ExpectedVersion != 9 || ids.Validate(materializer.command.CorrelationID) != nil {
		t.Fatalf("result=%+v command=%+v completed=%d err=%v", result, materializer.command, queue.completed, err)
	}
}

func TestProcessorRetriesConflictAndDeadLettersInvalid(t *testing.T) {
	for _, test := range []struct {
		name, code string
		err        error
		retry      bool
	}{
		{name: "version", code: "assessment_version_changed", err: baselineapp.ErrConflict, retry: true},
		{name: "invalid", code: "materialization_invalid", err: baselineapp.ErrInvalid, retry: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			queue := &maintenanceQueue{found: true, claim: Claim{AccountID: "f1000000-0000-4000-8000-000000000001", AssessmentID: "f2000000-0000-4000-8000-000000000002", AssessmentVersion: 1, ScheduledFor: time.Now(), LeaseID: "f3000000-0000-4000-8000-000000000003", Attempt: 1}}
			processor, _ := NewProcessor(queue, &maintenanceMaterializer{err: test.err}, &maintenanceClock{now: time.Now()}, maintenanceIDs{value: queue.claim.LeaseID}, DefaultLease, DefaultMaxAttempts)
			result, err := processor.ProcessOne(context.Background())
			if !errors.Is(err, test.err) || queue.failed != 1 || queue.retry != test.retry || queue.code != test.code || result.DeadLetter == test.retry {
				t.Fatalf("result=%+v retry=%v code=%s err=%v", result, queue.retry, queue.code, err)
			}
		})
	}
}
