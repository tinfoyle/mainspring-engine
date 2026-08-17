package sessions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidSession = errors.New("invalid session")
	ErrExpiredSession = errors.New("expired session")
)

type Session struct {
	ID              ids.SessionID
	UserID          ids.UserID
	TokenHash       [32]byte
	SecurityVersion uint64
	AuthenticatedAt time.Time
	LastSeenAt      time.Time
	RotatedAt       time.Time
	ExpiresAt       time.Time
	RevokedAt       *time.Time
}

type Repository interface {
	Create(context.Context, Session) error
	Use(context.Context, [32]byte, time.Time, time.Duration) (Session, error)
	Rotate(context.Context, ids.SessionID, [32]byte, [32]byte, time.Time) (bool, error)
	Revoke(context.Context, ids.SessionID, time.Time) error
	RevokeAll(context.Context, ids.UserID, time.Time) error
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository       Repository
	ids              ids.Generator
	clock            Clock
	absoluteTTL      time.Duration
	idleTTL          time.Duration
	rotationInterval time.Duration
}

func NewService(repository Repository, generator ids.Generator, clock Clock, absoluteTTL, idleTTL, rotationInterval time.Duration) (*Service, error) {
	if repository == nil || generator == nil || clock == nil {
		return nil, errors.New("session dependencies are required")
	}
	if absoluteTTL <= 0 || idleTTL <= 0 || rotationInterval <= 0 || idleTTL > absoluteTTL || rotationInterval > idleTTL {
		return nil, errors.New("invalid session lifetime policy")
	}
	return &Service{repository: repository, ids: generator, clock: clock, absoluteTTL: absoluteTTL, idleTTL: idleTTL, rotationInterval: rotationInterval}, nil
}

type Issued struct {
	Session Session
	Token   string
}

func (s *Service) Issue(ctx context.Context, userID ids.UserID, securityVersion uint64) (Issued, error) {
	if userID == "" || securityVersion == 0 {
		return Issued{}, errors.New("user ID and security version are required")
	}
	token, hash, err := newToken()
	if err != nil {
		return Issued{}, err
	}
	now := s.clock.Now().UTC()
	session := Session{ID: ids.SessionID(s.ids.New()), UserID: userID, TokenHash: hash, SecurityVersion: securityVersion, AuthenticatedAt: now, LastSeenAt: now, RotatedAt: now, ExpiresAt: now.Add(s.absoluteTTL)}
	if err := s.repository.Create(ctx, session); err != nil {
		return Issued{}, err
	}
	return Issued{Session: session, Token: token}, nil
}

type Authenticated struct {
	Session      Session
	RotatedToken string
}

func (s *Service) Authenticate(ctx context.Context, token string) (Authenticated, error) {
	if token == "" {
		return Authenticated{}, ErrInvalidSession
	}
	now := s.clock.Now().UTC()
	oldHash := sha256.Sum256([]byte(token))
	session, err := s.repository.Use(ctx, oldHash, now, s.idleTTL)
	if err != nil {
		return Authenticated{}, err
	}
	result := Authenticated{Session: session}
	if now.Sub(session.RotatedAt) < s.rotationInterval {
		return result, nil
	}
	rotatedToken, newHash, err := newToken()
	if err != nil {
		return Authenticated{}, err
	}
	rotated, err := s.repository.Rotate(ctx, session.ID, oldHash, newHash, now)
	if err != nil {
		return Authenticated{}, err
	}
	if !rotated {
		return Authenticated{}, ErrInvalidSession
	}
	result.Session.TokenHash = newHash
	result.Session.RotatedAt = now
	result.RotatedToken = rotatedToken
	return result, nil
}

func (s *Service) RevokeAll(ctx context.Context, userID ids.UserID) error {
	if userID == "" {
		return errors.New("user ID is required")
	}
	return s.repository.RevokeAll(ctx, userID, s.clock.Now().UTC())
}

func (s *Service) Revoke(ctx context.Context, sessionID ids.SessionID) error {
	if sessionID == "" {
		return errors.New("session ID is required")
	}
	return s.repository.Revoke(ctx, sessionID, s.clock.Now().UTC())
}

func newToken() (string, [32]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [32]byte{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, sha256.Sum256([]byte(token)), nil
}
