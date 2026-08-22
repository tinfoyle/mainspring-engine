// Package baselinemaintenance drives the narrow unattended boundary that
// materializes deterministic renewal and reassessment Work.
package baselinemaintenance

import (
	"context"
	"errors"
	"time"

	baselineapp "github.com/tinfoyle/spyglass-engine/internal/application/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultLease       = 2 * time.Minute
	DefaultMaxAttempts = 10
	maximumRetryDelay  = time.Hour
)

var (
	ErrDependencies = errors.New("Baseline maintenance worker dependencies are invalid")
	ErrLease        = errors.New("Baseline maintenance lease was lost")
	ErrClaim        = errors.New("Baseline maintenance claim is invalid")
)

type Claim struct {
	AccountID         ids.AccountID
	AssessmentID      ids.BaselineAssessmentID
	AssessmentVersion uint64
	ScheduledFor      time.Time
	LeaseID           string
	Attempt           int
}

type Stats struct {
	Pending, Ready, Leased, Retrying, DeadLetter uint64
	OldestReadyAge                               time.Duration
}

type Queue interface {
	Claim(context.Context, string, time.Time, time.Duration) (Claim, bool, error)
	Complete(context.Context, Claim, time.Time) error
	Fail(context.Context, Claim, bool, time.Time, string, time.Time, int) (string, error)
	Stats(context.Context, time.Time) (Stats, error)
}

type Materializer interface {
	MaterializeMaintenanceWorkload(context.Context, baselineapp.MaterializeMaintenanceWorkloadCommand) ([]workdomain.Item, error)
}

type Clock interface{ Now() time.Time }

type Processor struct {
	queue       Queue
	materialize Materializer
	clock       Clock
	ids         ids.Generator
	lease       time.Duration
	maxAttempts int
}

type Result struct {
	Worked, Completed, DeadLetter bool
	Materialized                  int
}

func NewProcessor(queue Queue, materializer Materializer, clock Clock, generator ids.Generator, lease time.Duration, maxAttempts int) (*Processor, error) {
	if queue == nil || materializer == nil || clock == nil || generator == nil || lease < time.Second || lease > 30*time.Minute || maxAttempts < 1 || maxAttempts > 100 {
		return nil, ErrDependencies
	}
	return &Processor{queue: queue, materialize: materializer, clock: clock, ids: generator, lease: lease, maxAttempts: maxAttempts}, nil
}

func (processor *Processor) ProcessOne(ctx context.Context) (Result, error) {
	now := processor.clock.Now().UTC()
	claim, found, err := processor.queue.Claim(ctx, processor.ids.New(), now, processor.lease)
	if err != nil || !found {
		return Result{}, err
	}
	correlationID, err := ids.Derive(string(claim.AssessmentID), "maintenance:"+claim.ScheduledFor.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return processor.fail(ctx, claim, false, "claim_invalid", ErrClaim)
	}
	items, err := processor.materialize.MaterializeMaintenanceWorkload(ctx, baselineapp.MaterializeMaintenanceWorkloadCommand{
		Actor: access.Actor{WorkloadID: baselineapp.MaintenanceWorkloadID}, AccountID: claim.AccountID,
		AssessmentID: claim.AssessmentID, ExpectedVersion: claim.AssessmentVersion, CorrelationID: correlationID,
	})
	if err != nil {
		retry := !errors.Is(err, baselineapp.ErrInvalid) && !errors.Is(err, baselineapp.ErrConstraint) && !access.IsDenied(err, access.DenialCorruptContext)
		return processor.fail(ctx, claim, retry, maintenanceErrorCode(err), err)
	}
	if err := processor.queue.Complete(ctx, claim, processor.clock.Now().UTC()); err != nil {
		return Result{Worked: true, Materialized: len(items)}, err
	}
	return Result{Worked: true, Completed: true, Materialized: len(items)}, nil
}

func (processor *Processor) Stats(ctx context.Context) (Stats, error) {
	return processor.queue.Stats(ctx, processor.clock.Now().UTC())
}

func (processor *Processor) fail(ctx context.Context, claim Claim, retry bool, code string, cause error) (Result, error) {
	now := processor.clock.Now().UTC()
	next := now.Add(retryDelay(claim.Attempt))
	state, failErr := processor.queue.Fail(ctx, claim, retry, next, code, now, processor.maxAttempts)
	return Result{Worked: true, DeadLetter: state == "dead_letter"}, errors.Join(cause, failErr)
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second
	for index := 1; index < attempt && delay < maximumRetryDelay; index++ {
		delay *= 2
	}
	if delay > maximumRetryDelay {
		return maximumRetryDelay
	}
	return delay
}

func maintenanceErrorCode(err error) string {
	switch {
	case errors.Is(err, baselineapp.ErrInvalid):
		return "materialization_invalid"
	case errors.Is(err, baselineapp.ErrConstraint):
		return "materialization_constraint"
	case errors.Is(err, baselineapp.ErrConflict):
		return "assessment_version_changed"
	case access.IsDenied(err, access.DenialLimitExceeded):
		return "work_capacity_exceeded"
	case access.IsDenied(err, access.DenialPackageNotEntitled), access.IsDenied(err, access.DenialPackageReadOnly):
		return "package_unavailable"
	default:
		return "materialization_unavailable"
	}
}
