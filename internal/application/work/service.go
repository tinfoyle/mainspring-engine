// Package work provides the transport-neutral Work command and query boundary.
package work

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	PackageCode catalog.PackageCode = "work"
	ActiveItems catalog.LimitCode   = "active_items"
	MaxPageSize                     = 100
)

var (
	ErrInvalidCommand         = errors.New("work command is invalid")
	ErrNotFound               = errors.New("work item not found")
	ErrConflict               = errors.New("work item version conflict")
	ErrConstraint             = errors.New("work item constraint failed")
	ErrCorrupt                = errors.New("work item persistence is corrupt")
	ErrCapacityReleasePending = errors.New("work item changed but its capacity release is pending")
	ErrCapacityCompensation   = errors.New("work creation failed and capacity compensation also failed")
)

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Capacity interface {
	Reserve(context.Context, usageadmission.ReserveCommand) (usageadmission.Reservation, error)
	Release(context.Context, usageadmission.ReleaseCommand) (usageadmission.Reservation, error)
}

type Repository interface {
	Create(context.Context, workdomain.Draft, Mutation) (workdomain.Item, error)
	Get(context.Context, ids.AccountID, ids.WorkItemID) (workdomain.Item, error)
	Update(context.Context, workdomain.Item, uint64, Mutation) (workdomain.Item, error)
	MarkCapacityReleased(context.Context, ids.AccountID, ids.WorkItemID, string, time.Time) error
	List(context.Context, ids.AccountID, ListQuery) (Page, error)
	Children(context.Context, ids.AccountID, ids.WorkItemID, int) ([]workdomain.Item, error)
	Summary(context.Context, ids.AccountID) (Summary, error)
}

type Clock interface{ Now() time.Time }

type Mutation struct {
	Kind          MutationKind
	Actor         workdomain.Actor
	Reason        string
	CorrelationID string
	At            time.Time
}

type MutationKind string

const (
	MutationCreated      MutationKind = "created"
	MutationTransitioned MutationKind = "transitioned"
	MutationAssigned     MutationKind = "assigned"
)

type ListQuery struct {
	States         []workdomain.State
	Kinds          []workdomain.Kind
	Search         string
	AfterUpdatedAt *time.Time
	AfterID        ids.WorkItemID
	Limit          int
}

type Page struct {
	Items      []workdomain.Item
	NextCursor *Cursor
}

type Cursor struct {
	UpdatedAt time.Time
	ID        ids.WorkItemID
}

type Summary struct {
	Active     uint64
	InProgress uint64
	Waiting    uint64
	Urgent     uint64
	Done       uint64
}

type Service struct {
	authorizer Authorizer
	capacity   Capacity
	repository Repository
	clock      Clock
}

func NewService(authorizer Authorizer, capacity Capacity, repository Repository, clock Clock) (*Service, error) {
	if authorizer == nil || capacity == nil || repository == nil || clock == nil {
		return nil, errors.New("work service dependencies are required")
	}
	return &Service{authorizer: authorizer, capacity: capacity, repository: repository, clock: clock}, nil
}

type CreateCommand struct {
	Actor         access.Actor
	AccountID     ids.AccountID
	RequestID     string
	ParentID      ids.WorkItemID
	Kind          workdomain.Kind
	Title         string
	Description   string
	Priority      workdomain.Priority
	Assignment    workdomain.Assignment
	Provenance    workdomain.Provenance
	DueAt         *time.Time
	Reason        string
	CorrelationID string
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (workdomain.Item, error) {
	if ids.Validate(command.RequestID) != nil || command.AccountID == "" || !command.Actor.Valid() || strings.TrimSpace(command.CorrelationID) == "" {
		return workdomain.Item{}, ErrInvalidCommand
	}
	accountContext, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Package: PackageCode, Mutation: true})
	if err != nil {
		return workdomain.Item{}, err
	}
	if !canManage(accountContext.Role) {
		return workdomain.Item{}, &access.DeniedError{Code: access.DenialRole, Package: PackageCode}
	}
	draft, err := workdomain.NewDraft(workdomain.Draft{ID: ids.WorkItemID(command.RequestID), AccountID: command.AccountID, ParentID: command.ParentID, Kind: command.Kind, Title: command.Title, Description: command.Description, Priority: command.Priority, Assignment: command.Assignment, Provenance: command.Provenance, DueAt: command.DueAt, CapacityReservationID: command.RequestID})
	if err != nil {
		return workdomain.Item{}, ErrInvalidCommand
	}
	if _, err := s.capacity.Reserve(ctx, usageadmission.ReserveCommand{Actor: command.Actor, AccountID: command.AccountID, PackageCode: PackageCode, LimitCode: ActiveItems, Amount: 1, RequestID: command.RequestID}); err != nil {
		return workdomain.Item{}, err
	}
	now := s.clock.Now().UTC()
	item, err := s.repository.Create(ctx, draft, Mutation{Kind: MutationCreated, Actor: domainActor(command.Actor), Reason: strings.TrimSpace(command.Reason), CorrelationID: command.CorrelationID, At: now})
	if err == nil {
		return item, nil
	}
	if _, releaseErr := s.capacity.Release(ctx, usageadmission.ReleaseCommand{Actor: command.Actor, AccountID: command.AccountID, RequestID: command.RequestID}); releaseErr != nil {
		return workdomain.Item{}, errors.Join(err, ErrCapacityCompensation, releaseErr)
	}
	return workdomain.Item{}, err
}

type TransitionCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	WorkItemID      ids.WorkItemID
	To              workdomain.State
	ExpectedVersion uint64
	RequestID       string
	Reason          string
	CorrelationID   string
}

