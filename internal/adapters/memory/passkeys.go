package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type PasskeyRepository struct {
	store       *Store
	sessions    *SessionStore
	mu          sync.Mutex
	handles     map[ids.UserID][]byte
	ceremonies  map[string]passkeys.Ceremony
	credentials map[ids.UserID][]passkeys.CredentialRecord
}

func NewPasskeyRepository(store *Store, sessionStore *SessionStore) *PasskeyRepository {
	return &PasskeyRepository{
		store:       store,
		sessions:    sessionStore,
		handles:     map[ids.UserID][]byte{},
		ceremonies:  map[string]passkeys.Ceremony{},
		credentials: map[ids.UserID][]passkeys.CredentialRecord{},
	}
}

func (r *PasskeyRepository) EnsureUser(_ context.Context, userID ids.UserID, handle []byte, _ time.Time) (passkeys.User, error) {
	user, ok := r.identity(userID)
	if !ok || user.State != identity.UserActive {
		return passkeys.User{}, passkeys.ErrCredentialNotFound
	}
	r.mu.Lock()
	if len(r.handles[userID]) == 0 {
		r.handles[userID] = append([]byte(nil), handle...)
	}
	result := r.userLocked(user)
	r.mu.Unlock()
	return result, nil
}

func (r *PasskeyRepository) User(_ context.Context, userID ids.UserID) (passkeys.User, error) {
	user, ok := r.identity(userID)
	if !ok || user.State != identity.UserActive {
		return passkeys.User{}, passkeys.ErrCredentialNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.handles[userID]) == 0 {
		return passkeys.User{}, passkeys.ErrCredentialNotFound
	}
	return r.userLocked(user), nil
}

func (r *PasskeyRepository) UserByHandle(_ context.Context, handle, credentialID []byte) (passkeys.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for userID, current := range r.handles {
		if !bytes.Equal(current, handle) || !hasCredential(r.credentials[userID], credentialID) {
			continue
		}
		user, ok := r.identity(userID)
		if !ok || user.State != identity.UserActive {
			break
		}
		return r.userLocked(user), nil
	}
	return passkeys.User{}, passkeys.ErrCredentialNotFound
}

func (r *PasskeyRepository) CreateCeremony(_ context.Context, ceremony passkeys.Ceremony) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.ceremonies[ceremony.ID]; exists {
		return passkeys.ErrInvalidCeremony
	}
	r.ceremonies[ceremony.ID] = ceremony
	return nil
}

func (r *PasskeyRepository) ConsumeCeremony(_ context.Context, id string, kind passkeys.CeremonyKind, userID ids.UserID, sessionID ids.SessionID, now time.Time) (passkeys.Ceremony, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.ceremonies[id]
	if !ok || value.Kind != kind || value.UserID != userID || value.SessionID != sessionID || !value.ExpiresAt.After(now) {
		return passkeys.Ceremony{}, passkeys.ErrInvalidCeremony
	}
	delete(r.ceremonies, id)
	return value, nil
}

func (r *PasskeyRepository) CreateCredential(_ context.Context, userID ids.UserID, name string, credential webauthn.Credential, now time.Time, maximum int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.handles[userID]) == 0 {
		return passkeys.ErrCredentialNotFound
	}
	values := r.credentials[userID]
	if len(values) >= maximum {
		return passkeys.ErrCredentialLimit
	}
	if hasCredential(values, credential.ID) {
		return passkeys.ErrInvalidCredential
	}
	r.credentials[userID] = append(values, passkeys.CredentialRecord{UserID: userID, Name: name, Credential: cloneCredential(credential), CreatedAt: now.UTC()})
	r.recordEvent(userID, sessions.EventPasskeyAdded, now)
	return nil
}

func (r *PasskeyRepository) UpdateCredential(_ context.Context, userID ids.UserID, credentialID []byte, expected uint32, credential webauthn.Credential, event passkeys.CredentialEvent, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := r.credentials[userID]
	for index := range values {
		if !bytes.Equal(values[index].Credential.ID, credentialID) || values[index].Credential.Authenticator.SignCount != expected {
			continue
		}
		usedAt := now.UTC()
		values[index].Credential = cloneCredential(credential)
		values[index].LastUsedAt = &usedAt
		r.credentials[userID] = values
		r.recordEvent(userID, sessions.SecurityEventType(event), now)
		return true, nil
	}
	return false, nil
}

func (r *PasskeyRepository) RecordCloneWarning(_ context.Context, userID ids.UserID, credentialID []byte, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if hasCredential(r.credentials[userID], credentialID) {
		r.recordEvent(userID, sessions.EventPasskeyCloneWarning, now)
	}
	return nil
}

func (r *PasskeyRepository) ListCredentials(_ context.Context, userID ids.UserID) ([]passkeys.CredentialRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneRecords(r.credentials[userID]), nil
}

func (r *PasskeyRepository) DeleteCredential(_ context.Context, userID ids.UserID, credentialID []byte, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := r.credentials[userID]
	for index := range values {
		if !bytes.Equal(values[index].Credential.ID, credentialID) {
			continue
		}
		r.credentials[userID] = append(values[:index:index], values[index+1:]...)
		r.recordEvent(userID, sessions.EventPasskeyRemoved, now)
		return true, nil
	}
	return false, nil
}

func (r *PasskeyRepository) identity(userID ids.UserID) (identity.User, bool) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	value, ok := r.store.users[userID]
	return value, ok
}

func (r *PasskeyRepository) userLocked(user identity.User) passkeys.User {
	return passkeys.User{Identity: user, Handle: append([]byte(nil), r.handles[user.ID]...), Credentials: cloneRecords(r.credentials[user.ID])}
}

func (r *PasskeyRepository) recordEvent(userID ids.UserID, eventType sessions.SecurityEventType, now time.Time) {
	if r.sessions != nil {
		r.sessions.RecordSecurityEvent(userID, sessions.SecurityEvent{Type: eventType, OccurredAt: now.UTC()})
	}
}

func hasCredential(values []passkeys.CredentialRecord, credentialID []byte) bool {
	for _, value := range values {
		if bytes.Equal(value.Credential.ID, credentialID) {
			return true
		}
	}
	return false
}

func cloneRecords(values []passkeys.CredentialRecord) []passkeys.CredentialRecord {
	result := make([]passkeys.CredentialRecord, 0, len(values))
	for _, value := range values {
		value.Credential = cloneCredential(value.Credential)
		result = append(result, value)
	}
	return result
}

func cloneCredential(value webauthn.Credential) webauthn.Credential {
	raw, _ := json.Marshal(value)
	var result webauthn.Credential
	_ = json.Unmarshal(raw, &result)
	return result
}

var _ passkeys.Repository = (*PasskeyRepository)(nil)
