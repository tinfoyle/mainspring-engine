package scheduling

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultExecutionLease       = 30 * time.Second
	DefaultExecutionMaxAttempts = 12
	MaximumExecutionMaxAttempts = 100
	maximumExecutionRetryDelay  = 15 * time.Minute
	executionRunLifetime        = 24 * time.Hour
)

var (
	ErrExecutionClaimInvalid       = errors.New("schedule execution claim is invalid")
	ErrExecutionSnapshotInvalid    = errors.New("schedule execution snapshot is invalid")
	ErrExecutionAuthorization      = errors.New("schedule execution authorization is denied")
	ErrExecutionAuthorizationStale = errors.New("schedule execution authorization is stale")
	ErrExecutionServiceUnavailable = errors.New("schedule execution authorization is unavailable")
	ErrExecutionLeaseLost          = errors.New("schedule execution lease was lost")
	ErrExecutionConflict           = errors.New("schedule execution conflicts with durable state")
	ErrExecutionCapacity           = errors.New("schedule execution Agent capacity is unavailable")
)

type ExecutionClaim struct {
	AccountID       ids.AccountID
	ScheduleID      ids.ScheduleID
	ScheduleVersion uint64
	ScheduledFor    time.Time
	LeaseID         string
	Attempt         int
}

func (claim ExecutionClaim) Valid() bool {
	return ids.Validate(string(claim.AccountID)) == nil && ids.Validate(string(claim.ScheduleID)) == nil &&
		claim.ScheduleVersion > 0 && !claim.ScheduledFor.IsZero() && ids.Validate(claim.LeaseID) == nil && claim.Attempt > 0
}

type ExecutionSnapshot struct {
	Schedule domain.Schedule
}

func (snapshot ExecutionSnapshot) ValidFor(claim ExecutionClaim) bool {
	value, err := domain.Restore(snapshot.Schedule)
	return err == nil && value.AccountID == claim.AccountID && value.ID == claim.ScheduleID && value.Version == claim.ScheduleVersion &&
		value.State == domain.StateActive && value.NextRunAt != nil && value.NextRunAt.Equal(claim.ScheduledFor.UTC())
}

type ExecutionAuthorization struct {
	EntitlementVersion   uint64
	MaximumConcurrentRun int64
	CanReadRestricted    bool
}

func (authorization ExecutionAuthorization) Valid() bool {
	return authorization.EntitlementVersion > 0 && authorization.MaximumConcurrentRun > 0
}

type OccurrenceCommand struct {
	Claim            ExecutionClaim
	Schedule         domain.Schedule
	OccurrenceID     string
	RunID            ids.RunID
	ConversationID   ids.ConversationID
	UserMessageID    ids.MessageID
	Subject          string
	Authorization    ExecutionAuthorization
	NextRunAt        time.Time
	At               time.Time
	RequestExpiresAt time.Time
}

func (command OccurrenceCommand) Valid() bool {
	return command.Claim.Valid() && ExecutionSnapshot{Schedule: command.Schedule}.ValidFor(command.Claim) &&
		ids.Validate(command.OccurrenceID) == nil && ids.Validate(string(command.RunID)) == nil &&
		ids.Validate(string(command.ConversationID)) == nil && ids.Validate(string(command.UserMessageID)) == nil &&
		len(strings.TrimSpace(command.Subject)) >= 2 && len([]rune(command.Subject)) <= 240 && command.Authorization.Valid() &&
		command.NextRunAt.After(command.Claim.ScheduledFor) && !command.At.IsZero() && command.RequestExpiresAt.After(command.At)
}

type ExecutionStore interface {
	Claim(context.Context, string, time.Time, time.Duration) (ExecutionClaim, bool, error)
	Load(context.Context, ExecutionClaim) (ExecutionSnapshot, error)
	Heartbeat(context.Context, ExecutionClaim, time.Time, time.Duration) error
	Dispatch(context.Context, OccurrenceCommand) (bool, error)
	Skip(context.Context, OccurrenceCommand) (bool, error)
	Fail(context.Context, ExecutionClaim, bool, time.Time, string, time.Time, int) (string, error)
}

