// Package approvedaction executes exact, human-approved Agent proposals after
// the ephemeral runner that proposed them has terminated.
package approvedaction

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultLease = 2 * time.Minute
	MaximumLease = 5 * time.Minute
)

var (
	ErrInvalidClaim = errors.New("approved action claim is invalid")
	ErrUnavailable  = errors.New("approved action execution is unavailable")
	validCode       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,99}$`)
)

type Claim struct {
	Lease            runnercapability.ActionLease
	CanonicalPayload json.RawMessage
	ApprovedByUserID ids.UserID
}

func (claim Claim) Valid(now time.Time) bool {
	lease := claim.Lease
	return ids.Validate(string(lease.AccountID)) == nil && ids.Validate(lease.InvocationID) == nil &&
		ids.Validate(lease.OperationID) == nil && ids.Validate(lease.AttemptID) == nil &&
		runnerbroker.ValidCapability(lease.Capability) && ids.Validate(string(claim.ApprovedByUserID)) == nil &&
		lease.InputDigest != [sha256.Size]byte{} && sha256.Sum256(claim.CanonicalPayload) == lease.InputDigest &&
		len(claim.CanonicalPayload) > 0 && len(claim.CanonicalPayload) <= runnercapability.MaximumInputBytes &&
		lease.IdempotencyKey == lease.OperationID &&
		(lease.Mode == runnercapability.ActionExecute || lease.Mode == runnercapability.ActionReconcile) &&
		lease.LeaseExpiresAt.After(now)
}

type Repository interface {
	Claim(context.Context, string, time.Time, time.Time) (Claim, bool, error)
	Complete(context.Context, runnercapability.ActionCompletion) error
}

type Clock interface{ Now() time.Time }

type Definition struct {
	Capability string
	Timeout    time.Duration
	Handler    runnercapability.ConsequentialHandler
}

type Service struct {
	repository  Repository
	ids         ids.Generator
	clock       Clock
	lease       time.Duration
	definitions map[string]Definition
}

func New(repository Repository, generator ids.Generator, clock Clock, lease time.Duration, definitions []Definition) (*Service, error) {
	if repository == nil || generator == nil || clock == nil || lease < time.Second || lease > MaximumLease || len(definitions) == 0 || len(definitions) > runnercapability.MaximumDefinitions {
		return nil, ErrInvalidClaim
	}
	registered := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		if !runnerbroker.ValidCapability(definition.Capability) || definition.Handler == nil || definition.Timeout < 100*time.Millisecond || definition.Timeout > MaximumLease {
			return nil, ErrInvalidClaim
		}
		if _, exists := registered[definition.Capability]; exists {
			return nil, ErrInvalidClaim
		}
		registered[definition.Capability] = definition
	}
	return &Service{repository: repository, ids: generator, clock: clock, lease: lease, definitions: registered}, nil
}

func (service *Service) ProcessOne(ctx context.Context) (bool, error) {
	now := service.clock.Now().UTC()
	attemptID := service.ids.New()
	if ids.Validate(attemptID) != nil {
		return false, ErrInvalidClaim
	}
	claim, found, err := service.repository.Claim(ctx, attemptID, now, now.Add(service.lease))
	if err != nil || !found {
		return false, err
	}
	if !claim.Valid(now) || claim.Lease.AttemptID != attemptID {
		return true, ErrInvalidClaim
	}
	definition, exists := service.definitions[claim.Lease.Capability]
	if !exists {
		return true, fmt.Errorf("%w: executor_unavailable", ErrUnavailable)
	}
	grant := runnerbroker.CapabilityGrant{
		Identity:  runnerbroker.Identity{InvocationID: claim.Lease.InvocationID},
		AccountID: claim.Lease.AccountID, Capability: claim.Lease.Capability, ExpiresAt: claim.Lease.LeaseExpiresAt,
	}
	call := runnercapability.AuthorizedCall{Grant: grant, OperationID: claim.Lease.OperationID, Input: claim.CanonicalPayload,
		InputDigest: claim.Lease.InputDigest, ApprovedByUserID: claim.ApprovedByUserID, Action: &claim.Lease}
	executionContext, cancel := context.WithDeadline(ctx, minimum(now.Add(definition.Timeout), claim.Lease.LeaseExpiresAt))
	defer cancel()
	outcome, code := runnercapability.ActionSucceeded, ""
	if claim.Lease.Mode == runnercapability.ActionReconcile {
		_, outcome, err = definition.Handler.Reconcile(executionContext, call)
		if !validOutcome(outcome) {
			outcome, err = runnercapability.ActionUnknown, ErrUnavailable
		} else if err != nil && outcome == runnercapability.ActionSucceeded {
			outcome = failureOutcome(err)
		}
	} else {
		_, err = definition.Handler.Execute(executionContext, call)
		if err != nil {
			outcome = failureOutcome(err)
		}
	}
	if err != nil {
		code = machineCode(err, "execution_failed")
	} else if outcome != runnercapability.ActionSucceeded {
		code = "action_" + string(outcome)
	}
	completionContext, completionCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer completionCancel()
	if completeErr := service.repository.Complete(completionContext, runnercapability.ActionCompletion{
		Lease: claim.Lease, Outcome: outcome, ErrorCode: code, At: service.clock.Now().UTC(),
	}); completeErr != nil {
		return true, fmt.Errorf("%w: settle: %v", ErrUnavailable, completeErr)
	}
	if err != nil {
		return true, fmt.Errorf("approved action %s: %w", code, err)
	}
	if outcome != runnercapability.ActionSucceeded {
		return true, fmt.Errorf("approved action %s: %w", code, ErrUnavailable)
	}
	return true, nil
}

type codedError interface{ Code() string }
type definitiveError interface{ Definitive() bool }

func machineCode(err error, fallback string) string {
	var coded codedError
	if errors.As(err, &coded) && validCode.MatchString(coded.Code()) {
		return coded.Code()
	}
	return fallback
}

func failureOutcome(err error) runnercapability.ActionOutcome {
	var definitive definitiveError
	if errors.As(err, &definitive) && definitive.Definitive() {
		return runnercapability.ActionFailed
	}
	return runnercapability.ActionUnknown
}

func validOutcome(outcome runnercapability.ActionOutcome) bool {
	return outcome == runnercapability.ActionSucceeded || outcome == runnercapability.ActionFailed || outcome == runnercapability.ActionUnknown
}

func minimum(left, right time.Time) time.Time {
	if right.Before(left) {
		return right
	}
	return left
}
