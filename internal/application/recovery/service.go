// Package recovery owns system-wide credential recovery. Recovery is bound to
// a User identity, never to an Account or Membership.
package recovery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidChallenge = errors.New("credential recovery challenge is invalid or expired")
	ErrInvalidPassword  = errors.New("new password does not satisfy policy")
)

type Clock interface{ Now() time.Time }
type PasswordHasher interface{ Hash(string) (string, error) }

type AttemptLimiter interface {
	Blocked(context.Context, [32]byte, time.Time) (bool, error)
	Failure(context.Context, [32]byte, time.Time, int, time.Duration) error
}

type Pending struct {
	ID        ids.RecoveryID
	Email     string
	TokenHash [sha256.Size]byte
	ExpiresAt time.Time
	CreatedAt time.Time
}

type Recipient struct {
	UserID      ids.UserID
	Email       string
	DisplayName string
}

type Repository interface {
	Create(context.Context, Pending) (Recipient, bool, error)
	Delete(context.Context, ids.RecoveryID) error
	Complete(context.Context, [sha256.Size]byte, string, time.Time) (ids.UserID, error)
}

type Message struct {
	RecoveryID  ids.RecoveryID
	Email       string
	DisplayName string
	Token       string
	ExpiresAt   time.Time
	Suppress    bool
}

type Sender interface {
	SendRecovery(context.Context, Message) error
}

type Service struct {
	repository Repository
	sender     Sender
	limiter    AttemptLimiter
	passwords  PasswordHasher
	ids        ids.Generator
	clock      Clock
	tokenTTL   time.Duration
}

func NewService(repository Repository, sender Sender, limiter AttemptLimiter, passwords PasswordHasher, generator ids.Generator, clock Clock) (*Service, error) {
	if repository == nil || sender == nil || limiter == nil || passwords == nil || generator == nil || clock == nil {
		return nil, errors.New("credential recovery dependencies are required")
	}
	return &Service{repository: repository, sender: sender, limiter: limiter, passwords: passwords, ids: generator, clock: clock, tokenTTL: 30 * time.Minute}, nil
}

type BeginCommand struct{ Email string }
type BeginResult struct {
	RecoveryID ids.RecoveryID
	ExpiresAt  time.Time
	Delivered  bool
}

func (s *Service) Begin(ctx context.Context, command BeginCommand) (BeginResult, error) {
	now := s.clock.Now().UTC()
	normalized, err := identity.NormalizeEmail(command.Email)
	if err != nil {
		normalized = strings.ToLower(strings.TrimSpace(command.Email))
	}
	limitKey := sha256.Sum256([]byte("recovery:" + normalized))
	blocked, err := s.limiter.Blocked(ctx, limitKey, now)
	if err != nil {
		return BeginResult{}, err
	}
	token, tokenHash, err := newToken()
	if err != nil {
		return BeginResult{}, err
	}
	expiresAt := now.Add(s.tokenTTL)
	if blocked {
		return BeginResult{}, s.sender.SendRecovery(ctx, Message{Token: token, ExpiresAt: expiresAt, Suppress: true})
	}
	if err := s.limiter.Failure(ctx, limitKey, now, 3, 30*time.Minute); err != nil {
		return BeginResult{}, err
	}
	pending := Pending{ID: ids.RecoveryID(s.ids.New()), Email: normalized, TokenHash: tokenHash, ExpiresAt: expiresAt, CreatedAt: now}
	recipient, exists, err := s.repository.Create(ctx, pending)
	if err != nil {
		return BeginResult{}, err
	}
	if !exists {
		return BeginResult{}, s.sender.SendRecovery(ctx, Message{Token: token, ExpiresAt: expiresAt, Suppress: true})
	}
	message := Message{RecoveryID: pending.ID, Email: recipient.Email, DisplayName: recipient.DisplayName, Token: token, ExpiresAt: pending.ExpiresAt}
	if err := s.sender.SendRecovery(ctx, message); err != nil {
		_ = s.repository.Delete(ctx, pending.ID)
		return BeginResult{}, err
	}
	return BeginResult{RecoveryID: pending.ID, ExpiresAt: pending.ExpiresAt, Delivered: true}, nil
}

type CompleteCommand struct{ Token, Password string }

func (s *Service) Complete(ctx context.Context, command CompleteCommand) error {
	passwordHash, err := s.passwords.Hash(command.Password)
	if err != nil {
		return errors.Join(ErrInvalidPassword, err)
	}
	tokenHash := sha256.Sum256([]byte(command.Token))
	_, err = s.repository.Complete(ctx, tokenHash, passwordHash, s.clock.Now().UTC())
	return err
}

func newToken() (string, [sha256.Size]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [sha256.Size]byte{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, sha256.Sum256([]byte(token)), nil
}
