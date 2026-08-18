// Package accountlifecycle owns recoverable Account closure and retention
// transitions. Logical closure is deliberately separate from destructive data
// erasure, which requires a later retention/operator workflow.
package accountlifecycle

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrNotFound          = errors.New("Account closure request not found")
	ErrVersionConflict   = errors.New("Account version conflict")
	ErrStateConflict     = errors.New("Account state does not allow this transition")
	ErrOwnershipRequired = errors.New("active owner Membership is required")
	ErrBillingActive     = errors.New("active billing must be resolved before Account closure")
	ErrReasonRequired    = errors.New("a reason between 3 and 300 characters is required")
)

type State string

const (
	StateCoolingOff State = "cooling_off"
	StateProcessing State = "processing"
	StateBlocked    State = "blocked"
	StateCanceled   State = "canceled"
	StateClosed     State = "closed"
)

type Status struct {
	RequestID      string                `json:"request_id"`
	AccountID      ids.AccountID         `json:"account_id"`
	AccountName    string                `json:"account_name"`
	AccountState   accounts.AccountState `json:"account_state"`
	AccountVersion uint64                `json:"account_version"`
	State          State                 `json:"state"`
	Reason         string                `json:"reason"`
	BlockerCode    string                `json:"blocker_code,omitempty"`
	RequestedAt    time.Time             `json:"requested_at"`
	ExecuteAfter   time.Time             `json:"execute_after"`
	CanceledAt     *time.Time            `json:"canceled_at,omitempty"`
	ClosedAt       *time.Time            `json:"closed_at,omitempty"`
	DeleteAfter    *time.Time            `json:"delete_after,omitempty"`
}

type RequestMutation struct {
	RequestID, EventID, Reason string
	ActorUserID                ids.UserID
	AccountID                  ids.AccountID
	ExpectedAccountVersion     uint64
	At, ExecuteAfter           time.Time
}

type CancelMutation struct {
	EventID, Reason        string
	ActorUserID            ids.UserID
	AccountID              ids.AccountID
	ExpectedAccountVersion uint64
	At                     time.Time
}

type Work struct {
	RequestID   string
	AccountID   ids.AccountID
	Attempt     int
	RequestedBy ids.UserID
}

type Repository interface {
	Request(context.Context, RequestMutation) (Status, error)
	Cancel(context.Context, CancelMutation) (Status, error)
	ListOwned(context.Context, ids.UserID) ([]Status, error)
	Claim(context.Context, time.Time, time.Duration) (Work, bool, error)
	Evaluate(context.Context, Work, string, time.Time, time.Duration, time.Duration) (Status, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository Repository
	authorizer *access.Authorizer
	ids        ids.Generator
	clock      Clock
	coolingOff time.Duration
}

func NewService(repository Repository, authorizer *access.Authorizer, generator ids.Generator, clock Clock, coolingOff time.Duration) (*Service, error) {
	if repository == nil || authorizer == nil || generator == nil || clock == nil || coolingOff < 24*time.Hour || coolingOff > 30*24*time.Hour {
		return nil, errors.New("Account lifecycle dependencies and 1-30 day cooling-off period are required")
	}
	return &Service{repository: repository, authorizer: authorizer, ids: generator, clock: clock, coolingOff: coolingOff}, nil
}

type RequestCommand struct {
	ActorUserID            ids.UserID
	Session                sessions.Session
	AccountID              ids.AccountID
	ExpectedAccountVersion uint64
	Reason                 string
}

func (s *Service) Request(ctx context.Context, command RequestCommand) (Status, error) {
	if _, err := s.authorizer.Authorize(ctx, access.Actor{UserID: command.ActorUserID}, command.AccountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner}}); err != nil {
		return Status{}, err
	}
	if err := strongauth.Require(command.Session, command.ActorUserID, s.clock.Now()); err != nil {
		return Status{}, err
	}
	if command.ExpectedAccountVersion == 0 {
		return Status{}, ErrVersionConflict
	}
	reason, err := normalizeReason(command.Reason)
	if err != nil {
		return Status{}, err
	}
	now := s.clock.Now().UTC()
	return s.repository.Request(ctx, RequestMutation{RequestID: s.ids.New(), EventID: s.ids.New(), Reason: reason, ActorUserID: command.ActorUserID, AccountID: command.AccountID, ExpectedAccountVersion: command.ExpectedAccountVersion, At: now, ExecuteAfter: now.Add(s.coolingOff)})
}

type CancelCommand struct {
	ActorUserID            ids.UserID
	Session                sessions.Session
	AccountID              ids.AccountID
	ExpectedAccountVersion uint64
	Reason                 string
}

func (s *Service) Cancel(ctx context.Context, command CancelCommand) (Status, error) {
	// A closing Account intentionally fails the ordinary Authorizer. Strong
	// identity proof happens here and the repository transaction rechecks the
	// still-active owner Membership before restoring Account access.
	if err := strongauth.Require(command.Session, command.ActorUserID, s.clock.Now()); err != nil {
		return Status{}, err
	}
	if command.ExpectedAccountVersion == 0 {
		return Status{}, ErrVersionConflict
	}
	reason, err := normalizeReason(command.Reason)
	if err != nil {
		return Status{}, err
	}
	return s.repository.Cancel(ctx, CancelMutation{EventID: s.ids.New(), Reason: reason, ActorUserID: command.ActorUserID, AccountID: command.AccountID, ExpectedAccountVersion: command.ExpectedAccountVersion, At: s.clock.Now().UTC()})
}

func (s *Service) ListOwned(ctx context.Context, actorUserID ids.UserID) ([]Status, error) {
	if actorUserID == "" {
		return nil, &access.DeniedError{Code: access.DenialUnauthenticated}
	}
	return s.repository.ListOwned(ctx, actorUserID)
}

type Processor struct {
	repository   Repository
	ids          ids.Generator
	clock        Clock
	lease        time.Duration
	retention    time.Duration
	blockedRetry time.Duration
}

func NewProcessor(repository Repository, generator ids.Generator, clock Clock, lease, retention, blockedRetry time.Duration) (*Processor, error) {
	if repository == nil || generator == nil || clock == nil || lease <= 0 || lease > 30*time.Minute || retention < 7*24*time.Hour || retention > 365*24*time.Hour || blockedRetry < time.Hour || blockedRetry > 7*24*time.Hour {
		return nil, errors.New("Account closure processor dependencies and safe timing are required")
	}
	return &Processor{repository: repository, ids: generator, clock: clock, lease: lease, retention: retention, blockedRetry: blockedRetry}, nil
}

func (p *Processor) ProcessOne(ctx context.Context) (bool, error) {
	now := p.clock.Now().UTC()
	work, ok, err := p.repository.Claim(ctx, now, p.lease)
	if err != nil || !ok {
		return ok, err
	}
	_, err = p.repository.Evaluate(ctx, work, p.ids.New(), now, p.retention, p.blockedRetry)
	return true, err
}

func normalizeReason(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 3 || len(value) > 300 {
		return "", ErrReasonRequired
	}
	return value, nil
}
