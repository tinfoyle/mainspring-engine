package invitations_test

import (
	"context"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

type generator struct{ n int }

func (g *generator) New() string {
	g.n++
	if g.n == 1 {
		return "00000000-0000-4000-8000-000000000011"
	}
	return "00000000-0000-4000-8000-000000000012"
}

type sender struct{ message invitations.Message }

func (s *sender) SendInvitation(_ context.Context, message invitations.Message) error {
	s.message = message
	return nil
}

type repository struct {
	invitation accounts.Invitation
	state      access.State
}

func (r *repository) AccessState(context.Context, ids.UserID, ids.AccountID) (access.State, error) {
	return r.state, nil
}
func (r *repository) Create(_ context.Context, value accounts.Invitation) error {
	r.invitation = value
	return nil
}
func (r *repository) Delete(context.Context, ids.InvitationID) error { return nil }
func (r *repository) Accept(_ context.Context, userID ids.UserID, hash [32]byte, now time.Time, membershipID ids.MembershipID) (accounts.Membership, error) {
	if r.invitation.TokenHash != hash {
		return accounts.Membership{}, invitations.ErrInvitationNotFound
	}
	return accounts.Membership{ID: membershipID, AccountID: r.invitation.AccountID, UserID: userID, Role: r.invitation.Role, State: accounts.MembershipActive, Version: 1, CreatedAt: now}, nil
}

func TestInvitationAddsMembershipToExistingSystemIdentity(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("00000000-0000-4000-8000-000000000001")
	ownerID := ids.UserID("00000000-0000-4000-8000-000000000002")
	memberID := ids.UserID("00000000-0000-4000-8000-000000000003")
	repository := &repository{state: access.State{Account: accounts.Account{ID: accountID, State: accounts.AccountActive}, Membership: accounts.Membership{AccountID: accountID, UserID: ownerID, Role: accounts.RoleOwner, State: accounts.MembershipActive}, Entitlements: entitlements.Snapshot{AccountID: accountID}}}
	authorizer, err := access.NewAuthorizer(repository)
	if err != nil {
		t.Fatal(err)
	}
	sender := &sender{}
	service, err := invitations.NewService(repository, sender, authorizer, &generator{}, clock{now})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), invitations.CreateCommand{ActorUserID: ownerID, AccountID: accountID, Email: "member@example.com", Role: accounts.RoleMember})
	if err != nil {
		t.Fatal(err)
	}
	if created.InvitationID == "" || sender.message.Token == "" {
		t.Fatal("missing invitation output")
	}
	accepted, err := service.Accept(context.Background(), invitations.AcceptCommand{UserID: memberID, Token: sender.message.Token})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.AccountID != accountID || accepted.UserID != memberID || accepted.Role != accounts.RoleMember {
		t.Fatalf("unexpected membership: %#v", accepted)
	}
}
