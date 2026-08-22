package scheduling

import (
	"context"
	"testing"
	"time"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type executionStoreStub struct {
	claim       ExecutionClaim
	snapshot    ExecutionSnapshot
	dispatched  []OccurrenceCommand
	skipped     []OccurrenceCommand
	failures    []string
	dispatchErr error
}

func (store *executionStoreStub) Claim(context.Context, string, time.Time, time.Duration) (ExecutionClaim, bool, error) {
	return store.claim, true, nil
}
func (store *executionStoreStub) Load(context.Context, ExecutionClaim) (ExecutionSnapshot, error) {
	return store.snapshot, nil
}
func (store *executionStoreStub) Heartbeat(context.Context, ExecutionClaim, time.Time, time.Duration) error {
	return nil
}
func (store *executionStoreStub) Dispatch(_ context.Context, command OccurrenceCommand) (bool, error) {
	store.dispatched = append(store.dispatched, command)
	return false, store.dispatchErr
}
func (store *executionStoreStub) Skip(_ context.Context, command OccurrenceCommand) (bool, error) {
	store.skipped = append(store.skipped, command)
	return false, store.dispatchErr
}
func (store *executionStoreStub) Fail(_ context.Context, _ ExecutionClaim, retry bool, _ time.Time, code string, _ time.Time, _ int) (string, error) {
	store.failures = append(store.failures, code)
	if retry {
		return "retry", nil
	}
	return "dead_letter", nil
}

type executionAuthorizerStub struct {
	value ExecutionAuthorization
	err   error
}

func (authorizer executionAuthorizerStub) Authorize(context.Context, ExecutionSnapshot) (ExecutionAuthorization, error) {
	return authorizer.value, authorizer.err
}

type executionClockStub struct{ values []time.Time }

func (clock *executionClockStub) Now() time.Time {
	value := clock.values[0]
	if len(clock.values) > 1 {
		clock.values = clock.values[1:]
	}
	return value
}

type executionIDStub struct{ value string }

func (generator executionIDStub) New() string { return generator.value }

func TestExecutionProcessorDispatchesDeterministicOccurrence(t *testing.T) {
	scheduledFor := time.Date(2026, 8, 24, 13, 0, 0, 0, time.UTC)
	schedule := executionSchedule(t, scheduledFor, domain.MissedCatchUpOne)
	claim := ExecutionClaim{AccountID: schedule.AccountID, ScheduleID: schedule.ID, ScheduleVersion: schedule.Version, ScheduledFor: scheduledFor, LeaseID: "81000000-0000-4000-8000-000000000001", Attempt: 1}
	store := &executionStoreStub{claim: claim, snapshot: ExecutionSnapshot{Schedule: schedule}}
	now := scheduledFor.Add(time.Minute)
	processor, err := NewExecutionProcessor(store, executionAuthorizerStub{value: ExecutionAuthorization{EntitlementVersion: 7, MaximumConcurrentRun: 4, CanReadRestricted: true}},
		&executionClockStub{values: []time.Time{now, now}}, executionIDStub{"91000000-0000-4000-8000-000000000001"}, DefaultExecutionLease, DefaultExecutionMaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessOne(context.Background())
	if err != nil || !result.Worked || !result.Dispatched || result.Skipped || len(store.dispatched) != 1 || len(store.skipped) != 0 {
		t.Fatalf("result=%+v dispatched=%d skipped=%d err=%v", result, len(store.dispatched), len(store.skipped), err)
	}
	command := store.dispatched[0]
	wantOccurrence, _ := ids.Derive(string(schedule.ID), "occurrence/"+scheduledFor.Format(time.RFC3339Nano))
	wantRun, _ := ids.Derive(wantOccurrence, "run")
	if command.OccurrenceID != wantOccurrence || command.RunID != ids.RunID(wantRun) || command.Subject != "Daily review — 2026-08-24" ||
		!command.NextRunAt.Equal(time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)) || !command.Authorization.CanReadRestricted {
		t.Fatalf("command=%+v", command)
	}
}

func TestExecutionProcessorAppliesSkipAndCatchUpOneWithoutBacklogReplay(t *testing.T) {
	scheduledFor := time.Date(2026, 8, 20, 13, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 24, 15, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name       string
		policy     domain.MissedRunPolicy
		dispatched int
		skipped    int
	}{{"skip", domain.MissedSkip, 0, 1}, {"catch up one", domain.MissedCatchUpOne, 1, 0}} {
		t.Run(test.name, func(t *testing.T) {
			schedule := executionSchedule(t, scheduledFor, test.policy)
			claim := ExecutionClaim{AccountID: schedule.AccountID, ScheduleID: schedule.ID, ScheduleVersion: schedule.Version, ScheduledFor: scheduledFor, LeaseID: "81000000-0000-4000-8000-000000000001", Attempt: 1}
			store := &executionStoreStub{claim: claim, snapshot: ExecutionSnapshot{Schedule: schedule}}
			processor, err := NewExecutionProcessor(store, executionAuthorizerStub{value: ExecutionAuthorization{EntitlementVersion: 7, MaximumConcurrentRun: 4}},
				&executionClockStub{values: []time.Time{now, now}}, executionIDStub{"91000000-0000-4000-8000-000000000001"}, DefaultExecutionLease, DefaultExecutionMaxAttempts)
			if err != nil {
				t.Fatal(err)
			}
			result, err := processor.ProcessOne(context.Background())
			if err != nil || len(store.dispatched) != test.dispatched || len(store.skipped) != test.skipped || result.Skipped != (test.skipped == 1) {
				t.Fatalf("result=%+v dispatched=%d skipped=%d err=%v", result, len(store.dispatched), len(store.skipped), err)
			}
			var command OccurrenceCommand
			if test.dispatched == 1 {
				command = store.dispatched[0]
			} else {
				command = store.skipped[0]
			}
			if !command.NextRunAt.Equal(time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)) {
				t.Fatalf("next=%s", command.NextRunAt)
			}
		})
	}
}