func (s *Service) Transition(ctx context.Context, command TransitionCommand) (workdomain.Item, error) {
	if command.WorkItemID == "" || command.ExpectedVersion == 0 || strings.TrimSpace(command.CorrelationID) == "" || !command.Actor.Valid() {
		return workdomain.Item{}, ErrInvalidCommand
	}
	accountContext, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Package: PackageCode, Mutation: true})
	if err != nil {
		return workdomain.Item{}, err
	}
	item, err := s.repository.Get(ctx, command.AccountID, command.WorkItemID)
	if err != nil {
		return workdomain.Item{}, err
	}
	now := s.clock.Now().UTC()
	updated, err := item.Transition(workdomain.TransitionCommand{To: command.To, Role: accountContext.Role, Actor: domainActor(command.Actor), Reason: command.Reason, ExpectedVersion: command.ExpectedVersion, At: now})
	if err != nil {
		return workdomain.Item{}, err
	}
	reopening := item.State == workdomain.StateDone && command.To == workdomain.StateOpen
	if reopening {
		if ids.Validate(command.RequestID) != nil {
			return workdomain.Item{}, ErrInvalidCommand
		}
		if _, err := s.capacity.Reserve(ctx, usageadmission.ReserveCommand{Actor: command.Actor, AccountID: command.AccountID, PackageCode: PackageCode, LimitCode: ActiveItems, Amount: 1, RequestID: command.RequestID}); err != nil {
			return workdomain.Item{}, err
		}
		updated, err = updated.WithReopenedCapacity(command.RequestID)
		if err != nil {
			return workdomain.Item{}, err
		}
	}
	updated, err = s.repository.Update(ctx, updated, item.Version, Mutation{Kind: MutationTransitioned, Actor: domainActor(command.Actor), Reason: strings.TrimSpace(command.Reason), CorrelationID: command.CorrelationID, At: now})
	if err != nil {
		if reopening {
			_, _ = s.capacity.Release(ctx, usageadmission.ReleaseCommand{Actor: command.Actor, AccountID: command.AccountID, RequestID: command.RequestID})
		}
		return workdomain.Item{}, err
	}
	if updated.State.Terminal() && !item.State.Terminal() && updated.CapacityReservationID != "" {
		if _, releaseErr := s.capacity.Release(ctx, usageadmission.ReleaseCommand{Actor: command.Actor, AccountID: command.AccountID, RequestID: updated.CapacityReservationID}); releaseErr != nil {
			return updated, errors.Join(ErrCapacityReleasePending, releaseErr)
		}
		if markErr := s.repository.MarkCapacityReleased(ctx, command.AccountID, updated.ID, updated.CapacityReservationID, now); markErr != nil {
			return updated, errors.Join(ErrCapacityReleasePending, markErr)
		}
	}
	return updated, nil
}

type AssignCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	WorkItemID      ids.WorkItemID
	Assignment      workdomain.Assignment
	ExpectedVersion uint64
	Reason          string
	CorrelationID   string
}

func (s *Service) Assign(ctx context.Context, command AssignCommand) (workdomain.Item, error) {
	if command.WorkItemID == "" || command.ExpectedVersion == 0 || strings.TrimSpace(command.CorrelationID) == "" || !command.Actor.Valid() {
		return workdomain.Item{}, ErrInvalidCommand
	}
	accountContext, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Package: PackageCode, Mutation: true})
	if err != nil {
		return workdomain.Item{}, err
	}
	item, err := s.repository.Get(ctx, command.AccountID, command.WorkItemID)
	if err != nil {
		return workdomain.Item{}, err
	}
	now := s.clock.Now().UTC()
	updated, err := item.Assign(workdomain.AssignmentCommand{Assignment: command.Assignment, Role: accountContext.Role, Actor: domainActor(command.Actor), ExpectedVersion: command.ExpectedVersion, At: now})
	if err != nil {
		return workdomain.Item{}, err
	}
	return s.repository.Update(ctx, updated, item.Version, Mutation{Kind: MutationAssigned, Actor: domainActor(command.Actor), Reason: strings.TrimSpace(command.Reason), CorrelationID: command.CorrelationID, At: now})
}

func (s *Service) List(ctx context.Context, actor access.Actor, accountID ids.AccountID, query ListQuery) (Page, error) {
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: PackageCode}); err != nil {
		return Page{}, err
	}
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > MaxPageSize || len(query.States) > 5 || len(query.Kinds) > 2 || len(query.Search) > 200 || (query.AfterUpdatedAt == nil) != (query.AfterID == "") {
		return Page{}, ErrInvalidCommand
	}
	return s.repository.List(ctx, accountID, query)
}

func (s *Service) Children(ctx context.Context, actor access.Actor, accountID ids.AccountID, parentID ids.WorkItemID, limit int) ([]workdomain.Item, error) {
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: PackageCode}); err != nil {
		return nil, err
	}
	if parentID == "" || limit <= 0 || limit > MaxPageSize {
		return nil, ErrInvalidCommand
	}
	return s.repository.Children(ctx, accountID, parentID, limit)
}

func (s *Service) Summary(ctx context.Context, actor access.Actor, accountID ids.AccountID) (Summary, error) {
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: PackageCode}); err != nil {
		return Summary{}, err
	}
	return s.repository.Summary(ctx, accountID)
}

func canManage(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator || role == accounts.RoleMember
}

func domainActor(actor access.Actor) workdomain.Actor {
	if actor.UserID != "" {
		return workdomain.Actor{Kind: workdomain.ActorUser, ID: string(actor.UserID)}
	}
	return workdomain.Actor{Kind: workdomain.ActorWorkload, ID: actor.WorkloadID}
}
