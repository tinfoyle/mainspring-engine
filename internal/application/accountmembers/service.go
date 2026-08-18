// Package accountmembers owns Account Membership administration and ownership
// continuity. Identity authentication and Account authorization remain separate
// inputs to this application boundary.
package accountmembers

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
	ErrMembershipNotFound = errors.New("membership not found")
	ErrVersionConflict    = errors.New("membership version conflict")
	ErrRoleInvalid        = errors.New("membership role is invalid")
	ErrOwnershipRequired  = errors.New("Account must retain one active owner")
	ErrTargetDenied       = errors.New("target Membership cannot be managed by this actor")
	ErrReasonRequired     = errors.New("a reason between 3 and 300 characters is required")
	ErrStateConflict      = errors.New("Membership state does not allow this transition")
)

type Member struct {
	MembershipID ids.MembershipID         `json:"membership_id"`
	UserID       ids.UserID               `json:"user_id"`
	DisplayName  string                   `json:"display_name"`
	Email        string                   `json:"email"`
	Role         accounts.MembershipRole  `json:"role"`
	State        accounts.MembershipState `json:"state"`
	Version      uint64                   `json:"version"`
	CreatedAt    time.Time                `json:"created_at"`
}

type ChangeRoleMutation struct {
	EventID, Reason    string
	ActorUserID        ids.UserID
	ExpectedActorRole  accounts.MembershipRole
	AccountID          ids.AccountID
	TargetMembershipID ids.MembershipID
	ExpectedVersion    uint64
	Role               accounts.MembershipRole
	At                 time.Time
}

type RemoveMutation struct {
	EventID, Reason    string
	ActorUserID        ids.UserID
	ExpectedActorRole  accounts.MembershipRole
	AccountID          ids.AccountID
	TargetMembershipID ids.MembershipID
	ExpectedVersion    uint64
	At                 time.Time
}

type TransferMutation struct {
	EventID, PreviousOwnerNoticeID, NewOwnerNoticeID, Reason string
	ActorUserID                                              ids.UserID
	AccountID                                                ids.AccountID
	TargetMembershipID                                       ids.MembershipID
	ExpectedActorVersion                                     uint64
	ExpectedTargetVersion                                    uint64
	At                                                       time.Time
}

type OwnershipNoticeRole string

const (
	OwnershipNoticePreviousOwner OwnershipNoticeRole = "previous_owner"
	OwnershipNoticeNewOwner      OwnershipNoticeRole = "new_owner"
)

type OwnershipTransferNotice struct {
	Email, DisplayName, AccountName, CounterpartDisplayName string
	RecipientRole                                           OwnershipNoticeRole
	OccurredAt                                              time.Time
}

type PreparedNotification struct {
	ID         string
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
	CreatedAt  time.Time
}

type OwnershipNotificationPreparer interface {
	PrepareOwnershipTransfer(string, OwnershipTransferNotice) (PreparedNotification, error)
}

type OwnershipTransferSender interface {
	SendOwnershipTransfer(context.Context, OwnershipTransferNotice) error
}

type StateAction string

const (
	StateActionSuspend    StateAction = "suspend"
	StateActionReactivate StateAction = "reactivate"
	StateActionLeave      StateAction = "leave"
)

type StateMutation struct {
	EventID, Reason    string
	Action             StateAction
	ActorUserID        ids.UserID
	ExpectedActorRole  accounts.MembershipRole
	AccountID          ids.AccountID
	TargetMembershipID ids.MembershipID
	ExpectedVersion    uint64
	At                 time.Time
}

type TransferResult struct {
	PreviousOwner Member `json:"previous_owner"`
	NewOwner      Member `json:"new_owner"`
}

