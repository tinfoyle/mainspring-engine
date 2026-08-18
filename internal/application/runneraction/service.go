// Package runneraction owns the durable execution lease around consequential
// runner capabilities. Approval evidence is written by a separate Attention
// owner; this service can only consume an exact approved projection.
package runneraction

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
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
	ErrInvalidAction    = errors.New("runner action is invalid")
	ErrApprovalRequired = errors.New("runner action approval is required")
	ErrApprovalExpired  = errors.New("runner action approval expired")
	ErrActionBusy       = errors.New("runner action is already leased")
	ErrActionDenied     = errors.New("runner action is denied")
	ErrStateConflict    = errors.New("runner action state conflicts with the request")
	ErrRepository       = errors.New("runner action repository is unavailable")
)

type BeginCommand struct {
	Request        runnercapability.ActionRequest
	AttemptID      string
	Now            time.Time
	LeaseExpiresAt time.Time
}

type Repository interface {
	Begin(context.Context, BeginCommand) (runnercapability.ActionLease, error)
	Complete(context.Context, runnercapability.ActionCompletion) error
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository Repository
	ids        ids.Generator
	clock      Clock
	lease      time.Duration
}

func New(repository Repository, generator ids.Generator, clock Clock, lease time.Duration) (*Service, error) {
	if repository == nil || generator == nil || clock == nil || lease <= 0 || lease > MaximumLease {
		return nil, ErrInvalidAction
	}
	return &Service{repository: repository, ids: generator, clock: clock, lease: lease}, nil
}

func (s *Service) BeginAction(ctx context.Context, request runnercapability.ActionRequest) (runnercapability.ActionLease, error) {
	now := s.clock.Now().UTC()
	if !validRequest(request, now) {
		return runnercapability.ActionLease{}, ErrInvalidAction
	}
	attemptID := s.ids.New()
	if ids.Validate(attemptID) != nil {
		return runnercapability.ActionLease{}, ErrInvalidAction
	}
	leaseExpiresAt := now.Add(s.lease)
	if request.ExpiresAt.Before(leaseExpiresAt) {
		leaseExpiresAt = request.ExpiresAt.UTC()
	}
	lease, err := s.repository.Begin(ctx, BeginCommand{Request: request, AttemptID: attemptID, Now: now, LeaseExpiresAt: leaseExpiresAt})
	if err != nil {
		return runnercapability.ActionLease{}, classify(err)
	}
	if !validLease(lease, request, attemptID, now, leaseExpiresAt) {
		return runnercapability.ActionLease{}, &DecisionError{code: "action_state_conflict", cause: ErrStateConflict}
	}
	return lease, nil
}

func (s *Service) CompleteAction(ctx context.Context, completion runnercapability.ActionCompletion) error {
	if !validCompletion(completion) {
		return ErrInvalidAction
	}
	if err := s.repository.Complete(ctx, completion); err != nil {
		return classify(err)
	}
	return nil
}

type DecisionError struct {
	code  string
	cause error
}

func (e *DecisionError) Error() string { return e.code }
func (e *DecisionError) Code() string  { return e.code }
func (e *DecisionError) Unwrap() error { return e.cause }

func classify(err error) error {
	switch {
	case errors.Is(err, ErrApprovalRequired):
		return &DecisionError{code: "approval_required", cause: ErrApprovalRequired}
	case errors.Is(err, ErrApprovalExpired):
		return &DecisionError{code: "approval_expired", cause: ErrApprovalExpired}
	case errors.Is(err, ErrActionBusy):
		return &DecisionError{code: "action_in_progress", cause: ErrActionBusy}
	case errors.Is(err, ErrActionDenied):
		return &DecisionError{code: "action_denied", cause: ErrActionDenied}
	case errors.Is(err, ErrStateConflict):
		return &DecisionError{code: "action_state_conflict", cause: ErrStateConflict}
	default:
		return &DecisionError{code: "action_repository_unavailable", cause: fmt.Errorf("%w: %v", ErrRepository, err)}
	}
}

func validRequest(request runnercapability.ActionRequest, now time.Time) bool {
	return ids.Validate(string(request.AccountID)) == nil && ids.Validate(request.InvocationID) == nil && ids.Validate(request.PodUID) == nil &&
		ids.Validate(request.OperationID) == nil && runnerbroker.ValidCapability(request.Capability) && request.InputDigest != [sha256.Size]byte{} &&
		!request.ExpiresAt.IsZero() && request.ExpiresAt.After(now) && request.ExpiresAt.Sub(now) <= runnerbroker.MaximumInvocationLife
}

func validLease(lease runnercapability.ActionLease, request runnercapability.ActionRequest, attemptID string, now, maximumExpiry time.Time) bool {
	return lease.AccountID == request.AccountID && lease.InvocationID == request.InvocationID && lease.OperationID == request.OperationID && lease.AttemptID == attemptID &&
		lease.Capability == request.Capability && lease.InputDigest == request.InputDigest && lease.IdempotencyKey == request.OperationID &&
		(lease.Mode == runnercapability.ActionExecute || lease.Mode == runnercapability.ActionReconcile) && lease.LeaseExpiresAt.After(now) && !lease.LeaseExpiresAt.After(maximumExpiry)
}

func validCompletion(completion runnercapability.ActionCompletion) bool {
	lease := completion.Lease
	if ids.Validate(string(lease.AccountID)) != nil || ids.Validate(lease.InvocationID) != nil || ids.Validate(lease.OperationID) != nil || ids.Validate(lease.AttemptID) != nil ||
		!runnerbroker.ValidCapability(lease.Capability) || lease.InputDigest == [sha256.Size]byte{} || lease.IdempotencyKey != lease.OperationID ||
		(lease.Mode != runnercapability.ActionExecute && lease.Mode != runnercapability.ActionReconcile) || lease.LeaseExpiresAt.IsZero() || completion.At.IsZero() {
		return false
	}
	if completion.Outcome != runnercapability.ActionSucceeded && completion.Outcome != runnercapability.ActionFailed && completion.Outcome != runnercapability.ActionUnknown {
		return false
	}
	if completion.Outcome == runnercapability.ActionSucceeded {
		return completion.ErrorCode == ""
	}
	return validErrorCode(completion.ErrorCode)
}

func validErrorCode(value string) bool {
	if value == "" || len(value) > 100 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

var _ runnercapability.ActionAuthorizer = (*Service)(nil)
