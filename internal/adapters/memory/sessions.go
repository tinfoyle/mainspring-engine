package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type SessionStore struct {
	mu     sync.Mutex
	values map[ids.SessionID]sessions.Session
	events map[ids.UserID][]sessions.SecurityEvent
}

func NewSessionStore() *SessionStore {
	return &SessionStore{values: map[ids.SessionID]sessions.Session{}, events: map[ids.UserID][]sessions.SecurityEvent{}}
}

func (s *SessionStore) Create(_ context.Context, session sessions.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[session.ID] = session
	s.appendEventLocked(session.UserID, sessions.SecurityEvent{Type: sessions.EventSessionCreated, SessionID: session.ID, OccurredAt: session.AuthenticatedAt})
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
	revokedAny := false
	for id, value := range s.values {
		if value.UserID == userID && value.RevokedAt == nil {
			revoked := now.UTC()
			value.RevokedAt = &revoked
			s.values[id] = value
			revokedAny = true
		}
	}
	if revokedAny {
		s.appendEventLocked(userID, sessions.SecurityEvent{Type: sessions.EventSessionsRevoked, OccurredAt: now.UTC()})
	}
	return nil
}

func (s *SessionStore) Revoke(_ context.Context, sessionID ids.SessionID, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[sessionID]
	if !ok || value.RevokedAt != nil {
		return nil
	}
	revoked := now.UTC()
	value.RevokedAt = &revoked
	s.values[sessionID] = value
	s.appendEventLocked(value.UserID, sessions.SecurityEvent{Type: sessions.EventSessionRevoked, SessionID: sessionID, OccurredAt: now.UTC()})
	return nil
}

func (s *SessionStore) RevokeOwned(_ context.Context, userID ids.UserID, sessionID ids.SessionID, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[sessionID]
	if !ok || value.UserID != userID || value.RevokedAt != nil {
		return false, nil
	}
	revoked := now.UTC()
	value.RevokedAt = &revoked
	s.values[sessionID] = value
	s.appendEventLocked(userID, sessions.SecurityEvent{Type: sessions.EventSessionRevoked, SessionID: sessionID, OccurredAt: now.UTC()})
	return true, nil
}

func (s *SessionStore) Active(_ context.Context, userID ids.UserID, now time.Time, idleTTL time.Duration) ([]sessions.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]sessions.Session, 0)
	for _, value := range s.values {
		if value.UserID == userID && value.RevokedAt == nil && value.ExpiresAt.After(now) && value.LastSeenAt.Add(idleTTL).After(now) {
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LastSeenAt.After(result[j].LastSeenAt) })
	return result, nil
}

func (s *SessionStore) MarkReauthenticated(_ context.Context, userID ids.UserID, sessionID ids.SessionID, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[sessionID]
	if !ok || value.UserID != userID || value.RevokedAt != nil || !value.ExpiresAt.After(now) {
		return false, nil
	}
	value.ReauthenticatedAt = now.UTC()
	value.LastSeenAt = now.UTC()
	s.values[sessionID] = value
	s.appendEventLocked(userID, sessions.SecurityEvent{Type: sessions.EventSessionReauthenticated, SessionID: sessionID, OccurredAt: now.UTC()})
	return true, nil
}

func (s *SessionStore) SecurityEvents(_ context.Context, userID ids.UserID, limit int) ([]sessions.SecurityEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := append([]sessions.SecurityEvent(nil), s.events[userID]...)
	sort.SliceStable(values, func(i, j int) bool { return values[i].OccurredAt.After(values[j].OccurredAt) })
	if len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}

func (s *SessionStore) appendEventLocked(userID ids.UserID, event sessions.SecurityEvent) {
	s.events[userID] = append(s.events[userID], event)
}

func (s *SessionStore) RecordSecurityEvent(userID ids.UserID, event sessions.SecurityEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendEventLocked(userID, event)
}

var _ sessions.Repository = (*SessionStore)(nil)