type Repository interface {
	List(context.Context, ids.AccountID) ([]Member, error)
	Current(context.Context, ids.AccountID, ids.UserID) (Member, error)
	ChangeRole(context.Context, ChangeRoleMutation) (Member, error)
	Remove(context.Context, RemoveMutation) error
	ChangeState(context.Context, StateMutation) (Member, error)
	TransferOwnership(context.Context, TransferMutation) (TransferResult, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository Repository
	authorizer *access.Authorizer
	ids        ids.Generator
	clock      Clock
}

func NewService(repository Repository, authorizer *access.Authorizer, generator ids.Generator, clock Clock) (*Service, error) {
	if repository == nil || authorizer == nil || generator == nil || clock == nil {
		return nil, errors.New("Account Membership dependencies are required")
	}
	return &Service{repository: repository, authorizer: authorizer, ids: generator, clock: clock}, nil
}

func (s *Service) List(ctx context.Context, actorUserID ids.UserID, accountID ids.AccountID) ([]Member, error) {
	if _, err := s.authorizer.Authorize(ctx, access.Actor{UserID: actorUserID}, accountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator}}); err != nil {
		return nil, err
	}
	return s.repository.List(ctx, accountID)
}

func (s *Service) Current(ctx context.Context, actorUserID ids.UserID, accountID ids.AccountID) (Member, error) {
	if _, err := s.authorizer.Authorize(ctx, access.Actor{UserID: actorUserID}, accountID, access.Requirement{}); err != nil {
		return Member{}, err
	}
	return s.repository.Current(ctx, accountID, actorUserID)
}

type ChangeRoleCommand struct {
	ActorUserID        ids.UserID
	Session            sessions.Session
	AccountID          ids.AccountID
	TargetMembershipID ids.MembershipID
	ExpectedVersion    uint64
	Role               accounts.MembershipRole
	Reason             string
}

func (s *Service) ChangeRole(ctx context.Context, command ChangeRoleCommand) (Member, error) {
	accountContext, err := s.authorizer.Authorize(ctx, access.Actor{UserID: command.ActorUserID}, command.AccountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner}})
	if err != nil {
		return Member{}, err
	}
	if err := strongauth.Require(command.Session, command.ActorUserID, s.clock.Now()); err != nil {
		return Member{}, err
	}
	if command.TargetMembershipID == "" || command.ExpectedVersion == 0 {
		return Member{}, ErrMembershipNotFound
	}
	if !assignableRole(command.Role) {
		return Member{}, ErrRoleInvalid
	}
	reason, err := normalizeReason(command.Reason)
	if err != nil {
		return Member{}, err
	}
	return s.repository.ChangeRole(ctx, ChangeRoleMutation{EventID: s.ids.New(), Reason: reason, ActorUserID: command.ActorUserID, ExpectedActorRole: accountContext.Role, AccountID: command.AccountID, TargetMembershipID: command.TargetMembershipID, ExpectedVersion: command.ExpectedVersion, Role: command.Role, At: s.clock.Now().UTC()})
}

type RemoveCommand struct {
	ActorUserID        ids.UserID
	Session            sessions.Session
	AccountID          ids.AccountID
	TargetMembershipID ids.MembershipID
	ExpectedVersion    uint64
	Reason             string
}

func (s *Service) Remove(ctx context.Context, command RemoveCommand) error {
	accountContext, err := s.authorizer.Authorize(ctx, access.Actor{UserID: command.ActorUserID}, command.AccountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator}})
	if err != nil {
		return err
	}
	if err := strongauth.Require(command.Session, command.ActorUserID, s.clock.Now()); err != nil {
		return err
	}
	if command.TargetMembershipID == "" || command.ExpectedVersion == 0 {
		return ErrMembershipNotFound
	}
	reason, err := normalizeReason(command.Reason)
	if err != nil {
		return err
	}
	return s.repository.Remove(ctx, RemoveMutation{EventID: s.ids.New(), Reason: reason, ActorUserID: command.ActorUserID, ExpectedActorRole: accountContext.Role, AccountID: command.AccountID, TargetMembershipID: command.TargetMembershipID, ExpectedVersion: command.ExpectedVersion, At: s.clock.Now().UTC()})
}

type StateCommand struct {
	ActorUserID        ids.UserID
	Session            sessions.Session
	AccountID          ids.AccountID
	TargetMembershipID ids.MembershipID
	ExpectedVersion    uint64
	Reason             string
}

func (s *Service) Suspend(ctx context.Context, command StateCommand) (Member, error) {
	return s.changeState(ctx, command, StateActionSuspend)
}

