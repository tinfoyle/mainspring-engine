package actionrecovery

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type testAuthorizer struct {
	context     access.AccountContext
	requirement access.Requirement
	err         error
}

func (a *testAuthorizer) Authorize(_ context.Context, _ access.Actor, _ ids.AccountID, r access.Requirement) (access.AccountContext, error) {
	a.requirement = r
	return a.context, a.err
}

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

type testRepository struct {
	requested   RequestCommand
	digest      [sha256.Size]byte
	requestUser ids.UserID
	requestAt   time.Time
	confirmed   ConfirmCommand
	confirmUser ids.UserID
	confirmAt   time.Time
	detail      Detail
	err         error
}

func (r *testRepository) List(context.Context, ids.AccountID, ListQuery) (Page, error) {
	return Page{Items: []Summary{}}, r.err
}
func (r *testRepository) Get(context.Context, ids.AccountID, string) (Detail, error) {
	return r.detail, r.err
}
func (r *testRepository) RequestResolution(_ context.Context, account ids.AccountID, operation, resolution string, outcome State, digest [sha256.Size]byte, user ids.UserID, at time.Time) error {
	r.requested = RequestCommand{AccountID: account, OperationID: operation, ResolutionID: resolution, Outcome: outcome}
	r.digest = digest
	r.requestUser = user
	r.requestAt = at
	return r.err
}
func (r *testRepository) ConfirmResolution(_ context.Context, account ids.AccountID, operation, resolution string, user ids.UserID, at time.Time) error {
	r.confirmed = ConfirmCommand{AccountID: account, OperationID: operation, ResolutionID: resolution}
	r.confirmUser = user
	r.confirmAt = at
	return r.err
}

const testAccount ids.AccountID = "10000000-0000-4000-8000-000000000001"
const testUser ids.UserID = "20000000-0000-4000-8000-000000000002"
const testOperation = "30000000-0000-4000-8000-000000000003"
const testResolution = "40000000-0000-4000-8000-000000000004"

func fixture(t *testing.T) (*Service, *testAuthorizer, *testRepository, testClock) {
	t.Helper()
	authorizer := &testAuthorizer{context: access.AccountContext{Role: accounts.RoleOwner}}
	repository := &testRepository{detail: Detail{Summary: Summary{OperationID: testOperation, State: StateUnknown}}}
	clock := testClock{now: time.Date(2026, 8, 21, 23, 0, 0, 0, time.UTC)}
	service, err := New(authorizer, repository, clock)
	if err != nil {
		t.Fatal(err)
	}
	return service, authorizer, repository, clock
}

func TestResolutionRequestIsOwnerAuthorizedAndHashesOnlyTrimmedEvidence(t *testing.T) {
	service, authorizer, repository, clock := fixture(t)
	detail, err := service.Request(context.Background(), RequestCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, OperationID: testOperation, ResolutionID: testResolution, Outcome: StateSucceeded, Reason: "  provider object inspected  "})
	if err != nil || detail.State != StateUnknown {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	want := sha256.Sum256([]byte("provider object inspected"))
	if repository.digest != want || repository.requestUser != testUser || !repository.requestAt.Equal(clock.now) || !authorizer.requirement.Mutation {
		t.Fatalf("request=%+v requirement=%+v", repository.requested, authorizer.requirement)
	}
}

func TestMemberAndWorkloadCannotInspectOrResolve(t *testing.T) {
	service, authorizer, _, _ := fixture(t)
	authorizer.context.Role = accounts.RoleMember
	if _, err := service.List(context.Background(), access.Actor{UserID: testUser}, testAccount, ListQuery{}); !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("member list=%v", err)
	}
	if _, err := service.Get(context.Background(), access.Actor{WorkloadID: "agent"}, testAccount, testOperation); !errors.Is(err, ErrInvalid) {
		t.Fatalf("workload get=%v", err)
	}
}

func TestConfirmationUsesCurrentHumanAndMutationAuthority(t *testing.T) {
	service, authorizer, repository, clock := fixture(t)
	detail, err := service.Confirm(context.Background(), ConfirmCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, OperationID: testOperation, ResolutionID: testResolution})
	if err != nil || detail.OperationID != testOperation || repository.confirmUser != testUser || !repository.confirmAt.Equal(clock.now) || !authorizer.requirement.Mutation {
		t.Fatalf("detail=%+v confirmation=%+v requirement=%+v err=%v", detail, repository.confirmed, authorizer.requirement, err)
	}
}

func TestInvalidReasonAndCursorFailBeforeAuthority(t *testing.T) {
	service, authorizer, _, _ := fixture(t)
	if _, err := service.Request(context.Background(), RequestCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, OperationID: testOperation, ResolutionID: testResolution, Outcome: StateSucceeded, Reason: "no"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short reason=%v", err)
	}
	if _, err := service.List(context.Background(), access.Actor{UserID: testUser}, testAccount, ListQuery{AfterOperationID: testOperation}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("partial cursor=%v", err)
	}
	if authorizer.requirement.Package != "" {
		t.Fatal("invalid request reached authority")
	}
}
