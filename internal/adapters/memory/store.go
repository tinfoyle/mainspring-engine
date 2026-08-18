package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/authentication"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/placement"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Store struct {
	mu              sync.RWMutex
	pending         map[ids.RegistrationID]registration.Pending
	users           map[ids.UserID]identity.User
	usersByEmail    map[string]ids.UserID
	credentials     map[ids.UserID]identity.LocalCredential
	accounts        map[ids.AccountID]accounts.Account
	memberships     map[ids.MembershipID]accounts.Membership
	assignments     map[ids.AccountID]placement.Assignment
	grants          map[ids.AccountID][]entitlements.Grant
	snapshots       map[ids.AccountID]entitlements.Snapshot
	cells           []placement.Cell
	catalog         catalog.PublishedCatalog
	authAttempts    map[[32]byte]authAttempt
	networkAttempts map[networkAttemptKey]authAttempt
	invitations     map[ids.InvitationID]accounts.Invitation
}

type networkAttemptKey struct {
	Scope abuse.Scope
	Actor [32]byte
}

type authAttempt struct {
	WindowStarted time.Time
	Count         int
	LockedUntil   time.Time
}

func NewStore(publishedCatalog catalog.PublishedCatalog, cells []placement.Cell) *Store {
	return &Store{pending: map[ids.RegistrationID]registration.Pending{}, users: map[ids.UserID]identity.User{}, usersByEmail: map[string]ids.UserID{}, credentials: map[ids.UserID]identity.LocalCredential{}, accounts: map[ids.AccountID]accounts.Account{}, memberships: map[ids.MembershipID]accounts.Membership{}, assignments: map[ids.AccountID]placement.Assignment{}, grants: map[ids.AccountID][]entitlements.Grant{}, snapshots: map[ids.AccountID]entitlements.Snapshot{}, cells: append([]placement.Cell(nil), cells...), catalog: publishedCatalog, authAttempts: map[[32]byte]authAttempt{}, networkAttempts: map[networkAttemptKey]authAttempt{}, invitations: map[ids.InvitationID]accounts.Invitation{}}
}

func (s *Store) CreatePending(_ context.Context, pending registration.Pending) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.usersByEmail[pending.User.PrimaryEmail]; exists {
		return registration.ErrEmailExists
	}
	for _, current := range s.pending {
		if current.User.PrimaryEmail == pending.User.PrimaryEmail && current.ConsumedAt == nil {
			if current.ExpiresAt.After(pending.CreatedAt) {
				return registration.ErrEmailExists
			}
			expiredAt := pending.CreatedAt.UTC()
			current.ConsumedAt = &expiredAt
			s.pending[current.ID] = current
		}
	}
	s.pending[pending.ID] = pending
	return nil
}

func (s *Store) DeletePending(_ context.Context, id ids.RegistrationID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
	return nil
}

func (s *Store) Complete(_ context.Context, tokenHash [32]byte, now time.Time, build func(registration.Pending) (registration.Provisioned, error)) (registration.Provisioned, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var pending registration.Pending
	var found bool
	for _, candidate := range s.pending {
		if subtleHashEqual(candidate.TokenHash, tokenHash) {
			pending, found = candidate, true
			break
		}
	}
	if !found {
		return registration.Provisioned{}, registration.ErrRegistrationNotFound
	}
	if pending.ConsumedAt != nil {
		return registration.Provisioned{}, registration.ErrRegistrationConsumed
	}
	if !pending.ExpiresAt.After(now) {
		return registration.Provisioned{}, registration.ErrRegistrationExpired
	}
	if _, exists := s.usersByEmail[pending.User.PrimaryEmail]; exists {
		return registration.Provisioned{}, registration.ErrEmailExists
	}
	provisioned, err := build(pending)
	if err != nil {
		return registration.Provisioned{}, err
	}
	for _, existing := range s.accounts {
		if existing.Slug == provisioned.Account.Slug {
			return registration.Provisioned{}, errors.New("account slug already exists")
		}
	}
	consumed := now.UTC()
	pending.ConsumedAt = &consumed
	s.pending[pending.ID] = pending
	s.users[provisioned.User.ID] = provisioned.User
	s.usersByEmail[provisioned.User.PrimaryEmail] = provisioned.User.ID
	s.credentials[provisioned.User.ID] = provisioned.Credential
	s.accounts[provisioned.Account.ID] = provisioned.Account
	s.memberships[provisioned.Membership.ID] = provisioned.Membership
	s.assignments[provisioned.Account.ID] = provisioned.Assignment
	s.grants[provisioned.Account.ID] = append([]entitlements.Grant(nil), provisioned.Grants...)
	s.snapshots[provisioned.Account.ID] = provisioned.Snapshot
	for index := range s.cells {
		if s.cells[index].ID == provisioned.Assignment.CellID {
			s.cells[index].AssignedAccounts++
		}
	}
	return provisioned, nil
}

