package memory

import (
	"context"
	"sync"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/recoverycodes"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type RecoveryCodeRepository struct {
	mu       sync.Mutex
	sessions *SessionStore
	sets     map[ids.UserID]memoryRecoveryCodeSet
	grants   map[ids.SessionID]memoryRecoveryGrant
}

type memoryRecoveryCodeSet struct {
	value recoverycodes.Set
	used  map[recoverycodes.CodeHash]time.Time
}

type memoryRecoveryGrant struct {
	userID    ids.UserID
	expiresAt time.Time
}

func NewRecoveryCodeRepository(sessions *SessionStore) *RecoveryCodeRepository {
	return &RecoveryCodeRepository{sessions: sessions, sets: map[ids.UserID]memoryRecoveryCodeSet{}, grants: map[ids.SessionID]memoryRecoveryGrant{}}
}

func (r *RecoveryCodeRepository) Rotate(_ context.Context, value recoverycodes.Set) (recoverycodes.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.sets[value.UserID]
	value.Version = current.value.Version + 1
	value.Hashes = append([]recoverycodes.CodeHash(nil), value.Hashes...)
	r.sets[value.UserID] = memoryRecoveryCodeSet{value: value, used: map[recoverycodes.CodeHash]time.Time{}}
	for sessionID, grant := range r.grants {
		if grant.userID == value.UserID {
			delete(r.grants, sessionID)
		}
	}
	r.record(value.UserID, sessions.SecurityEvent{Type: sessions.EventRecoveryCodesRotated, OccurredAt: value.CreatedAt.UTC()})
	return recoverycodes.Status{Configured: true, Version: value.Version, Remaining: len(value.Hashes), CreatedAt: value.CreatedAt.UTC()}, nil
}

func (r *RecoveryCodeRepository) Status(_ context.Context, userID ids.UserID) (recoverycodes.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.sets[userID]
	if !exists {
		return recoverycodes.Status{}, nil
	}
	remaining := len(value.value.Hashes) - len(value.used)
	return recoverycodes.Status{Configured: true, Version: value.value.Version, Remaining: remaining, CreatedAt: value.value.CreatedAt.UTC()}, nil
}

func (r *RecoveryCodeRepository) Consume(_ context.Context, userID ids.UserID, sessionID ids.SessionID, hash recoverycodes.CodeHash, now, expiresAt time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.sets[userID]
	if !exists {
		return false, nil
	}
	if _, used := value.used[hash]; used {
		return false, nil
	}
	found := false
	for _, candidate := range value.value.Hashes {
		if candidate == hash {
			found = true
			break
		}
	}
	if !found {
		return false, nil
	}
	value.used[hash] = now.UTC()
	r.sets[userID] = value
	r.grants[sessionID] = memoryRecoveryGrant{userID: userID, expiresAt: expiresAt.UTC()}
	r.record(userID, sessions.SecurityEvent{Type: sessions.EventRecoveryCodeConsumed, SessionID: sessionID, OccurredAt: now.UTC()})
	return true, nil
}

func (r *RecoveryCodeRepository) Granted(_ context.Context, userID ids.UserID, sessionID ids.SessionID, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.grants[sessionID]
	return exists && value.userID == userID && value.expiresAt.After(now), nil
}

func (r *RecoveryCodeRepository) record(userID ids.UserID, event sessions.SecurityEvent) {
	if r.sessions != nil {
		r.sessions.RecordSecurityEvent(userID, event)
	}
}

var _ recoverycodes.Store = (*RecoveryCodeRepository)(nil)
