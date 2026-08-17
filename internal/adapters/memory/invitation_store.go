package memory

import (
	"context"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (s *Store) Create(_ context.Context, value accounts.Invitation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, membership := range s.memberships {
		if membership.AccountID != value.AccountID || membership.State != accounts.MembershipActive {
			continue
		}
		if user, ok := s.users[membership.UserID]; ok && user.PrimaryEmail == value.Email {
			return invitations.ErrMembershipExists
		}
	}
	for id, current := range s.invitations {
		if current.AccountID == value.AccountID && current.Email == value.Email && current.State == accounts.InvitationPending {
			revoked := value.CreatedAt
			current.State = accounts.InvitationRevoked
			current.RevokedAt = &revoked
			s.invitations[id] = current
		}
	}
	s.invitations[value.ID] = value
	return nil
}
func (s *Store) Delete(_ context.Context, id ids.InvitationID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.invitations, id)
	return nil
}
func (s *Store) Accept(_ context.Context, userID ids.UserID, hash [32]byte, now time.Time, membershipID ids.MembershipID) (accounts.Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var invitation accounts.Invitation
	found := false
	for _, candidate := range s.invitations {
		if subtleHashEqual(candidate.TokenHash, hash) {
			invitation = candidate
			found = true
			break
		}
	}
	if !found {
		return accounts.Membership{}, invitations.ErrInvitationNotFound
	}
	if invitation.State != accounts.InvitationPending {
		return accounts.Membership{}, invitations.ErrInvitationConsumed
	}
	if !invitation.ExpiresAt.After(now) {
		return accounts.Membership{}, invitations.ErrInvitationExpired
	}
	user, ok := s.users[userID]
	if !ok || user.PrimaryEmail != invitation.Email {
		return accounts.Membership{}, invitations.ErrInvitationEmailMismatch
	}
	for _, current := range s.memberships {
		if current.AccountID == invitation.AccountID && current.UserID == userID && current.State == accounts.MembershipActive {
			return accounts.Membership{}, invitations.ErrMembershipExists
		}
	}
	membership := accounts.Membership{ID: membershipID, AccountID: invitation.AccountID, UserID: userID, Role: invitation.Role, State: accounts.MembershipActive, Version: 1, CreatedAt: now.UTC()}
	s.memberships[membership.ID] = membership
	accepted := now.UTC()
	invitation.State = accounts.InvitationAccepted
	invitation.AcceptedAt = &accepted
	s.invitations[invitation.ID] = invitation
	return membership, nil
}

var _ invitations.Repository = (*Store)(nil)