// ExecutionAuthorizer resolves the creator's current Membership, Account
// placement, target-package modes and Agent Run limit over an authenticated
// workload boundary. It does not accept a browser credential.
type ExecutionAuthorizer interface {
	Authorize(context.Context, ExecutionSnapshot) (ExecutionAuthorization, error)
}

type ExecutionClock interface{ Now() time.Time }

type ExecutionResult struct {
	Worked     bool
	Dispatched bool
	Skipped    bool
	Reconciled bool
	DeadLetter bool
}

type ExecutionProcessor struct {
	store       ExecutionStore
	authorizer  ExecutionAuthorizer
	clock       ExecutionClock
	ids         ids.Generator
	lease       time.Duration
	maxAttempts int
}

func NewExecutionProcessor(store ExecutionStore, authorizer ExecutionAuthorizer, clock ExecutionClock, generator ids.Generator, lease time.Duration, maxAttempts int) (*ExecutionProcessor, error) {
	if store == nil || authorizer == nil || clock == nil || generator == nil || lease < time.Second || lease > 30*time.Minute ||
		maxAttempts < 1 || maxAttempts > MaximumExecutionMaxAttempts {
		return nil, errors.New("schedule execution dependencies or bounds are invalid")
	}
	return &ExecutionProcessor{store: store, authorizer: authorizer, clock: clock, ids: generator, lease: lease, maxAttempts: maxAttempts}, nil
}

func (processor *ExecutionProcessor) ProcessOne(ctx context.Context) (ExecutionResult, error) {
	now := processor.clock.Now().UTC()
	claim, found, err := processor.store.Claim(ctx, processor.ids.New(), now, processor.lease)
	if err != nil || !found {
		return ExecutionResult{}, err
	}
	if !claim.Valid() {
		return processor.reject(ctx, claim, now, "claim_invalid", ErrExecutionClaimInvalid)
	}
	snapshot, err := processor.store.Load(ctx, claim)
	if err != nil {
		if errors.Is(err, ErrExecutionLeaseLost) {
			return ExecutionResult{Worked: true}, err
		}
		if errors.Is(err, ErrExecutionSnapshotInvalid) || errors.Is(err, ErrExecutionConflict) {
			return processor.reject(ctx, claim, now, "snapshot_invalid", err)
		}
		return processor.retry(ctx, claim, now, "snapshot_unavailable", err)
	}
	if !snapshot.ValidFor(claim) {
		return processor.reject(ctx, claim, now, "snapshot_invalid", ErrExecutionSnapshotInvalid)
	}
	authorization, err := processor.authorizer.Authorize(ctx, snapshot)
	if err != nil {
		switch {
		case errors.Is(err, ErrExecutionAuthorization), errors.Is(err, ErrExecutionAuthorizationStale):
			return processor.reject(ctx, claim, now, "authorization_denied", err)
		default:
			return processor.retry(ctx, claim, now, "authorization_unavailable", errors.Join(ErrExecutionServiceUnavailable, err))
		}
	}
	if !authorization.Valid() {
		return processor.reject(ctx, claim, now, "authorization_invalid", ErrExecutionAuthorization)
	}
	now = processor.clock.Now().UTC()
	if err := processor.store.Heartbeat(ctx, claim, now, processor.lease); err != nil {
		if errors.Is(err, ErrExecutionLeaseLost) {
			return ExecutionResult{Worked: true}, err
		}
		return processor.retry(ctx, claim, now, "heartbeat_failed", err)
	}
	command, missed, err := buildOccurrenceCommand(claim, snapshot.Schedule, authorization, now)
	if err != nil || !command.Valid() {
		return processor.reject(ctx, claim, now, "occurrence_invalid", errors.Join(ErrExecutionSnapshotInvalid, err))
	}
	if missed && snapshot.Schedule.MissedRunPolicy == domain.MissedSkip {
		reconciled, err := processor.store.Skip(ctx, command)
		return processor.finish(ctx, claim, now, reconciled, true, err)
	}
	reconciled, err := processor.store.Dispatch(ctx, command)
	return processor.finish(ctx, claim, now, reconciled, false, err)
}

