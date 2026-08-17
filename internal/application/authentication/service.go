package authentication

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
)

var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrIdentityNotFound = errors.New("identity not found")

type LocalIdentity struct {
	User         identity.User
	PasswordHash string
}

type IdentitySource interface {
	LocalIdentity(context.Context, string) (LocalIdentity, error)
}

type AttemptLimiter interface {
	Blocked(context.Context, [32]byte, time.Time) (bool, error)
	Failure(context.Context, [32]byte, time.Time, int, time.Duration) error
	Success(context.Context, [32]byte) error
}

type PasswordVerifier interface {
	Verify(string, string) bool
}

type Service struct {
	identities IdentitySource
	limiter    AttemptLimiter
	passwords  PasswordVerifier
	sessions   *sessions.Service
	clock      sessions.Clock
	dummyHash  string
}

func NewService(identities IdentitySource, limiter AttemptLimiter, passwords PasswordVerifier, sessionService *sessions.Service, clock sessions.Clock, dummyHash string) (*Service, error) {
	if identities == nil || limiter == nil || passwords == nil || sessionService == nil || clock == nil || dummyHash == "" {
		return nil, errors.New("authentication dependencies are required")
	}
	return &Service{identities: identities, limiter: limiter, passwords: passwords, sessions: sessionService, clock: clock, dummyHash: dummyHash}, nil
}

type LoginCommand struct{ Email, Password string }

func (s *Service) Login(ctx context.Context, command LoginCommand) (sessions.Issued, error) {
	now := s.clock.Now().UTC()
	normalized, normalizeErr := identity.NormalizeEmail(command.Email)
	identifier := normalized
	if identifier == "" {
		identifier = strings.ToLower(strings.TrimSpace(command.Email))
	}
	key := sha256.Sum256([]byte(identifier))
	blocked, err := s.limiter.Blocked(ctx, key, now)
	if err != nil {
		return sessions.Issued{}, err
	}

	local, lookupErr := s.identities.LocalIdentity(ctx, normalized)
	encoded := s.dummyHash
	if lookupErr == nil {
		encoded = local.PasswordHash
	}
	passwordMatches := s.passwords.Verify(encoded, command.Password)
	if lookupErr != nil && !errors.Is(lookupErr, ErrIdentityNotFound) {
		return sessions.Issued{}, lookupErr
	}
	if blocked {
		return sessions.Issued{}, ErrInvalidCredentials
	}
	valid := normalizeErr == nil && lookupErr == nil && passwordMatches && local.User.State == identity.UserActive && !blocked
	if !valid {
		if err := s.limiter.Failure(ctx, key, now, 5, 15*time.Minute); err != nil {
			return sessions.Issued{}, err
		}
		return sessions.Issued{}, ErrInvalidCredentials
	}
	if err := s.limiter.Success(ctx, key); err != nil {
		return sessions.Issued{}, err
	}
	return s.sessions.Issue(ctx, local.User.ID, local.User.SecurityVersion)
}
