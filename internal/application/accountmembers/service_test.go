package accountmembers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	memberTestUser    ids.UserID       = "11111111-1111-4111-8111-111111111111"
	memberTestAccount ids.AccountID    = "22222222-2222-4222-8222-222222222222"
	memberTestTarget  ids.MembershipID = "33333333-3333-4333-8333-333333333333"
)

type memberClock struct{ now time.Time }

func (c memberClock) Now() time.Time { return c.now }

type memberIDs struct{}

func (memberIDs) New() string { return "44444444-4444-4444-8444-444444444444" }

type memberState struct{ role accounts.MembershipRole }

func (s memberState) AccessState(context.Context, ids.UserID, ids.AccountID) (access.State, error) {
	return access.State{Account: accounts.Account{ID: memberTestAccount, State: accounts.AccountActive}, Membership: accounts.Membership{AccountID: memberTestAccount, UserID: memberTestUser, Role: s.role, State: accounts.MembershipActive}, Entitlements: entitlements.Snapshot{AccountID: memberTestAccount}}, nil
}

type memberRepository struct {
	changeCalls, removeCalls, transferCalls, stateCalls int
	change                                              ChangeRoleMutation
	remove                                              RemoveMutation
	transfer                                            TransferMutation
	state                                               StateMutation
}

func (*memberRepository) List(context.Context, ids.AccountID) ([]Member, error) { return nil, nil }
func (*memberRepository) Current(context.Context, ids.AccountID, ids.UserID) (Member, error) {
	return Member{MembershipID: memberTestTarget, UserID: memberTestUser, Role: accounts.RoleMember, State: accounts.MembershipActive, Version: 1}, nil
}
func (r *memberRepository) ChangeRole(_ context.Context, value ChangeRoleMutation) (Member, error) {
	r.changeCalls++
	r.change = value
	return Member{MembershipID: value.TargetMembershipID, Role: value.Role, Version: value.ExpectedVersion + 1}, nil
}
func (r *memberRepository) Remove(_ context.Context, value RemoveMutation) error {
	r.removeCalls++
	r.remove = value
	return nil
}
func (r *memberRepository) ChangeState(_ context.Context, value StateMutation) (Member, error) {
	r.stateCalls++
	r.state = value
	state := accounts.MembershipSuspended
	if value.Action == StateActionReactivate {
		state = accounts.MembershipActive
	} else if value.Action == StateActionLeave {
		state = accounts.MembershipRemoved
	}
	return Member{MembershipID: value.TargetMembershipID, State: state, Version: value.ExpectedVersion + 1}, nil
}
func (r *memberRepository) TransferOwnership(_ context.Context, value TransferMutation) (TransferResult, error) {
	r.transferCalls++
	r.transfer = value
	return TransferResult{NewOwner: Member{MembershipID: value.TargetMembershipID, Role: accounts.RoleOwner}}, nil
}

func TestMembershipLifecycleRequiresStrongEvidenceAndCarriesStatePolicy(t *testing.T) {
	now := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	repository := &memberRepository{}
	owner, _ := access.NewAuthorizer(memberState{role: accounts.RoleOwner})
	service, _ := NewService(repository, owner, memberIDs{}, memberClock{now})
	strong := sessions.Session{UserID: memberTestUser, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey}

	suspended, err := service.Suspend(context.Background(), StateCommand{ActorUserID: memberTestUser, Session: strong, AccountID: memberTestAccount, TargetMembershipID: memberTestTarget, ExpectedVersion: 2, Reason: " Temporary access hold "})
	if err != nil || suspended.State != accounts.MembershipSuspended || repository.state.Action != StateActionSuspend || repository.state.Reason != "Temporary access hold" || repository.state.ExpectedActorRole != accounts.RoleOwner {
		t.Fatalf("suspended=%+v mutation=%+v err=%v", suspended, repository.state, err)
	}
	if _, err := service.Reactivate(context.Background(), StateCommand{ActorUserID: memberTestUser, Session: sessions.Session{UserID: memberTestUser, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPassword}, AccountID: memberTestAccount, TargetMembershipID: memberTestTarget, ExpectedVersion: 3, Reason: "Restore access"}); !errors.Is(err, strongauth.ErrRequired) {
		t.Fatalf("weak reactivation error=%v", err)
	}

	member, _ := access.NewAuthorizer(memberState{role: accounts.RoleMember})
	service, _ = NewService(repository, member, memberIDs{}, memberClock{now})
	if err := service.Leave(context.Background(), LeaveCommand{ActorUserID: memberTestUser, Session: strong, AccountID: memberTestAccount, ExpectedVersion: 4, Reason: " Voluntary departure "}); err != nil || repository.state.Action != StateActionLeave || repository.state.TargetMembershipID != "" || repository.state.Reason != "Voluntary departure" {
		t.Fatalf("leave mutation=%+v err=%v", repository.state, err)
	}
	if repository.stateCalls != 2 {
		t.Fatalf("state calls=%d", repository.stateCalls)
	}
}

