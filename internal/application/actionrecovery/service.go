// Package actionrecovery owns Account-authorized, content-redacted views and
// dual-controlled commands for consequential action recovery.
package actionrecovery

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultLimit = 50
	MaximumLimit = 100
)

var (
	ErrInvalid    = errors.New("action recovery request is invalid")
	ErrNotFound   = errors.New("action recovery record was not found")
	ErrConflict   = errors.New("action recovery state conflicts with the request")
	ErrConstraint = errors.New("action recovery request is not eligible")
	ErrRepository = errors.New("action recovery repository is unavailable")
)

type State string

const (
	StateExecuting        State = "executing"
	StateReconciling      State = "reconciling"
	StateRetryWait        State = "retry_wait"
	StateSucceeded        State = "succeeded"
	StateFailed           State = "failed"
	StateUnknown          State = "unknown"
	StateManualResolution State = "manual_resolution"
)

func (state State) Valid() bool {
	switch state {
	case StateExecuting, StateReconciling, StateRetryWait, StateSucceeded, StateFailed, StateUnknown, StateManualResolution:
		return true
	default:
		return false
	}
}

type Summary struct {
	OperationID, ApprovalID, InvocationID string
	Capability, ExecutorID                string
	ExecutorVersion, PolicyVersion        uint64
	State                                 State
	AttemptCount                          uint32
	LastErrorCode                         string
	NextAttemptAt, CompletedAt            *time.Time
	StartedAt, UpdatedAt                  time.Time
}

type Resolution struct {
	ID, OperationID   string
	RequestedOutcome  State
	ReasonSHA256      [sha256.Size]byte
	RequestedByUserID ids.UserID
	RequestedAt       time.Time
	State             string
	ConfirmedByUserID ids.UserID
	ConfirmedAt       *time.Time
}

type Detail struct {
	Summary
	Resolution *Resolution
}

type Cursor struct {
	UpdatedAt   time.Time
	OperationID string
}

type ListQuery struct {
	State            State
	AfterUpdatedAt   *time.Time
	AfterOperationID string
	Limit            int
}

type Page struct {
	Items      []Summary
	NextCursor *Cursor
}

type Repository interface {
	List(context.Context, ids.AccountID, ListQuery) (Page, error)
	Get(context.Context, ids.AccountID, string) (Detail, error)
	RequestResolution(context.Context, ids.AccountID, string, string, State, [sha256.Size]byte, ids.UserID, time.Time) error
	ConfirmResolution(context.Context, ids.AccountID, string, string, ids.UserID, time.Time) error
}

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	authorizer Authorizer
	repository Repository
	clock      Clock
}

func New(authorizer Authorizer, repository Repository, clock Clock) (*Service, error) {
	if authorizer == nil || repository == nil || clock == nil {
		return nil, errors.New("action recovery dependencies are required")
	}
	return &Service{authorizer: authorizer, repository: repository, clock: clock}, nil
}

func (s *Service) List(ctx context.Context, actor access.Actor, accountID ids.AccountID, query ListQuery) (Page, error) {
	if !validHuman(actor) || ids.Validate(string(accountID)) != nil || (query.State != "" && !query.State.Valid()) || !validCursor(query) {
		return Page{}, ErrInvalid
	}
	if err := s.authorize(ctx, actor, accountID, false); err != nil {
		return Page{}, err
	}
	if query.Limit == 0 {
		query.Limit = DefaultLimit
	}
	if query.Limit < 1 || query.Limit > MaximumLimit {
		return Page{}, ErrInvalid
	}
	return s.repository.List(ctx, accountID, query)
}

func (s *Service) Get(ctx context.Context, actor access.Actor, accountID ids.AccountID, operationID string) (Detail, error) {
	if !validHuman(actor) || ids.Validate(string(accountID)) != nil || ids.Validate(operationID) != nil {
		return Detail{}, ErrInvalid
	}
	if err := s.authorize(ctx, actor, accountID, false); err != nil {
		return Detail{}, err
	}
	return s.repository.Get(ctx, accountID, operationID)
}

type RequestCommand struct {
	Actor                     access.Actor
	AccountID                 ids.AccountID
	OperationID, ResolutionID string
	Outcome                   State
	Reason                    string
}

func (s *Service) Request(ctx context.Context, command RequestCommand) (Detail, error) {
	if !validHuman(command.Actor) || ids.Validate(string(command.AccountID)) != nil || ids.Validate(command.OperationID) != nil || ids.Validate(command.ResolutionID) != nil ||
		(command.Outcome != StateSucceeded && command.Outcome != StateFailed) || !validReason(command.Reason) {
		return Detail{}, ErrInvalid
	}
	if err := s.authorize(ctx, command.Actor, command.AccountID, true); err != nil {
		return Detail{}, err
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(command.Reason)))
	if err := s.repository.RequestResolution(ctx, command.AccountID, command.OperationID, command.ResolutionID, command.Outcome, digest, command.Actor.UserID, s.clock.Now().UTC()); err != nil {
		return Detail{}, err
	}
	return s.repository.Get(ctx, command.AccountID, command.OperationID)
}

type ConfirmCommand struct {
	Actor                     access.Actor
	AccountID                 ids.AccountID
	OperationID, ResolutionID string
}

func (s *Service) Confirm(ctx context.Context, command ConfirmCommand) (Detail, error) {
	if !validHuman(command.Actor) || ids.Validate(string(command.AccountID)) != nil || ids.Validate(command.OperationID) != nil || ids.Validate(command.ResolutionID) != nil {
		return Detail{}, ErrInvalid
	}
	if err := s.authorize(ctx, command.Actor, command.AccountID, true); err != nil {
		return Detail{}, err
	}
	if err := s.repository.ConfirmResolution(ctx, command.AccountID, command.OperationID, command.ResolutionID, command.Actor.UserID, s.clock.Now().UTC()); err != nil {
		return Detail{}, err
	}
	return s.repository.Get(ctx, command.AccountID, command.OperationID)
}

func (s *Service) authorize(ctx context.Context, actor access.Actor, accountID ids.AccountID, mutation bool) error {
	accountContext, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator}, Package: catalog.PackageAgents, Mutation: mutation})
	if err != nil {
		return err
	}
	if accountContext.Role != accounts.RoleOwner && accountContext.Role != accounts.RoleAdministrator {
		return &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageAgents}
	}
	return nil
}

func validHuman(actor access.Actor) bool {
	return actor.UserID != "" && actor.WorkloadID == "" && ids.Validate(string(actor.UserID)) == nil
}
func validReason(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 3 && len(value) <= 1000
}
func validCursor(query ListQuery) bool {
	if (query.AfterUpdatedAt == nil) != (query.AfterOperationID == "") {
		return false
	}
	return query.AfterUpdatedAt == nil || (!query.AfterUpdatedAt.IsZero() && ids.Validate(query.AfterOperationID) == nil)
}
