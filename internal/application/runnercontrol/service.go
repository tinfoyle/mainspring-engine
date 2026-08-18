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
	DefaultLease       = 2 * time.Minute
	DefaultMaxAttempts = 8
)

var (
	ErrInvalidInvocation  = errors.New("runner invocation is invalid")
	ErrInvocationConflict = errors.New("runner invocation identity conflicts with an existing record")
	ErrLeaseLost          = errors.New("runner invocation lease was lost")
	validProfile          = regexp.MustCompile(`^[a-z][a-z0-9-]{0,49}$`)
)

type Invocation struct {
	ID             string
	AccountID      ids.AccountID
	Profile        string
	State          string
	AttemptCount   int
	LeaseID        string
	LeaseExpiresAt *time.Time
	JobName        string
	QueuedAt       time.Time
}

type AccountPolicy struct {
	AccountID        ids.AccountID
	Weight           int
	ConcurrencyLimit int
}

type Stats struct {
	Ready, Launching, Launched, Failed, DeadLetter uint64
	OldestReadyAge                                 time.Duration
}

type Queue interface {
	Configure(context.Context, AccountPolicy, time.Time) error
	Enqueue(context.Context, Invocation) (bool, error)
	ClaimFair(context.Context, time.Time, time.Duration) (Invocation, bool, error)
	MarkLaunched(context.Context, Invocation, string, time.Time) error
	FailLaunch(context.Context, Invocation, time.Time, string, bool) error
	Complete(context.Context, string, string, string, time.Time) error
	Stats(context.Context, time.Time) (Stats, error)
}

type Launcher interface {
	Ensure(context.Context, Invocation) (string, error)
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

func (s *Service) ProcessOne(ctx context.Context) (bool, error) {
	now := s.clock.Now().UTC()
	invocation, found, err := s.queue.ClaimFair(ctx, now, s.lease)
	if err != nil || !found {
		return found, err
	}
	jobName, launchErr := s.launcher.Ensure(ctx, invocation)
	if launchErr != nil {
		code := boundedCode(launchErr)
		dead := invocation.AttemptCount >= s.maxAttempts
		next := now.Add(retryDelay(invocation.AttemptCount))
		if markErr := s.queue.FailLaunch(ctx, invocation, next, code, dead); markErr != nil {
			return true, errors.Join(launchErr, markErr)
		}
		return true, launchErr
	}
	if strings.TrimSpace(jobName) == "" || len(jobName) > 253 {
		_ = s.queue.FailLaunch(ctx, invocation, now.Add(retryDelay(invocation.AttemptCount)), "launcher_contract_invalid", true)
		return true, ErrInvalidInvocation
	}
	return true, s.queue.MarkLaunched(ctx, invocation, jobName, now)
}

func (s *Service) Stats(ctx context.Context) (Stats, error) {
	return s.queue.Stats(ctx, s.clock.Now().UTC())
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
