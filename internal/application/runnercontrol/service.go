// Package runnercontrol owns fair, durable admission to ephemeral execution.
// Queue records contain routing identifiers only; invocation payloads and
// customer credentials never enter this control plane.
package runnercontrol

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultLease            = 2 * time.Minute
	DefaultMaxAttempts      = 8
	InspectionInterval      = 10 * time.Second
	DefaultPayloadRetention = 24 * time.Hour
	DefaultPruneBatch       = 500
	MaximumPruneBatch       = 1000
)

var (
	ErrInvalidInvocation  = errors.New("runner invocation is invalid")
	ErrInvocationConflict = errors.New("runner invocation identity conflicts with an existing record")
	ErrInvocationNotFound = errors.New("runner invocation was not found")
	ErrLaunchUncertain    = errors.New("runner Job creation outcome is uncertain")
	ErrLeaseLost          = errors.New("runner invocation lease was lost")
	validProfile          = regexp.MustCompile(`^[a-z][a-z0-9-]{0,49}$`)
)

type Invocation struct {
	ID                string
	AccountID         ids.AccountID
	Profile           string
	State             string
	AttemptCount      int
	LeaseID           string
	LeaseExpiresAt    *time.Time
	JobName           string
	QueuedAt          time.Time
	LaunchedAt        *time.Time
	CancelRequestedAt *time.Time
}

type AccountPolicy struct {
	AccountID        ids.AccountID
	Weight           int
	ConcurrencyLimit int
}

type Stats struct {
	Ready, Launching, LaunchUncertain, Launched, Canceling, Failed, DeadLetter uint64
	OldestReadyAge                                                             time.Duration
}

type Queue interface {
	Configure(context.Context, AccountPolicy, time.Time) error
	Enqueue(context.Context, Invocation) (bool, error)
	RequestCancellation(context.Context, ids.AccountID, string, time.Time) (string, error)
	ClaimFair(context.Context, time.Time, time.Duration) (Invocation, bool, error)
	MarkLaunched(context.Context, Invocation, string, time.Time) error
	MarkLaunchUncertain(context.Context, Invocation, string, time.Time, string) error
	FailLaunch(context.Context, Invocation, time.Time, time.Time, string, bool) error
	ClaimReconciliationCandidates(context.Context, time.Time, time.Duration, int) ([]Invocation, error)
	ConfirmLaunch(context.Context, Invocation, time.Time) error
	ResolveLaunchAbsent(context.Context, Invocation, time.Time, time.Time, string, bool) error
	Complete(context.Context, string, string, string, time.Time) error
	PruneTerminalPayloads(context.Context, time.Time, time.Time, int) (int64, error)
	Stats(context.Context, time.Time) (Stats, error)
}

type Launcher interface {
	// Ensure returns ErrLaunchUncertain together with the deterministic Job
	// name whenever the create may have crossed the substrate boundary.
	Ensure(context.Context, Invocation) (string, error)
	Cancel(context.Context, Invocation) error
	Inspect(context.Context, Invocation) (TerminalStatus, error)
}

type TerminalStatus struct {
	Observed bool
	Terminal bool
	Outcome  string
}

type Clock interface{ Now() time.Time }

type Service struct {
	queue       Queue
	launcher    Launcher
	clock       Clock
	lease       time.Duration
	maxAttempts int
}

func NewService(queue Queue, launcher Launcher, clock Clock, lease time.Duration, maxAttempts int) (*Service, error) {
	if queue == nil || launcher == nil || clock == nil || lease <= 0 || lease > 30*time.Minute || maxAttempts < 1 || maxAttempts > 100 {
		return nil, ErrInvalidInvocation
	}
	return &Service{queue: queue, launcher: launcher, clock: clock, lease: lease, maxAttempts: maxAttempts}, nil
}

func (s *Service) Configure(ctx context.Context, policy AccountPolicy) error {
	if ids.Validate(string(policy.AccountID)) != nil || policy.Weight < 1 || policy.Weight > 100 || policy.ConcurrencyLimit < 1 || policy.ConcurrencyLimit > 1000 {
		return ErrInvalidInvocation
	}
	return s.queue.Configure(ctx, policy, s.clock.Now().UTC())
}

func (s *Service) Enqueue(ctx context.Context, invocation Invocation) (bool, error) {
	if ids.Validate(invocation.ID) != nil || ids.Validate(string(invocation.AccountID)) != nil || !validProfile.MatchString(invocation.Profile) || invocation.QueuedAt.IsZero() {
		return false, ErrInvalidInvocation
	}
	invocation.State = "queued"
	return s.queue.Enqueue(ctx, invocation)
}

// RequestCancellation durably binds a cancellation request to both the
// invocation and Account. The returned state tells the caller whether the
// request completed before launch or requires controller reconciliation.
func (s *Service) RequestCancellation(ctx context.Context, accountID ids.AccountID, invocationID string) (string, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(invocationID) != nil {
		return "", ErrInvalidInvocation
	}
	return s.queue.RequestCancellation(ctx, accountID, invocationID, s.clock.Now().UTC())
}

