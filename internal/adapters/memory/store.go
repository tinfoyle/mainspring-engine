package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/placement"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Store struct {
	mu           sync.RWMutex
	pending      map[ids.RegistrationID]registration.Pending
	users        map[ids.UserID]identity.User
	usersByEmail map[string]ids.UserID
	accounts     map[ids.AccountID]accounts.Account
	memberships  map[ids.MembershipID]accounts.Membership
	assignments  map[ids.AccountID]placement.Assignment
	grants       map[ids.AccountID][]entitlements.Grant
	snapshots    map[ids.AccountID]entitlements.Snapshot
	cells        []placement.Cell
	catalog      catalog.PublishedCatalog
}

func NewStore(publishedCatalog catalog.PublishedCatalog, cells []placement.Cell) *Store {
	return &Store{pending: map[ids.RegistrationID]registration.Pending{}, users: map[ids.UserID]identity.User{}, usersByEmail: map[string]ids.UserID{}, accounts: map[ids.AccountID]accounts.Account{}, memberships: map[ids.MembershipID]accounts.Membership{}, assignments: map[ids.AccountID]placement.Assignment{}, grants: map[ids.AccountID][]entitlements.Grant{}, snapshots: map[ids.AccountID]entitlements.Snapshot{}, cells: append([]placement.Cell(nil), cells...), catalog: publishedCatalog}
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

func subtleHashEqual(left, right [32]byte) bool {
	var different byte
	for index := range left {
		different |= left[index] ^ right[index]
	}
	return different == 0
}
