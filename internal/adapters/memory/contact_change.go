package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/contactchange"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type ContactChangeRepository struct {
	store    *Store
	sessions *SessionStore
}

func NewContactChangeRepository(store *Store, sessionStore *SessionStore) *ContactChangeRepository {
	return &ContactChangeRepository{store: store, sessions: sessionStore}
}

func (r *ContactChangeRepository) User(_ context.Context, userID ids.UserID) (identity.User, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	user, ok := r.store.users[userID]
	if !ok {
		return identity.User{}, contactchange.ErrInvalidUser
	}
	return user, nil
}

func (r *ContactChangeRepository) CreatePending(_ context.Context, pending contactchange.Pending, notifications []contactchange.PreparedNotification) error {
	if len(notifications) != 2 {
		return errors.New("contact change requires verification and request notifications")
	}
	r.store.mu.Lock()
	user, ok := r.store.users[pending.UserID]
	if !ok || user.State != identity.UserActive || user.PrimaryEmail != pending.OldEmail || user.SecurityVersion != pending.SecurityVersion {
		r.store.mu.Unlock()
		return contactchange.ErrStaleIdentity
	}
	if owner, exists := r.store.usersByEmail[pending.NewEmail]; exists && owner != pending.UserID {
		r.store.mu.Unlock()
		return contactchange.ErrEmailExists
	}
	for id, current := range r.store.contactChanges {
		if current.ConsumedAt != nil {
			continue
		}
		if !current.ExpiresAt.After(pending.CreatedAt) || current.UserID == pending.UserID {
			consumed := pending.CreatedAt.UTC()
			current.ConsumedAt = &consumed
			r.store.contactChanges[id] = current
			continue
		}
		if current.NewEmail == pending.NewEmail {
			r.store.mu.Unlock()
			return contactchange.ErrEmailExists
		}
	}
	r.store.contactChanges[pending.ID] = pending
	r.store.mu.Unlock()
	if r.sessions != nil {
		r.sessions.RecordSecurityEvent(pending.UserID, sessions.SecurityEvent{Type: sessions.EventPrimaryEmailChangeRequested, OccurredAt: pending.CreatedAt.UTC()})
	}
	return nil
}

func (r *ContactChangeRepository) Complete(ctx context.Context, tokenHash [32]byte, now time.Time, prepare func(contactchange.Pending) ([]contactchange.PreparedNotification, error)) (contactchange.Completed, error) {
	r.store.mu.Lock()
	var pending contactchange.Pending
	var found bool
	for _, candidate := range r.store.contactChanges {
		if subtleHashEqual(candidate.TokenHash, tokenHash) {
			pending, found = candidate, true
			break
		}
	}
	if !found {
		r.store.mu.Unlock()
		return contactchange.Completed{}, contactchange.ErrNotFound
	}
	if pending.ConsumedAt != nil {
		r.store.mu.Unlock()
		return contactchange.Completed{}, contactchange.ErrConsumed
	}
	if !pending.ExpiresAt.After(now) {
		r.store.mu.Unlock()
		return contactchange.Completed{}, contactchange.ErrExpired
	}
	user, ok := r.store.users[pending.UserID]
	if !ok || user.State != identity.UserActive || user.PrimaryEmail != pending.OldEmail || user.SecurityVersion != pending.SecurityVersion {
		r.store.mu.Unlock()
		return contactchange.Completed{}, contactchange.ErrStaleIdentity
	}
	if owner, exists := r.store.usersByEmail[pending.NewEmail]; exists && owner != pending.UserID {
		r.store.mu.Unlock()
		return contactchange.Completed{}, contactchange.ErrEmailExists
	}
	notifications, err := prepare(pending)
	if err != nil {
		r.store.mu.Unlock()
		return contactchange.Completed{}, err
	}
	if len(notifications) != 2 {
		r.store.mu.Unlock()
		return contactchange.Completed{}, errors.New("contact change completion requires two notifications")
	}
	delete(r.store.usersByEmail, user.PrimaryEmail)
	user.PrimaryEmail = pending.NewEmail
	verified := now.UTC()
	user.EmailVerifiedAt = &verified
	user.SecurityVersion++
	r.store.users[user.ID] = user
	r.store.usersByEmail[user.PrimaryEmail] = user.ID
	consumed := now.UTC()
	pending.ConsumedAt = &consumed
	r.store.contactChanges[pending.ID] = pending
	r.store.mu.Unlock()

	if r.sessions != nil {
		_ = r.sessions.RevokeAll(ctx, user.ID, now)
		r.sessions.RecordSecurityEvent(user.ID, sessions.SecurityEvent{Type: sessions.EventPrimaryEmailChanged, OccurredAt: now.UTC()})
	}
	return contactchange.Completed{UserID: user.ID, OldEmail: pending.OldEmail, NewEmail: pending.NewEmail, SecurityVersion: user.SecurityVersion, ChangedAt: now.UTC()}, nil
}

var _ contactchange.Repository = (*ContactChangeRepository)(nil)

type ContactChangeSink struct {
	mu       sync.Mutex
	messages []contactchange.Message
}

func (s *ContactChangeSink) PrepareContactChange(id string, message contactchange.Message) (contactchange.PreparedNotification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	return contactchange.PreparedNotification{ID: id, Ciphertext: []byte("development"), Nonce: []byte("development"), KeyVersion: 1, CreatedAt: time.Now().UTC()}, nil
}

func (s *ContactChangeSink) LatestVerification(userID ids.UserID) (contactchange.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := len(s.messages) - 1; index >= 0; index-- {
		message := s.messages[index]
		if message.UserID == userID && message.Action == contactchange.ActionVerifyNew {
			return message, true
		}
	}
	return contactchange.Message{}, false
}

var _ contactchange.NotificationPreparer = (*ContactChangeSink)(nil)