func (s *Service) ProcessOne(ctx context.Context) (bool, error) {
	now := s.clock.Now().UTC()
	invocation, found, err := s.queue.ClaimFair(ctx, now, s.lease)
	if err != nil || !found {
		return found, err
	}
	jobName, launchErr := s.launcher.Ensure(ctx, invocation)
	if launchErr != nil {
		if errors.Is(launchErr, ErrLaunchUncertain) {
			if strings.TrimSpace(jobName) == "" || len(jobName) > 253 {
				// The lease remains in place because an ambiguous create cannot be
				// released without a deterministic identity to reconcile.
				return true, errors.Join(launchErr, ErrInvalidInvocation)
			}
			if markErr := s.queue.MarkLaunchUncertain(ctx, invocation, jobName, now, "launcher_ambiguous"); markErr != nil {
				return true, errors.Join(launchErr, markErr)
			}
			return true, launchErr
		}
		code := boundedCode(launchErr)
		dead := invocation.AttemptCount >= s.maxAttempts
		next := now.Add(retryDelay(invocation.AttemptCount))
		if markErr := s.queue.FailLaunch(ctx, invocation, now, next, code, dead); markErr != nil {
			return true, errors.Join(launchErr, markErr)
		}
		return true, launchErr
	}
	if strings.TrimSpace(jobName) == "" || len(jobName) > 253 {
		_ = s.queue.FailLaunch(ctx, invocation, now, now.Add(retryDelay(invocation.AttemptCount)), "launcher_contract_invalid", true)
		return true, ErrInvalidInvocation
	}
	return true, s.queue.MarkLaunched(ctx, invocation, jobName, now)
}

func (s *Service) Stats(ctx context.Context) (Stats, error) {
	return s.queue.Stats(ctx, s.clock.Now().UTC())
}

// ReconcileJobs observes a bounded set of launched or canceling Jobs and
// durably releases Account capacity only after a terminal outcome. Multiple
// controller replicas may inspect the same Job because deletion and completion
// are both exact and idempotent.
func (s *Service) ReconcileJobs(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		return 0, ErrInvalidInvocation
	}
	invocations, err := s.queue.ClaimReconciliationCandidates(ctx, s.clock.Now().UTC(), InspectionInterval, limit)
	if err != nil {
		return 0, err
	}
	reconciled := 0
	var failures []error
	for _, invocation := range invocations {
		if invocation.State == "canceling" {
			if cancelErr := s.launcher.Cancel(ctx, invocation); cancelErr != nil {
				failures = append(failures, cancelErr)
				continue
			}
		}
		status, inspectErr := s.launcher.Inspect(ctx, invocation)
		if inspectErr != nil {
			failures = append(failures, inspectErr)
			continue
		}
		if invocation.State == "launch_uncertain" {
			if status.Terminal && !status.Observed {
				failures = append(failures, ErrInvalidInvocation)
				continue
			}
			if !status.Observed {
				now := s.clock.Now().UTC()
				dead := invocation.AttemptCount >= s.maxAttempts
				if resolveErr := s.queue.ResolveLaunchAbsent(ctx, invocation, now, now.Add(retryDelay(invocation.AttemptCount)), "launcher_not_observed", dead); resolveErr != nil {
					failures = append(failures, resolveErr)
					continue
				}
				reconciled++
				continue
			}
			if !status.Terminal {
				if confirmErr := s.queue.ConfirmLaunch(ctx, invocation, s.clock.Now().UTC()); confirmErr != nil {
					failures = append(failures, confirmErr)
					continue
				}
				reconciled++
				continue
			}
		}
		if !status.Terminal {
			continue
		}
		if status.Outcome != "completed" && status.Outcome != "execution_failed" && status.Outcome != "canceled" {
			failures = append(failures, ErrInvalidInvocation)
			continue
		}
		if completeErr := s.queue.Complete(ctx, invocation.ID, invocation.JobName, status.Outcome, s.clock.Now().UTC()); completeErr != nil {
			failures = append(failures, completeErr)
			continue
		}
		reconciled++
	}
	return reconciled, errors.Join(failures...)
}

// Complete records a terminal runner outcome and releases the Account's
// concurrency slot. jobName binds the callback to the launch that owns it.
func (s *Service) Complete(ctx context.Context, invocationID, jobName, outcome string) error {
	if ids.Validate(invocationID) != nil || strings.TrimSpace(jobName) == "" || len(jobName) > 253 {
		return ErrInvalidInvocation
	}
	switch outcome {
	case "completed", "execution_failed", "canceled":
	default:
		return ErrInvalidInvocation
	}
	return s.queue.Complete(ctx, invocationID, jobName, outcome, s.clock.Now().UTC())
}

// PruneTerminalPayloads destroys encrypted request/result envelopes after a
// bounded recovery window. Identifier-only lifecycle, hashes, action state,
// and capability audit remain available under their separate retention rules.
func (s *Service) PruneTerminalPayloads(ctx context.Context, retention time.Duration, limit int) (int64, error) {
	if retention < time.Hour || retention > 30*24*time.Hour || limit < 1 || limit > MaximumPruneBatch {
		return 0, ErrInvalidInvocation
	}
	now := s.clock.Now().UTC()
	return s.queue.PruneTerminalPayloads(ctx, now.Add(-retention), now, limit)
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second << min(attempt-1, 8)
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

func boundedCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "launcher_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "launcher_canceled"
	}
	return "launcher_failed"
}
