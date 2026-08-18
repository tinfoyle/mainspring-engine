package memory

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type RecoveryRepository struct {
	mu         sync.Mutex
	store      *Store
	sessions   *SessionStore
	challenges map[ids.RecoveryID]memoryRecoveryChallenge
}

type memoryRecoveryChallenge struct {
	pending    recovery.Pending
	userID     ids.UserID
	consumedAt *time.Time
}

func NewRecoveryRepository(store *Store, sessions *SessionStore) *RecoveryRepository {
	return &RecoveryRepository{store: store, sessions: sessions, challenges: make(map[ids.RecoveryID]memoryRecoveryChallenge)}
}

func (r *RecoveryRepository) Create(_ context.Context, pending recovery.Pending) (recovery.Recipient, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	userID, exists := r.store.usersByEmail[pending.Email]
	user := r.store.users[userID]
	_, hasCredential := r.store.credentials[userID]
	if !exists || !hasCredential || user.State != identity.UserActive {
		return recovery.Recipient{}, false, nil
	}
	for id, challenge := range r.challenges {
		if challenge.userID == userID && challenge.consumedAt == nil {
			consumed := pending.CreatedAt.UTC()
			challenge.consumedAt = &consumed
			r.challenges[id] = challenge
		}
	}
	r.challenges[pending.ID] = memoryRecoveryChallenge{pending: pending, userID: userID}
	return recovery.Recipient{UserID: userID, Email: user.PrimaryEmail, DisplayName: user.DisplayName}, true, nil
}

func (r *RecoveryRepository) Delete(_ context.Context, id ids.RecoveryID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.challenges, id)
	return nil
}

func (r *RecoveryRepository) Complete(_ context.Context, tokenHash [32]byte, passwordHash string, now time.Time) (ids.UserID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var id ids.RecoveryID
	var challenge memoryRecoveryChallenge
	for candidateID, candidate := range r.challenges {
		if subtleHashEqual(candidate.pending.TokenHash, tokenHash) {
			id, challenge = candidateID, candidate
			break
		}
	}
	if id == "" || challenge.consumedAt != nil || !challenge.pending.ExpiresAt.After(now) {
		return "", recovery.ErrInvalidChallenge
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	user, exists := r.store.users[challenge.userID]
	credential, hasCredential := r.store.credentials[challenge.userID]
	if !exists || !hasCredential || user.State != identity.UserActive {
		return "", recovery.ErrInvalidChallenge
	}
	r.sessions.mu.Lock()
	defer r.sessions.mu.Unlock()
	credential.PasswordHash = passwordHash
	credential.UpdatedAt = now.UTC()
	r.store.credentials[challenge.userID] = credential
	user.SecurityVersion++
	r.store.users[challenge.userID] = user
	delete(r.store.authAttempts, sha256.Sum256([]byte(user.PrimaryEmail)))
	delete(r.store.authAttempts, sha256.Sum256([]byte("reauth:"+string(user.ID))))
	for sessionID, session := range r.sessions.values {
		if session.UserID == challenge.userID && session.RevokedAt == nil {
			revoked := now.UTC()
			session.RevokedAt = &revoked
			r.sessions.values[sessionID] = session
		}
	}
	r.sessions.appendEventLocked(challenge.userID, sessions.SecurityEvent{Type: sessions.EventCredentialRecovered, OccurredAt: now.UTC()})
	for candidateID, candidate := range r.challenges {
		if candidate.userID == challenge.userID && candidate.consumedAt == nil {
			consumed := now.UTC()
			candidate.consumedAt = &consumed
			r.challenges[candidateID] = candidate
		}
	}
	return challenge.userID, nil
}

var _ recovery.Repository = (*RecoveryRepository)(nil)

type RecoverySink struct {
	mu       sync.Mutex
	messages []recovery.Message
}

func (s *RecoverySink) SendRecovery(_ context.Context, message recovery.Message) error {
	if message.Suppress {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	return nil
}

func (s *RecoverySink) Latest() (recovery.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return recovery.Message{}, false
	}
	return s.messages[len(s.messages)-1], true
}

var _ recovery.Sender = (*RecoverySink)(nil)