func TestExecutionProcessorClassifiesAuthorizationAndCapacity(t *testing.T) {
	scheduledFor := time.Date(2026, 8, 24, 13, 0, 0, 0, time.UTC)
	schedule := executionSchedule(t, scheduledFor, domain.MissedSkip)
	claim := ExecutionClaim{AccountID: schedule.AccountID, ScheduleID: schedule.ID, ScheduleVersion: 1, ScheduledFor: scheduledFor, LeaseID: "81000000-0000-4000-8000-000000000001", Attempt: 3}
	for _, test := range []struct {
		name        string
		authErr     error
		dispatchErr error
		code        string
		dead        bool
	}{{"authorization", ErrExecutionAuthorization, nil, "authorization_denied", true}, {"capacity", nil, ErrExecutionCapacity, "run_capacity", false}} {
		t.Run(test.name, func(t *testing.T) {
			store := &executionStoreStub{claim: claim, snapshot: ExecutionSnapshot{Schedule: schedule}, dispatchErr: test.dispatchErr}
			now := scheduledFor.Add(time.Minute)
			processor, err := NewExecutionProcessor(store, executionAuthorizerStub{value: ExecutionAuthorization{EntitlementVersion: 7, MaximumConcurrentRun: 4}, err: test.authErr},
				&executionClockStub{values: []time.Time{now, now}}, executionIDStub{"91000000-0000-4000-8000-000000000001"}, DefaultExecutionLease, DefaultExecutionMaxAttempts)
			if err != nil {
				t.Fatal(err)
			}
			result, err := processor.ProcessOne(context.Background())
			if err == nil || result.DeadLetter != test.dead || len(store.failures) != 1 || store.failures[0] != test.code {
				t.Fatalf("result=%+v failures=%v err=%v", result, store.failures, err)
			}
		})
	}
}

func executionSchedule(t *testing.T, next time.Time, policy domain.MissedRunPolicy) domain.Schedule {
	t.Helper()
	value, err := domain.Restore(domain.Schedule{
		ID: "11000000-0000-4000-8000-000000000001", AccountID: "21000000-0000-4000-8000-000000000001", Name: "Daily review", Timezone: "America/New_York",
		Recurrence: domain.Recurrence{Frequency: domain.FrequencyDaily, LocalHour: 9, GapPolicy: domain.GapSkip, OverlapPolicy: domain.OverlapFirst}, MissedRunPolicy: policy,
		Template: domain.AgentRunTemplate{BoardroomID: "31000000-0000-4000-8000-000000000001", Mode: "selected", PersonaIDs: []ids.PersonaID{"41000000-0000-4000-8000-000000000001"}, Subject: "Daily review", Prompt: "Review priorities."},
		State:    domain.StateActive, Version: 1, NextRunAt: &next, CreatedBy: "51000000-0000-4000-8000-000000000001", CreatedAt: next.Add(-24 * time.Hour), UpdatedAt: next.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