func TestOwnerMutationsCarryStrongActorBoundEvidenceAndAuditInputs(t *testing.T) {
	now := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	repository := &memberRepository{}
	authorizer, _ := access.NewAuthorizer(memberState{role: accounts.RoleOwner})
	service, _ := NewService(repository, authorizer, memberIDs{}, memberClock{now})
	session := sessions.Session{UserID: memberTestUser, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey}

	changed, err := service.ChangeRole(context.Background(), ChangeRoleCommand{ActorUserID: memberTestUser, Session: session, AccountID: memberTestAccount, TargetMembershipID: memberTestTarget, ExpectedVersion: 2, Role: accounts.RoleAdministrator, Reason: "  Expanded operational responsibility  "})
	if err != nil || changed.Version != 3 || repository.change.Reason != "Expanded operational responsibility" || repository.change.ExpectedActorRole != accounts.RoleOwner {
		t.Fatalf("change=%+v mutation=%+v err=%v", changed, repository.change, err)
	}
	if err := service.Remove(context.Background(), RemoveCommand{ActorUserID: memberTestUser, Session: session, AccountID: memberTestAccount, TargetMembershipID: memberTestTarget, ExpectedVersion: 3, Reason: "Access no longer required"}); err != nil {
		t.Fatal(err)
	}
	transferred, err := service.TransferOwnership(context.Background(), TransferOwnershipCommand{ActorUserID: memberTestUser, Session: session, AccountID: memberTestAccount, TargetMembershipID: memberTestTarget, ExpectedActorVersion: 1, ExpectedTargetVersion: 3, Reason: "Leadership transition"})
	if err != nil || transferred.NewOwner.Role != accounts.RoleOwner || repository.changeCalls != 1 || repository.removeCalls != 1 || repository.transferCalls != 1 {
		t.Fatalf("transfer=%+v repository=%+v err=%v", transferred, repository, err)
	}
}

func TestMembershipMutationsRejectWeakStaleAndCrossUserEvidence(t *testing.T) {
	now := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	repository := &memberRepository{}
	authorizer, _ := access.NewAuthorizer(memberState{role: accounts.RoleOwner})
	service, _ := NewService(repository, authorizer, memberIDs{}, memberClock{now})
	cases := map[string]sessions.Session{
		"password":   {UserID: memberTestUser, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPassword},
		"stale":      {UserID: memberTestUser, ReauthenticatedAt: now.Add(-strongauth.MaximumAge - time.Second), ReauthenticationMethod: sessions.AuthenticationMethodPasskey},
		"cross-user": {UserID: ids.UserID("55555555-5555-4555-8555-555555555555"), ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey},
	}
	for name, session := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := service.ChangeRole(context.Background(), ChangeRoleCommand{ActorUserID: memberTestUser, Session: session, AccountID: memberTestAccount, TargetMembershipID: memberTestTarget, ExpectedVersion: 1, Role: accounts.RoleViewer, Reason: "Reduce access"})
			if !errors.Is(err, strongauth.ErrRequired) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if repository.changeCalls != 0 {
		t.Fatal("weak evidence reached the Membership repository")
	}
}

func TestMembershipRoleAndInputPolicy(t *testing.T) {
	now := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	session := sessions.Session{UserID: memberTestUser, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey}
	repository := &memberRepository{}
	administrator, _ := access.NewAuthorizer(memberState{role: accounts.RoleAdministrator})
	service, _ := NewService(repository, administrator, memberIDs{}, memberClock{now})
	if _, err := service.ChangeRole(context.Background(), ChangeRoleCommand{ActorUserID: memberTestUser, Session: session, AccountID: memberTestAccount, TargetMembershipID: memberTestTarget, ExpectedVersion: 1, Role: accounts.RoleViewer, Reason: "Reduce access"}); !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("administrator role change error = %v", err)
	}
	if err := service.Remove(context.Background(), RemoveCommand{ActorUserID: memberTestUser, Session: session, AccountID: memberTestAccount, TargetMembershipID: memberTestTarget, ExpectedVersion: 1, Reason: "Remove access"}); err != nil {
		t.Fatalf("administrator removal: %v", err)
	}

	owner, _ := access.NewAuthorizer(memberState{role: accounts.RoleOwner})
	service, _ = NewService(repository, owner, memberIDs{}, memberClock{now})
	if _, err := service.ChangeRole(context.Background(), ChangeRoleCommand{ActorUserID: memberTestUser, Session: session, AccountID: memberTestAccount, TargetMembershipID: memberTestTarget, ExpectedVersion: 1, Role: accounts.RoleOwner, Reason: "Promote owner"}); !errors.Is(err, ErrRoleInvalid) {
		t.Fatalf("direct owner assignment error = %v", err)
	}
	if err := service.Remove(context.Background(), RemoveCommand{ActorUserID: memberTestUser, Session: session, AccountID: memberTestAccount, TargetMembershipID: memberTestTarget, ExpectedVersion: 1, Reason: "x"}); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("short reason error = %v", err)
	}
}