func (s *Service) Reactivate(ctx context.Context, command StateCommand) (Member, error) {
	return s.changeState(ctx, command, StateActionReactivate)
}

type LeaveCommand struct {
	ActorUserID     ids.UserID
	Session         sessions.Session
	AccountID       ids.AccountID
	ExpectedVersion uint64
	Reason          string
}

func (s *Service) Leave(ctx context.Context, command LeaveCommand) error {
	accountContext, err := s.authorizer.Authorize(ctx, access.Actor{UserID: command.ActorUserID}, command.AccountID, access.Requirement{})
	if err != nil {
		return err
	}
	if err := strongauth.Require(command.Session, command.ActorUserID, s.clock.Now()); err != nil {
		return err
	}
	if command.ExpectedVersion == 0 {
		return ErrMembershipNotFound
	}
	reason, err := normalizeReason(command.Reason)
	if err != nil {
		return err
	}
	_, err = s.repository.ChangeState(ctx, StateMutation{EventID: s.ids.New(), Reason: reason, Action: StateActionLeave, ActorUserID: command.ActorUserID, ExpectedActorRole: accountContext.Role, AccountID: command.AccountID, ExpectedVersion: command.ExpectedVersion, At: s.clock.Now().UTC()})
	return err
}

func (s *Service) changeState(ctx context.Context, command StateCommand, action StateAction) (Member, error) {
	accountContext, err := s.authorizer.Authorize(ctx, access.Actor{UserID: command.ActorUserID}, command.AccountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator}})
	if err != nil {
		return Member{}, err
	}
	if err := strongauth.Require(command.Session, command.ActorUserID, s.clock.Now()); err != nil {
		return Member{}, err
	}
	if command.TargetMembershipID == "" || command.ExpectedVersion == 0 {
		return Member{}, ErrMembershipNotFound
	}
	reason, err := normalizeReason(command.Reason)
	if err != nil {
		return Member{}, err
	}
	return s.repository.ChangeState(ctx, StateMutation{EventID: s.ids.New(), Reason: reason, Action: action, ActorUserID: command.ActorUserID, ExpectedActorRole: accountContext.Role, AccountID: command.AccountID, TargetMembershipID: command.TargetMembershipID, ExpectedVersion: command.ExpectedVersion, At: s.clock.Now().UTC()})
}

type TransferOwnershipCommand struct {
	ActorUserID           ids.UserID
	Session               sessions.Session
	AccountID             ids.AccountID
	TargetMembershipID    ids.MembershipID
	ExpectedActorVersion  uint64
	ExpectedTargetVersion uint64
	Reason                string
}

func (s *Service) TransferOwnership(ctx context.Context, command TransferOwnershipCommand) (TransferResult, error) {
	if _, err := s.authorizer.Authorize(ctx, access.Actor{UserID: command.ActorUserID}, command.AccountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner}}); err != nil {
		return TransferResult{}, err
	}
	if err := strongauth.Require(command.Session, command.ActorUserID, s.clock.Now()); err != nil {
		return TransferResult{}, err
	}
	if command.TargetMembershipID == "" || command.ExpectedActorVersion == 0 || command.ExpectedTargetVersion == 0 {
		return TransferResult{}, ErrMembershipNotFound
	}
	reason, err := normalizeReason(command.Reason)
	if err != nil {
		return TransferResult{}, err
	}
	return s.repository.TransferOwnership(ctx, TransferMutation{EventID: s.ids.New(), PreviousOwnerNoticeID: s.ids.New(), NewOwnerNoticeID: s.ids.New(), Reason: reason, ActorUserID: command.ActorUserID, AccountID: command.AccountID, TargetMembershipID: command.TargetMembershipID, ExpectedActorVersion: command.ExpectedActorVersion, ExpectedTargetVersion: command.ExpectedTargetVersion, At: s.clock.Now().UTC()})
}

func assignableRole(role accounts.MembershipRole) bool {
	return role == accounts.RoleAdministrator || role == accounts.RoleBillingAdmin || role == accounts.RoleMember || role == accounts.RoleViewer
}

func normalizeReason(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 3 || len(value) > 300 {
		return "", ErrReasonRequired
	}
	return value, nil
}
