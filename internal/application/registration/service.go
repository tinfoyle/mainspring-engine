package registration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/placement"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrRegistrationNotFound = errors.New("registration not found")
	ErrRegistrationExpired  = errors.New("registration expired")
	ErrRegistrationConsumed = errors.New("registration already consumed")
	ErrEmailExists          = errors.New("email already registered")
)

type Clock interface{ Now() time.Time }

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

type Pending struct {
	ID          ids.RegistrationID
	User        identity.User
	AccountName string
	Region      string
	TokenHash   [32]byte
	ExpiresAt   time.Time
	CreatedAt   time.Time
	ConsumedAt  *time.Time
}

type Provisioned struct {
	User       identity.User
	Credential identity.LocalCredential
	Account    accounts.Account
	Membership accounts.Membership
	Assignment placement.Assignment
	Grants     []entitlements.Grant
	Snapshot   entitlements.Snapshot
}

type Repository interface {
	CreatePending(context.Context, Pending) error
	DeletePending(context.Context, ids.RegistrationID) error
	Complete(context.Context, [32]byte, time.Time, func(Pending) (Provisioned, error)) (Provisioned, error)
}

type VerificationMessage struct {
	RegistrationID ids.RegistrationID
	Email          string
	DisplayName    string
	Token          string
	ExpiresAt      time.Time
}

type VerificationSender interface {
	SendVerification(context.Context, VerificationMessage) error
}

type CellSource interface {
	AvailableCells(context.Context) ([]placement.Cell, error)
}

type PasswordHasher interface {
	Hash(string) (string, error)
}

type Service struct {
	repository Repository
	sender     VerificationSender
	cells      CellSource
	catalog    func() catalog.PublishedCatalog
	ids        ids.Generator
	clock      Clock
	passwords  PasswordHasher
	tokenTTL   time.Duration
}

func NewService(repository Repository, sender VerificationSender, cells CellSource, catalogSource func() catalog.PublishedCatalog, idGenerator ids.Generator, clock Clock, passwords PasswordHasher) *Service {
	return &Service{repository: repository, sender: sender, cells: cells, catalog: catalogSource, ids: idGenerator, clock: clock, passwords: passwords, tokenTTL: 30 * time.Minute}
}

type BeginCommand struct{ Email, DisplayName, AccountName, Region string }
type BeginResult struct {
	RegistrationID ids.RegistrationID
	ExpiresAt      time.Time
}

func (s *Service) Begin(ctx context.Context, command BeginCommand) (BeginResult, error) {
	now := s.clock.Now().UTC()
	userID := ids.UserID(s.ids.New())
	user, err := identity.NewPendingUser(userID, command.Email, command.DisplayName, now)
	if err != nil {
		return BeginResult{}, err
	}
	if command.Region == "" {
		command.Region = "us-east"
	}
	if _, err := accounts.NewAccount(ids.AccountID(s.ids.New()), userID, ids.CellID("validation"), command.AccountName, now); err != nil {
		return BeginResult{}, err
	}
	rawToken, hash, err := newToken()
	if err != nil {
		return BeginResult{}, err
	}
	pending := Pending{ID: ids.RegistrationID(s.ids.New()), User: user, AccountName: command.AccountName, Region: command.Region, TokenHash: hash, ExpiresAt: now.Add(s.tokenTTL), CreatedAt: now}
	if err := s.repository.CreatePending(ctx, pending); err != nil {
		return BeginResult{}, err
	}
	message := VerificationMessage{RegistrationID: pending.ID, Email: user.PrimaryEmail, DisplayName: user.DisplayName, Token: rawToken, ExpiresAt: pending.ExpiresAt}
	if err := s.sender.SendVerification(ctx, message); err != nil {
		_ = s.repository.DeletePending(ctx, pending.ID)
		return BeginResult{}, err
	}
	return BeginResult{RegistrationID: pending.ID, ExpiresAt: pending.ExpiresAt}, nil
}

type CompleteCommand struct{ Token, Password string }

func (s *Service) Complete(ctx context.Context, command CompleteCommand) (Provisioned, error) {
	if s.passwords == nil || s.catalog == nil {
		return Provisioned{}, errors.New("registration completion dependencies are not configured")
	}
	hash := sha256.Sum256([]byte(command.Token))
	now := s.clock.Now().UTC()
	passwordHash, err := s.passwords.Hash(command.Password)
	if err != nil {
		return Provisioned{}, err
	}
	cells, err := s.cells.AvailableCells(ctx)
	if err != nil {
		return Provisioned{}, err
	}
	return s.repository.Complete(ctx, hash, now, func(pending Pending) (Provisioned, error) {
		user, err := pending.User.VerifyEmail(now)
		if err != nil {
			return Provisioned{}, err
		}
		cell, err := placement.SelectCell(cells, pending.Region)
		if err != nil {
			return Provisioned{}, err
		}
		account, err := accounts.NewAccount(ids.AccountID(s.ids.New()), user.ID, cell.ID, pending.AccountName, now)
		if err != nil {
			return Provisioned{}, err
		}
		membership := accounts.NewOwnerMembership(ids.MembershipID(s.ids.New()), account.ID, user.ID, now)
		publication := s.catalog()
		plan, ok := publication.Plan("free")
		if !ok {
			return Provisioned{}, errors.New("published catalog has no free plan")
		}
		grants := entitlements.FreePlanGrants(account.ID, plan, s.ids, now)
		snapshot, err := entitlements.Evaluate(account.ID, account.EntitlementVersion, publication.Version, grants, now)
		if err != nil {
			return Provisioned{}, err
		}
		assignment := placement.Assignment{AccountID: account.ID, CellID: cell.ID, PlacementGeneration: account.PlacementGeneration, State: "active"}
		credential := identity.LocalCredential{UserID: user.ID, PasswordHash: passwordHash, CreatedAt: now, UpdatedAt: now}
		return Provisioned{User: user, Credential: credential, Account: account, Membership: membership, Assignment: assignment, Grants: grants, Snapshot: snapshot}, nil
	})
}

func newToken() (string, [32]byte, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", [32]byte{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(bytes[:])
	return token, sha256.Sum256([]byte(token)), nil
}
