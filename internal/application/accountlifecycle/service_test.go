package accountlifecycle

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
	lifecycleUser    ids.UserID    = "11111111-1111-4111-8111-111111111111"
	lifecycleAccount ids.AccountID = "22222222-2222-4222-8222-222222222222"
)

type lifecycleClock struct{ now time.Time }

func (c lifecycleClock) Now() time.Time { return c.now }

type lifecycleIDs struct{ next int }

func (g *lifecycleIDs) New() string {
	g.next++
	if g.next == 1 {
		return "33333333-3333-4333-8333-333333333333"
	}
	return "44444444-4444-4444-8444-444444444444"
}

type lifecycleAccess struct{ state accounts.AccountState }

func (s lifecycleAccess) AccessState(context.Context, ids.UserID, ids.AccountID) (access.State, error) {
	return access.State{Account: accounts.Account{ID: lifecycleAccount, State: s.state, EntitlementVersion: 1}, Membership: accounts.Membership{AccountID: lifecycleAccount, UserID: lifecycleUser, Role: accounts.RoleOwner, State: accounts.MembershipActive}, Entitlements: entitlements.Snapshot{AccountID: lifecycleAccount, Version: 1}}, nil
}

type lifecycleRepository struct {
	requested RequestMutation
	canceled  CancelMutation
	work      Work
	claim     bool
	evaluated bool
}

func (r *lifecycleRepository) Request(_ context.Context, mutation RequestMutation) (Status, error) {
	r.requested = mutation
	return Status{RequestID: mutation.RequestID, AccountID: mutation.AccountID, AccountVersion: mutation.ExpectedAccountVersion + 1, State: StateCoolingOff}, nil
}
func (r *lifecycleRepository) Cancel(_ context.Context, mutation CancelMutation) (Status, error) {
	r.canceled = mutation
	return Status{AccountID: mutation.AccountID, AccountVersion: mutation.ExpectedAccountVersion + 1, State: StateCanceled}, nil
}
func (*lifecycleRepository) ListOwned(context.Context, ids.UserID) ([]Status, error) { return nil, nil }
func (r *lifecycleRepository) Claim(context.Context, time.Time, time.Duration) (Work, bool, error) {
	return r.work, r.claim, nil
}
func (r *lifecycleRepository) Evaluate(_ context.Context, work Work, _ string, _ time.Time, _, _ time.Duration) (Status, error) {
	r.evaluated = true
	return Status{RequestID: work.RequestID, State: StateClosed}, nil
}

func TestOwnerClosureRequiresPasskeyAndCarriesCoolingOffPolicy(t *testing.T) {
	now := time.Date(2026, 8, 18, 16, 0, 0, 0, time.UTC)
	repository := &lifecycleRepository{}
	authorizer, _ := access.NewAuthorizer(lifecycleAccess{state: accounts.AccountActive})
	generator := &lifecycleIDs{}
	service, err := NewService(repository, authorizer, generator, lifecycleClock{now}, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	strong := sessions.Session{UserID: lifecycleUser, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey}
	status, err := service.Request(context.Background(), RequestCommand{ActorUserID: lifecycleUser, Session: strong, AccountID: lifecycleAccount, ExpectedAccountVersion: 4, Reason: "  Business no longer operates  "})
	if err != nil || status.State != StateCoolingOff {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if repository.requested.Reason != "Business no longer operates" || repository.requested.ExecuteAfter != now.Add(7*24*time.Hour) || repository.requested.ExpectedAccountVersion != 4 {
		t.Fatalf("mutation=%+v", repository.requested)
	}

	weak := sessions.Session{UserID: lifecycleUser, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPassword}
	if _, err := service.Request(context.Background(), RequestCommand{ActorUserID: lifecycleUser, Session: weak, AccountID: lifecycleAccount, ExpectedAccountVersion: 4, Reason: "Close business"}); !errors.Is(err, strongauth.ErrRequired) {
		t.Fatalf("weak request error=%v", err)
	}
}

func TestCancellationUsesRecoveryPathAndStillRequiresPasskey(t *testing.T) {
	now := time.Date(2026, 8, 18, 16, 0, 0, 0, time.UTC)
	repository := &lifecycleRepository{}
	// The ordinary authorizer would reject this closing Account. Cancel must
	// reach the repository, where owner status and version are rechecked in
	// the same transaction as restoration.
	authorizer, _ := access.NewAuthorizer(lifecycleAccess{state: accounts.AccountClosing})
	service, _ := NewService(repository, authorizer, &lifecycleIDs{}, lifecycleClock{now}, 7*24*time.Hour)
	strong := sessions.Session{UserID: lifecycleUser, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey}
	status, err := service.Cancel(context.Background(), CancelCommand{ActorUserID: lifecycleUser, Session: strong, AccountID: lifecycleAccount, ExpectedAccountVersion: 5, Reason: " Resume operations "})
	if err != nil || status.State != StateCanceled || repository.canceled.Reason != "Resume operations" {
		t.Fatalf("status=%+v mutation=%+v err=%v", status, repository.canceled, err)
	}
	if _, err := service.Cancel(context.Background(), CancelCommand{ActorUserID: lifecycleUser, Session: sessions.Session{UserID: lifecycleUser}, AccountID: lifecycleAccount, ExpectedAccountVersion: 5, Reason: "Resume operations"}); !errors.Is(err, strongauth.ErrRequired) {
		t.Fatalf("weak cancel error=%v", err)
	}
}

func TestProcessorClaimsAndEvaluatesOneDurableRequest(t *testing.T) {
	now := time.Date(2026, 8, 25, 16, 0, 0, 0, time.UTC)
	repository := &lifecycleRepository{claim: true, work: Work{RequestID: "33333333-3333-4333-8333-333333333333", AccountID: lifecycleAccount, Attempt: 1}}
	processor, err := NewProcessor(repository, &lifecycleIDs{}, lifecycleClock{now}, 2*time.Minute, 30*24*time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := processor.ProcessOne(context.Background())
	if err != nil || !worked || !repository.evaluated {
		t.Fatalf("worked=%v evaluated=%v err=%v", worked, repository.evaluated, err)
	}
}
