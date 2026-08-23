package accountexport

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	exportAccount ids.AccountID = "f1100000-0000-4000-8000-000000000001"
	exportOwner   ids.UserID    = "f1200000-0000-4000-8000-000000000001"
)

type exportClock struct{ now time.Time }

func (clock exportClock) Now() time.Time { return clock.now }

type exportIDs struct{ index int }

func (generator *exportIDs) New() string {
	generator.index++
	return []string{
		"f1300000-0000-4000-8000-000000000001",
		"f1300000-0000-4000-8000-000000000002",
		"f1300000-0000-4000-8000-000000000003",
	}[generator.index-1]
}

type exportAuthorizer struct {
	requirement access.Requirement
	err         error
}

func (authorizer *exportAuthorizer) Authorize(_ context.Context, actor access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	authorizer.requirement = requirement
	if authorizer.err != nil {
		return access.AccountContext{}, authorizer.err
	}
	return access.AccountContext{AccountID: accountID, CellID: "cell-us-east-01", PlacementGeneration: 4, Role: accounts.RoleOwner}, nil
}

type exportStore struct {
	created  CreateMutation
	canceled CancelMutation
}

func (store *exportStore) Create(_ context.Context, mutation CreateMutation) (Status, error) {
	store.created = mutation
	return Status{ID: mutation.ID, AccountID: mutation.AccountID, State: StateQueued, Version: 1}, nil
}
func (*exportStore) Get(context.Context, ids.AccountID, string) (Status, error)    { return Status{}, nil }
func (*exportStore) List(context.Context, ids.AccountID, uint64) ([]Status, error) { return nil, nil }
func (store *exportStore) Cancel(_ context.Context, mutation CancelMutation) (Status, error) {
	store.canceled = mutation
	return Status{ID: mutation.ID, AccountID: mutation.AccountID, State: StateCanceled, Version: mutation.ExpectedVersion + 1}, nil
}
func (*exportStore) ClaimBuild(context.Context, time.Time, time.Duration, string, string) (Work, bool, error) {
	return Work{}, false, nil
}
func (*exportStore) Complete(context.Context, CompleteMutation) (Status, error) {
	return Status{}, nil
}
func (*exportStore) RecordFailure(context.Context, FailureMutation) (Status, error) {
	return Status{}, nil
}
func (*exportStore) ClaimDeletion(context.Context, time.Time, time.Duration, string, string) (DeletionWork, bool, error) {
	return DeletionWork{}, false, nil
}
func (*exportStore) CompleteDeletion(context.Context, DeletionWork, time.Time, string) (Status, error) {
	return Status{}, nil
}

func TestOwnerCreatesAccountLevelExportWithStrongAuthentication(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	store, authorizer := &exportStore{}, &exportAuthorizer{}
	service, err := NewService(store, authorizer, &exportIDs{}, exportClock{now}, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	session := sessions.Session{UserID: exportOwner, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey}
	status, err := service.Create(context.Background(), CreateCommand{AccountID: exportAccount, Actor: exportOwner, Session: session})
	if err != nil || status.State != StateQueued {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if len(authorizer.requirement.Roles) != 1 || authorizer.requirement.Roles[0] != accounts.RoleOwner || authorizer.requirement.Package != "" || authorizer.requirement.Mutation {
		t.Fatalf("requirement=%+v", authorizer.requirement)
	}
	if store.created.ID == store.created.EventID || store.created.AccountID != exportAccount || store.created.RequestedBy != exportOwner ||
		store.created.CellID != "cell-us-east-01" || store.created.PlacementGeneration != 4 || store.created.RequestedAt != now || store.created.ExpiresAt != now.Add(7*24*time.Hour) {
		t.Fatalf("create mutation=%+v", store.created)
	}
	weak := session
	weak.ReauthenticationMethod = sessions.AuthenticationMethodPassword
	if _, err := service.Create(context.Background(), CreateCommand{AccountID: exportAccount, Actor: exportOwner, Session: weak}); !errors.Is(err, strongauth.ErrRequired) {
		t.Fatalf("weak authentication error=%v", err)
	}
}

func TestOwnerCancelIsVersionedAndStronglyAuthenticated(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	store := &exportStore{}
	service, _ := NewService(store, &exportAuthorizer{}, &exportIDs{}, exportClock{now}, 7*24*time.Hour)
	session := sessions.Session{UserID: exportOwner, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey}
	requestID := "f1400000-0000-4000-8000-000000000001"
	status, err := service.Cancel(context.Background(), CancelCommand{AccountID: exportAccount, Actor: exportOwner, ID: requestID, ExpectedVersion: 2, Session: session})
	if err != nil || status.State != StateCanceled || store.canceled.ID != requestID || store.canceled.ExpectedVersion != 2 || store.canceled.At != now {
		t.Fatalf("status=%+v mutation=%+v err=%v", status, store.canceled, err)
	}
	if _, err := service.Cancel(context.Background(), CancelCommand{AccountID: exportAccount, Actor: exportOwner, ID: requestID, Session: session}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing version error=%v", err)
	}
}
