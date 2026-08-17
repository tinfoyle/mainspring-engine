package memory

import (
	"context"
	"sync"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type SessionStore struct {
	mu     sync.Mutex
	values map[ids.SessionID]sessions.Session
}

func NewSessionStore() *SessionStore {
	return &SessionStore{values: map[ids.SessionID]sessions.Session{}}
}

func (s *SessionStore) Create(_ context.Context, session sessions.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[session.ID] = session
	return nil
}

func (s *SessionStore) Use(_ context.Context, hash [32]byte, now time.Time, idleTTL time.Duration) (sessions.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, value := range s.values {
		if value.TokenHash == hash {
			if value.RevokedAt != nil || !value.ExpiresAt.After(now) || !value.LastSeenAt.Add(idleTTL).After(now) {
				return sessions.Session{}, sessions.ErrExpiredSession
			}
			value.LastSeenAt = now.UTC()
			s.values[id] = value
			return value, nil
		}
	}
	return sessions.Session{}, sessions.ErrInvalidSession
}

func (s *SessionStore) Rotate(_ context.Context, id ids.SessionID, oldHash, newHash [32]byte, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[id]
	if !ok || value.TokenHash != oldHash || value.RevokedAt != nil {
		return false, nil
	}
	value.TokenHash, value.RotatedAt = newHash, now.UTC()
	s.values[id] = value
	return true, nil
}

func (s *SessionStore) RevokeAll(_ context.Context, userID ids.UserID, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, value := range s.values {
		if value.UserID == userID && value.RevokedAt == nil {
			revoked := now.UTC()
			value.RevokedAt = &revoked
			s.values[id] = value
		}
	}
	return nil
}

var _ sessions.Repository = (*SessionStore)(nil)