func (s *Store) AvailableCells(_ context.Context) ([]placement.Cell, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]placement.Cell(nil), s.cells...), nil
}

func (s *Store) Catalog() catalog.PublishedCatalog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.catalog
}
func (s *Store) Snapshot(accountID ids.AccountID) (entitlements.Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.snapshots[accountID]
	return value, ok
}

func (s *Store) LocalIdentity(_ context.Context, email string) (authentication.LocalIdentity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	userID, ok := s.usersByEmail[email]
	if !ok {
		return authentication.LocalIdentity{}, authentication.ErrIdentityNotFound
	}
	credential, ok := s.credentials[userID]
	if !ok {
		return authentication.LocalIdentity{}, authentication.ErrIdentityNotFound
	}
	return authentication.LocalIdentity{User: s.users[userID], PasswordHash: credential.PasswordHash}, nil
}

func (s *Store) LocalIdentityForUser(_ context.Context, userID ids.UserID) (authentication.LocalIdentity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[userID]
	if !ok {
		return authentication.LocalIdentity{}, authentication.ErrIdentityNotFound
	}
	credential, ok := s.credentials[userID]
	if !ok {
		return authentication.LocalIdentity{}, authentication.ErrIdentityNotFound
	}
	return authentication.LocalIdentity{User: user, PasswordHash: credential.PasswordHash}, nil
}

func (s *Store) Blocked(_ context.Context, key [32]byte, now time.Time) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.authAttempts[key]
	return ok && value.LockedUntil.After(now), nil
}

func (s *Store) Failure(_ context.Context, key [32]byte, now time.Time, threshold int, lock time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.authAttempts[key]
	if value.WindowStarted.IsZero() || !value.WindowStarted.Add(lock).After(now) {
		value = authAttempt{WindowStarted: now}
	}
	value.Count++
	if value.Count >= threshold {
		value.LockedUntil = now.Add(lock)
	}
	s.authAttempts[key] = value
	return nil
}

func (s *Store) Success(_ context.Context, key [32]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.authAttempts, key)
	return nil
}

func (s *Store) Consume(_ context.Context, scope abuse.Scope, actor [32]byte, now time.Time, policy abuse.Policy) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := networkAttemptKey{Scope: scope, Actor: actor}
	value := s.networkAttempts[key]
	if value.LockedUntil.After(now) {
		return false, nil
	}
	if value.WindowStarted.IsZero() || !value.WindowStarted.Add(policy.Window).After(now) {
		value = authAttempt{WindowStarted: now}
	}
	value.Count++
	allowed := value.Count <= policy.Limit
	if !allowed {
		value.LockedUntil = now.Add(policy.Window)
	}
	s.networkAttempts[key] = value
	return allowed, nil
}

func (s *Store) AccessState(_ context.Context, userID ids.UserID, accountID ids.AccountID) (access.State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	account, ok := s.accounts[accountID]
	if !ok {
		return access.State{}, &access.DeniedError{Code: access.DenialMembership}
	}
	var membership accounts.Membership
	found := false
	for _, candidate := range s.memberships {
		if candidate.AccountID == accountID && candidate.UserID == userID {
			membership = candidate
			found = true
			break
		}
	}
	if !found {
		return access.State{}, &access.DeniedError{Code: access.DenialMembership}
	}
	return access.State{Account: account, Membership: membership, Entitlements: s.snapshots[accountID]}, nil
}

func (s *Store) Choices(_ context.Context, userID ids.UserID) ([]accountaccess.Choice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]accountaccess.Choice, 0)
	for _, membership := range s.memberships {
		if membership.UserID != userID || membership.State != accounts.MembershipActive {
			continue
		}
		account, ok := s.accounts[membership.AccountID]
		if !ok || account.State != accounts.AccountActive {
			continue
		}
		result = append(result, accountaccess.Choice{AccountID: account.ID, Slug: account.Slug, DisplayName: account.DisplayName, AccountType: account.Type, Role: membership.Role, CellID: account.CellID, PlacementGeneration: account.PlacementGeneration, Entitlements: s.snapshots[account.ID]})
	}
	return result, nil
}

var _ authentication.IdentitySource = (*Store)(nil)
var _ authentication.AttemptLimiter = (*Store)(nil)
var _ abuse.Limiter = (*Store)(nil)
var _ access.StateSource = (*Store)(nil)
var _ accountaccess.Repository = (*Store)(nil)

func subtleHashEqual(left, right [32]byte) bool {
	var different byte
	for index := range left {
		different |= left[index] ^ right[index]
	}
	return different == 0
}