func buildOccurrenceCommand(claim ExecutionClaim, schedule domain.Schedule, authorization ExecutionAuthorization, now time.Time) (OccurrenceCommand, bool, error) {
	nextSelected, err := schedule.Recurrence.Next(claim.ScheduledFor, schedule.Timezone)
	if err != nil {
		return OccurrenceCommand{}, false, err
	}
	missed := !nextSelected.After(now)
	nextAfter := claim.ScheduledFor
	if missed {
		nextAfter = now
	}
	nextRunAt, err := schedule.Recurrence.Next(nextAfter, schedule.Timezone)
	if err != nil {
		return OccurrenceCommand{}, false, err
	}
	occurrenceID, err := ids.Derive(string(schedule.ID), "occurrence/"+claim.ScheduledFor.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return OccurrenceCommand{}, false, err
	}
	runID, err := ids.Derive(occurrenceID, "run")
	if err != nil {
		return OccurrenceCommand{}, false, err
	}
	conversationID, err := ids.Derive(occurrenceID, "conversation")
	if err != nil {
		return OccurrenceCommand{}, false, err
	}
	messageID, err := ids.Derive(occurrenceID, "user-message")
	if err != nil {
		return OccurrenceCommand{}, false, err
	}
	return OccurrenceCommand{Claim: claim, Schedule: schedule, OccurrenceID: occurrenceID, RunID: ids.RunID(runID),
		ConversationID: ids.ConversationID(conversationID), UserMessageID: ids.MessageID(messageID),
		Subject: datedSubject(schedule.Template.Subject, claim.ScheduledFor, schedule.Timezone), Authorization: authorization,
		NextRunAt: nextRunAt, At: now, RequestExpiresAt: now.Add(executionRunLifetime)}, missed, nil
}

func datedSubject(subject string, scheduledFor time.Time, timezone string) string {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return ""
	}
	suffix := fmt.Sprintf(" — %s", scheduledFor.In(location).Format(time.DateOnly))
	maximumBase := 240 - len([]rune(suffix))
	base := []rune(strings.TrimSpace(subject))
	if len(base) > maximumBase {
		base = base[:maximumBase]
	}
	return strings.TrimSpace(string(base)) + suffix
}

func (processor *ExecutionProcessor) finish(ctx context.Context, claim ExecutionClaim, now time.Time, reconciled, skipped bool, err error) (ExecutionResult, error) {
	if err == nil {
		return ExecutionResult{Worked: true, Dispatched: !skipped, Skipped: skipped, Reconciled: reconciled}, nil
	}
	switch {
	case errors.Is(err, ErrExecutionLeaseLost):
		return ExecutionResult{Worked: true}, err
	case errors.Is(err, ErrExecutionAuthorizationStale), errors.Is(err, ErrExecutionConflict), errors.Is(err, ErrExecutionSnapshotInvalid):
		return processor.reject(ctx, claim, now, "dispatch_invalid", err)
	case errors.Is(err, ErrExecutionCapacity):
		return processor.retry(ctx, claim, now, "run_capacity", err)
	default:
		return processor.retry(ctx, claim, now, "dispatch_failed", err)
	}
}

func (processor *ExecutionProcessor) reject(ctx context.Context, claim ExecutionClaim, now time.Time, code string, cause error) (ExecutionResult, error) {
	state, failErr := processor.store.Fail(ctx, claim, false, now, code, now, processor.maxAttempts)
	return ExecutionResult{Worked: true, DeadLetter: state == "dead_letter"}, errors.Join(cause, failErr)
}

func (processor *ExecutionProcessor) retry(ctx context.Context, claim ExecutionClaim, now time.Time, code string, cause error) (ExecutionResult, error) {
	next := now.Add(executionRetryDelay(claim.Attempt))
	state, failErr := processor.store.Fail(ctx, claim, true, next, code, now, processor.maxAttempts)
	return ExecutionResult{Worked: true, DeadLetter: state == "dead_letter"}, errors.Join(cause, failErr)
}

func executionRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second
	for index := 1; index < attempt && delay < maximumExecutionRetryDelay; index++ {
		delay *= 2
	}
	if delay > maximumExecutionRetryDelay {
		return maximumExecutionRetryDelay
	}
	return delay
}
