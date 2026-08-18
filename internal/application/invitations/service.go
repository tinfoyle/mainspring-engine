package invitations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvitationNotFound      = errors.New("invitation not found")
	ErrInvitationExpired       = errors.New("invitation expired")
	ErrInvitationConsumed      = errors.New("invitation already consumed")
	ErrMembershipExists        = errors.New("membership already exists")
	ErrInvitationEmailMismatch = errors.New("invitation email does not match authenticated user")
)

type Repository interface {
	Create(context.Context, accounts.Invitation) error
	Delete(context.Context, ids.InvitationID) error
	Accept(context.Context, ids.UserID, [32]byte, time.Time, ids.MembershipID) (accounts.Membership, error)
}

type Message struct {
	InvitationID              ids.InvitationID
	AccountID                 ids.AccountID
	Email, AccountName, Token string
	Role                      accounts.MembershipRole
	ExpiresAt                 time.Time
}
type Sender interface {
	SendInvitation(context.Context, Message) error
}
type Clock interface{ Now() time.Time }

type Service struct {
	repository Repository
	sender     Sender
	authorizer *access.Authorizer
	ids        ids.Generator
	clock      Clock
	ttl        time.Duration
}

func NewService(repository Repository, sender Sender, authorizer *access.Authorizer, generator ids.Generator, clock Clock) (*Service, error) {
	if repository == nil || sender == nil || authorizer == nil || generator == nil || clock == nil {
		return nil, errors.New("invitation dependencies are required")
	}
	return &Service{repository: repository, sender: sender, authorizer: authorizer, ids: generator, clock: clock, ttl: 7 * 24 * time.Hour}, nil
}

type CreateCommand struct {
	ActorUserID ids.UserID
	Session     sessions.Session
	AccountID   ids.AccountID
	Email       string
	Role        accounts.MembershipRole
}
type Created struct {
	InvitationID ids.InvitationID
	ExpiresAt    time.Time
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (Created, error) {
	accountContext, err := s.authorizer.Authorize(ctx, access.Actor{UserID: command.ActorUserID}, command.AccountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator}})
	if err != nil {
		return Created{}, err
	}
	if err := strongauth.Require(command.Session, command.ActorUserID, s.clock.Now()); err != nil {
		return Created{}, err
	}
	email, err := identity.NormalizeEmail(command.Email)
	if err != nil {
		return Created{}, err
	}
	if !invitableRole(command.Role) {
		return Created{}, errors.New("role cannot be invited")
	}
	raw, hash, err := newToken()
	if err != nil {
		return Created{}, err
	}
	now := s.clock.Now().UTC()
	invitation := accounts.Invitation{ID: ids.InvitationID(s.ids.New()), AccountID: command.AccountID, Email: email, Role: command.Role, State: accounts.InvitationPending, InvitedByUserID: command.ActorUserID, TokenHash: hash, ExpiresAt: now.Add(s.ttl), CreatedAt: now}
	if err := s.repository.Create(ctx, invitation); err != nil {
		return Created{}, err
	}
	message := Message{InvitationID: invitation.ID, AccountID: invitation.AccountID, Email: email, AccountName: accountContext.AccountName, Token: raw, Role: invitation.Role, ExpiresAt: invitation.ExpiresAt}
	if err := s.sender.SendInvitation(ctx, message); err != nil {
		_ = s.repository.Delete(ctx, invitation.ID)
		return Created{}, err
	}
	return Created{InvitationID: invitation.ID, ExpiresAt: invitation.ExpiresAt}, nil
}

type AcceptCommand struct {
	UserID ids.UserID
	Token  string
}

func (s *Service) Accept(ctx context.Context, command AcceptCommand) (accounts.Membership, error) {
	if command.UserID == "" || command.Token == "" {
		return accounts.Membership{}, ErrInvitationNotFound
	}
	hash := sha256.Sum256([]byte(command.Token))
	return s.repository.Accept(ctx, command.UserID, hash, s.clock.Now().UTC(), ids.MembershipID(s.ids.New()))
}

func invitableRole(role accounts.MembershipRole) bool {
	return role == accounts.RoleAdministrator || role == accounts.RoleBillingAdmin || role == accounts.RoleMember || role == accounts.RoleViewer
}
func newToken() (string, [32]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [32]byte{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, sha256.Sum256([]byte(token)), nil
}
