// Package contactchange owns verified changes to the system-wide Infinite Ocean
// identity email. The old address remains authoritative until a single-use
// token delivered to the new mailbox is consumed.
package contactchange

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrNotFound      = errors.New("contact change not found")
	ErrExpired       = errors.New("contact change expired")
	ErrConsumed      = errors.New("contact change already consumed")
	ErrEmailExists   = errors.New("email already registered")
	ErrSameEmail     = errors.New("new email matches the current email")
	ErrStaleIdentity = errors.New("identity changed after contact change began")
	ErrInvalidUser   = errors.New("identity is not active")
)

type Action string

const (
	ActionVerifyNew Action = "verify_new"
	ActionRequested Action = "requested"
	ActionCompleted Action = "completed"
)

type Pending struct {
	ID              ids.ContactChangeID
	UserID          ids.UserID
	OldEmail        string
	NewEmail        string
	DisplayName     string
	SecurityVersion uint64
	TokenHash       [32]byte
	ExpiresAt       time.Time
	CreatedAt       time.Time
	ConsumedAt      *time.Time
}

type Completed struct {
	UserID          ids.UserID
	OldEmail        string
	NewEmail        string
	SecurityVersion uint64
	ChangedAt       time.Time
}

type Message struct {
	Action      Action
	UserID      ids.UserID
	Email       string
	DisplayName string
	OldEmail    string
	NewEmail    string
	Token       string
	ExpiresAt   time.Time
	OccurredAt  time.Time
}

type PreparedNotification struct {
	ID         string
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
	CreatedAt  time.Time
}

type Repository interface {
	User(context.Context, ids.UserID) (identity.User, error)
	CreatePending(context.Context, Pending, []PreparedNotification) error
	Complete(context.Context, [32]byte, time.Time, func(Pending) ([]PreparedNotification, error)) (Completed, error)
}

type NotificationPreparer interface {
	PrepareContactChange(string, Message) (PreparedNotification, error)
}

type Sender interface {
	SendContactChange(context.Context, Message) error
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository Repository
	preparer   NotificationPreparer
	ids        ids.Generator
	clock      Clock
	tokenTTL   time.Duration
}

func NewService(repository Repository, preparer NotificationPreparer, generator ids.Generator, clock Clock) (*Service, error) {
	if repository == nil || preparer == nil || generator == nil || clock == nil {
		return nil, errors.New("contact change dependencies are required")
	}
	return &Service{repository: repository, preparer: preparer, ids: generator, clock: clock, tokenTTL: 30 * time.Minute}, nil
}

type BeginCommand struct {
	Session  sessions.Session
	NewEmail string
}

type BeginResult struct {
	ID        ids.ContactChangeID
	NewEmail  string
	ExpiresAt time.Time
}

func (s *Service) Begin(ctx context.Context, command BeginCommand) (BeginResult, error) {
	now := s.clock.Now().UTC()
	if err := strongauth.Require(command.Session, command.Session.UserID, now); err != nil {
		return BeginResult{}, err
	}
	user, err := s.repository.User(ctx, command.Session.UserID)
	if err != nil {
		return BeginResult{}, err
	}
	if user.State != identity.UserActive || user.EmailVerifiedAt == nil || user.SecurityVersion != command.Session.SecurityVersion {
		return BeginResult{}, ErrInvalidUser
	}
	newEmail, err := identity.NormalizeEmail(command.NewEmail)
	if err != nil {
		return BeginResult{}, err
	}
	if newEmail == user.PrimaryEmail {
		return BeginResult{}, ErrSameEmail
	}
	rawToken, tokenHash, err := newToken()
	if err != nil {
		return BeginResult{}, err
	}
	pending := Pending{
		ID: ids.ContactChangeID(s.ids.New()), UserID: user.ID, OldEmail: user.PrimaryEmail,
		NewEmail: newEmail, DisplayName: user.DisplayName, SecurityVersion: user.SecurityVersion,
		TokenHash: tokenHash, ExpiresAt: now.Add(s.tokenTTL), CreatedAt: now,
	}
	verification, err := s.preparer.PrepareContactChange(s.ids.New(), Message{
		Action: ActionVerifyNew, UserID: user.ID, Email: newEmail, DisplayName: user.DisplayName,
		OldEmail: user.PrimaryEmail, NewEmail: newEmail, Token: rawToken, ExpiresAt: pending.ExpiresAt,
	})
	if err != nil {
		return BeginResult{}, err
	}
	requested, err := s.preparer.PrepareContactChange(s.ids.New(), Message{
		Action: ActionRequested, UserID: user.ID, Email: user.PrimaryEmail, DisplayName: user.DisplayName,
		OldEmail: user.PrimaryEmail, NewEmail: newEmail, OccurredAt: now,
	})
	if err != nil {
		return BeginResult{}, err
	}
	if err := s.repository.CreatePending(ctx, pending, []PreparedNotification{verification, requested}); err != nil {
		return BeginResult{}, err
	}
	return BeginResult{ID: pending.ID, NewEmail: newEmail, ExpiresAt: pending.ExpiresAt}, nil
}

type CompleteCommand struct{ Token string }

func (s *Service) Complete(ctx context.Context, command CompleteCommand) (Completed, error) {
	hash := sha256.Sum256([]byte(command.Token))
	now := s.clock.Now().UTC()
	return s.repository.Complete(ctx, hash, now, func(pending Pending) ([]PreparedNotification, error) {
		result := make([]PreparedNotification, 0, 2)
		for _, recipient := range []string{pending.OldEmail, pending.NewEmail} {
			prepared, err := s.preparer.PrepareContactChange(s.ids.New(), Message{
				Action: ActionCompleted, UserID: pending.UserID, Email: recipient, DisplayName: pending.DisplayName,
				OldEmail: pending.OldEmail, NewEmail: pending.NewEmail, OccurredAt: now,
			})
			if err != nil {
				return nil, err
			}
			result = append(result, prepared)
		}
		return result, nil
	})
}

func (s *Service) Current(ctx context.Context, userID ids.UserID) (identity.User, error) {
	if userID == "" {
		return identity.User{}, ErrInvalidUser
	}
	return s.repository.User(ctx, userID)
}

func newToken() (string, [32]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [32]byte{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, sha256.Sum256([]byte(token)), nil
}
