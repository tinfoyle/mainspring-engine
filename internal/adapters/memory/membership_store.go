package memory

import (
	"context"
	"sort"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountmembers"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (s *Store) List(_ context.Context, accountID ids.AccountID) ([]accountmembers.Member, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]accountmembers.Member, 0)
	for _, membership := range s.memberships {
		if membership.AccountID != accountID || membership.State != accounts.MembershipActive {
			continue
		}
		user, ok := s.users[membership.UserID]
		if !ok {
			continue
		}
		result = append(result, memberView(membership, user.DisplayName, user.PrimaryEmail))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Role == accounts.RoleOwner {
			return result[j].Role != accounts.RoleOwner
		}
		if result[j].Role == accounts.RoleOwner {
			return false
		}
		if result[i].DisplayName == result[j].DisplayName {
			return result[i].MembershipID < result[j].MembershipID
		}
		return result[i].DisplayName < result[j].DisplayName
	})
	return result, nil
}

func (s *Store) ChangeRole(_ context.Context, mutation accountmembers.ChangeRoleMutation) (accountmembers.Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	actor, ok := s.activeMembershipForUser(mutation.AccountID, mutation.ActorUserID)
	if !ok || actor.Role != mutation.ExpectedActorRole || actor.Role != accounts.RoleOwner {
		return accountmembers.Member{}, accountmembers.ErrTargetDenied
	}
	target, ok := s.memberships[mutation.TargetMembershipID]
	if !ok || target.AccountID != mutation.AccountID || target.State != accounts.MembershipActive {
		return accountmembers.Member{}, accountmembers.ErrMembershipNotFound
	}
	if target.Role == accounts.RoleOwner {
		return accountmembers.Member{}, accountmembers.ErrOwnershipRequired
	}
	if target.Version != mutation.ExpectedVersion {
		return accountmembers.Member{}, accountmembers.ErrVersionConflict
	}
	if target.Role != mutation.Role {
		target.Role = mutation.Role
		target.Version++
		s.memberships[target.ID] = target
	}
	user := s.users[target.UserID]
	return memberView(target, user.DisplayName, user.PrimaryEmail), nil
}

func (s *Store) Remove(_ context.Context, mutation accountmembers.RemoveMutation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	actor, ok := s.activeMembershipForUser(mutation.AccountID, mutation.ActorUserID)
	if !ok || actor.Role != mutation.ExpectedActorRole || (actor.Role != accounts.RoleOwner && actor.Role != accounts.RoleAdministrator) {
		return accountmembers.ErrTargetDenied
	}
	target, ok := s.memberships[mutation.TargetMembershipID]
	if !ok || target.AccountID != mutation.AccountID || target.State != accounts.MembershipActive {
		return accountmembers.ErrMembershipNotFound
	}
	if target.Role == accounts.RoleOwner {
		return accountmembers.ErrOwnershipRequired
	}
	if actor.Role == accounts.RoleAdministrator && target.Role == accounts.RoleAdministrator {
		return accountmembers.ErrTargetDenied
	}
	if target.Version != mutation.ExpectedVersion {
		return accountmembers.ErrVersionConflict
	}
	target.State = accounts.MembershipRemoved
	target.Version++
	s.memberships[target.ID] = target
	return nil
}

func (s *Store) TransferOwnership(_ context.Context, mutation accountmembers.TransferMutation) (accountmembers.TransferResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	actor, ok := s.activeMembershipForUser(mutation.AccountID, mutation.ActorUserID)
	if !ok || actor.Role != accounts.RoleOwner {
		return accountmembers.TransferResult{}, accountmembers.ErrTargetDenied
	}
	target, ok := s.memberships[mutation.TargetMembershipID]
	if !ok || target.AccountID != mutation.AccountID || target.State != accounts.MembershipActive || target.ID == actor.ID {
		return accountmembers.TransferResult{}, accountmembers.ErrMembershipNotFound
	}
	if target.Role == accounts.RoleOwner {
		return accountmembers.TransferResult{}, accountmembers.ErrOwnershipRequired
	}
	if actor.Version != mutation.ExpectedActorVersion || target.Version != mutation.ExpectedTargetVersion {
		return accountmembers.TransferResult{}, accountmembers.ErrVersionConflict
	}
	actor.Role = accounts.RoleAdministrator
	actor.Version++
	target.Role = accounts.RoleOwner
	target.Version++
	s.memberships[actor.ID] = actor
	s.memberships[target.ID] = target
	actorUser, targetUser := s.users[actor.UserID], s.users[target.UserID]
	return accountmembers.TransferResult{PreviousOwner: memberView(actor, actorUser.DisplayName, actorUser.PrimaryEmail), NewOwner: memberView(target, targetUser.DisplayName, targetUser.PrimaryEmail)}, nil
}

func (s *Store) activeMembershipForUser(accountID ids.AccountID, userID ids.UserID) (accounts.Membership, bool) {
	for _, membership := range s.memberships {
		if membership.AccountID == accountID && membership.UserID == userID && membership.State == accounts.MembershipActive {
			return membership, true
		}
	}
	return accounts.Membership{}, false
}

func memberView(value accounts.Membership, displayName, email string) accountmembers.Member {
	return accountmembers.Member{MembershipID: value.ID, UserID: value.UserID, DisplayName: displayName, Email: email, Role: value.Role, State: value.State, Version: value.Version, CreatedAt: value.CreatedAt}
}

var _ accountmembers.Repository = (*Store)(nil)
