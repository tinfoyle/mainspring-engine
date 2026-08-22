package work

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	testAccount = "10000000-0000-4000-8000-000000000001"
	testUser    = "20000000-0000-4000-8000-000000000002"
	testRequest = "30000000-0000-4000-8000-000000000003"
)

func TestCreateAuthorizesAndReservesCapacity(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{}
	capacity := &fakeCapacity{}
	service, err := NewService(fakeAuthorizer{role: accounts.RoleMember}, capacity, repository, fakeClock{now})
	if err != nil {
		t.Fatal(err)
	}
	item, err := service.Create(context.Background(), CreateCommand{
		Actor: access.Actor{UserID: ids.UserID(testUser)}, AccountID: ids.AccountID(testAccount), RequestID: testRequest,
		Kind: workdomain.KindTicket, Title: "Prepare the weekly forecast", Priority: workdomain.PriorityHigh,
		Assignment:    workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared},
		Provenance:    workdomain.Provenance{Source: workdomain.SourceManual, CreatedBy: workdomain.Actor{Kind: workdomain.ActorUser, ID: testUser}},
		CorrelationID: "create-work-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != ids.WorkItemID(testRequest) || item.State != workdomain.StateOpen {
		t.Fatalf("item = %+v", item)
	}
	if len(capacity.reserves) != 1 || capacity.reserves[0].PackageCode != PackageCode || capacity.reserves[0].LimitCode != ActiveItems {
		t.Fatalf("reserves = %+v", capacity.reserves)
	}
	if repository.mutation.Kind != MutationCreated {
		t.Fatalf("mutation = %+v", repository.mutation)
	}
}

func TestCreateAllowsAuthorizedWorkloadWithoutHumanRole(t *testing.T) {
	repository := &fakeRepository{}
	capacity := &fakeCapacity{}
	service, _ := NewService(fakeAuthorizer{}, capacity, repository, fakeClock{time.Now()})
	command := createCommand()
	command.Actor = access.Actor{WorkloadID: "baseline-maintenance-worker"}
	command.Provenance.CreatedBy = workdomain.Actor{Kind: workdomain.ActorWorkload, ID: "baseline-maintenance-worker"}
	if _, err := service.Create(context.Background(), command); err != nil || repository.mutation.Actor.Kind != workdomain.ActorWorkload || repository.mutation.Actor.ID != "baseline-maintenance-worker" {
		t.Fatalf("mutation=%+v err=%v", repository.mutation, err)
	}
}

func TestCreateCompensatesCapacityWhenCellWriteFails(t *testing.T) {
	repository := &fakeRepository{createErr: ErrConstraint}
	capacity := &fakeCapacity{reservation: usageadmission.Reservation{NewlyCreated: true}}
	service, _ := NewService(fakeAuthorizer{role: accounts.RoleOwner}, capacity, repository, fakeClock{time.Now()})
	_, err := service.Create(context.Background(), createCommand())
	if !errors.Is(err, ErrConstraint) {
		t.Fatalf("error = %v", err)
	}
	if len(capacity.releases) != 1 || capacity.releases[0].RequestID != testRequest {
		t.Fatalf("releases = %+v", capacity.releases)
	}
}

func TestCreateDoesNotReleaseAReplayedReservationAfterConstraint(t *testing.T) {
	repository := &fakeRepository{createErr: ErrConstraint}
	capacity := &fakeCapacity{}
	service, _ := NewService(fakeAuthorizer{role: accounts.RoleOwner}, capacity, repository, fakeClock{time.Now()})
	_, err := service.Create(context.Background(), createCommand())
	if !errors.Is(err, ErrConstraint) || len(capacity.releases) != 0 {
		t.Fatalf("error=%v releases=%+v", err, capacity.releases)
	}
}

func TestCreateRetainsCapacityWhenCellOutcomeIsAmbiguous(t *testing.T) {
	repository := &fakeRepository{createErr: errors.New("cell commit response lost")}
	capacity := &fakeCapacity{}
	service, _ := NewService(fakeAuthorizer{role: accounts.RoleOwner}, capacity, repository, fakeClock{time.Now()})
	_, err := service.Create(context.Background(), createCommand())
	if !errors.Is(err, ErrCapacityOutcomeUnknown) || len(capacity.releases) != 0 {
		t.Fatalf("error=%v releases=%+v", err, capacity.releases)
	}
}

func TestCreateDoesNotReleaseAnExistingReservationOnPayloadConflict(t *testing.T) {
	repository := &fakeRepository{createErr: ErrConflict}
	capacity := &fakeCapacity{}
	service, _ := NewService(fakeAuthorizer{role: accounts.RoleOwner}, capacity, repository, fakeClock{time.Now()})
	_, err := service.Create(context.Background(), createCommand())
	if !errors.Is(err, ErrConflict) || errors.Is(err, ErrCapacityOutcomeUnknown) || len(capacity.releases) != 0 {
		t.Fatalf("error=%v releases=%+v", err, capacity.releases)
	}
}

func TestCreateCompensatesAFreshReservationAfterSerializedConflict(t *testing.T) {
	repository := &fakeRepository{createErr: ErrConflict}
	capacity := &fakeCapacity{reservation: usageadmission.Reservation{NewlyCreated: true}}
	service, _ := NewService(fakeAuthorizer{role: accounts.RoleOwner}, capacity, repository, fakeClock{time.Now()})
	_, err := service.Create(context.Background(), createCommand())
	if !errors.Is(err, ErrConflict) || len(capacity.releases) != 1 {
		t.Fatalf("error=%v releases=%+v", err, capacity.releases)
	}
}

func TestTerminalTransitionDefersCapacityToDurableReconciliation(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	item := materialized(t, now)
	item, _ = item.Transition(workdomain.TransitionCommand{To: workdomain.StateInProgress, Role: accounts.RoleOwner, Actor: workdomain.Actor{Kind: workdomain.ActorUser, ID: testUser}, ExpectedVersion: 1, At: now.Add(time.Minute)})
	repository := &fakeRepository{item: item}
	capacity := &fakeCapacity{}
	service, _ := NewService(fakeAuthorizer{role: accounts.RoleMember}, capacity, repository, fakeClock{now.Add(2 * time.Minute)})
	completed, err := service.Transition(context.Background(), TransitionCommand{Actor: access.Actor{UserID: ids.UserID(testUser)}, AccountID: ids.AccountID(testAccount), WorkItemID: item.ID, To: workdomain.StateDone, ExpectedVersion: item.Version, CorrelationID: "complete-work-test"})
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != workdomain.StateDone || completed.CompletedAt == nil || len(capacity.releases) != 0 || repository.releasedReservation != "" {
		t.Fatalf("completed=%+v releases=%+v marked=%s", completed, capacity.releases, repository.releasedReservation)
	}
}

func TestReopenCompensatesOnlyAFreshReservationAfterLosingWrite(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	item := materialized(t, now)
	item, _ = item.Transition(workdomain.TransitionCommand{To: workdomain.StateInProgress, Role: accounts.RoleOwner, Actor: workdomain.Actor{Kind: workdomain.ActorUser, ID: testUser}, ExpectedVersion: item.Version, At: now.Add(time.Minute)})
	item, _ = item.Transition(workdomain.TransitionCommand{To: workdomain.StateDone, Role: accounts.RoleOwner, Actor: workdomain.Actor{Kind: workdomain.ActorUser, ID: testUser}, ExpectedVersion: item.Version, At: now.Add(2 * time.Minute)})

	for _, test := range []struct {
		name          string
		newlyCreated  bool
		expectedDrops int
	}{
		{name: "fresh", newlyCreated: true, expectedDrops: 1},
		{name: "replayed", newlyCreated: false, expectedDrops: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			capacity := &fakeCapacity{reservation: usageadmission.Reservation{NewlyCreated: test.newlyCreated}}
			repository := &fakeRepository{item: item, updateErr: ErrConflict}
			service, _ := NewService(fakeAuthorizer{role: accounts.RoleOwner}, capacity, repository, fakeClock{now.Add(3 * time.Minute)})
			_, err := service.Transition(context.Background(), TransitionCommand{Actor: access.Actor{UserID: ids.UserID(testUser)}, AccountID: ids.AccountID(testAccount), WorkItemID: item.ID, To: workdomain.StateOpen, ExpectedVersion: item.Version, RequestID: testRequest, Reason: "new evidence", CorrelationID: testRequest})
			if !errors.Is(err, ErrConflict) || len(capacity.releases) != test.expectedDrops {
				t.Fatalf("error=%v releases=%+v", err, capacity.releases)
			}
		})
	}
}

func TestReopenRetainsCapacityWhenCellOutcomeIsAmbiguous(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	item := materialized(t, now)
	item, _ = item.Transition(workdomain.TransitionCommand{To: workdomain.StateInProgress, Role: accounts.RoleOwner, Actor: workdomain.Actor{Kind: workdomain.ActorUser, ID: testUser}, ExpectedVersion: item.Version, At: now.Add(time.Minute)})
	item, _ = item.Transition(workdomain.TransitionCommand{To: workdomain.StateDone, Role: accounts.RoleOwner, Actor: workdomain.Actor{Kind: workdomain.ActorUser, ID: testUser}, ExpectedVersion: item.Version, At: now.Add(2 * time.Minute)})
	capacity := &fakeCapacity{reservation: usageadmission.Reservation{NewlyCreated: true}}
	repository := &fakeRepository{item: item, updateErr: errors.New("cell commit response lost")}
	service, _ := NewService(fakeAuthorizer{role: accounts.RoleOwner}, capacity, repository, fakeClock{now.Add(3 * time.Minute)})
	_, err := service.Transition(context.Background(), TransitionCommand{Actor: access.Actor{UserID: ids.UserID(testUser)}, AccountID: ids.AccountID(testAccount), WorkItemID: item.ID, To: workdomain.StateOpen, ExpectedVersion: item.Version, RequestID: testRequest, Reason: "new evidence", CorrelationID: testRequest})
	if !errors.Is(err, ErrCapacityOutcomeUnknown) || len(capacity.releases) != 0 {
		t.Fatalf("error=%v releases=%+v", err, capacity.releases)
	}
}

func TestAttentionResumptionAuthorizesDeduplicatesAndOrdersParents(t *testing.T) {
	now := time.Date(2026, 8, 21, 16, 0, 0, 0, time.UTC)
	repository := &fakeRepository{}
	service, _ := NewService(fakeAuthorizer{role: accounts.RoleMember}, &fakeCapacity{}, repository, fakeClock{now})
	first := ids.WorkItemID("30000000-0000-4000-8000-000000000003")
	second := ids.WorkItemID("40000000-0000-4000-8000-000000000004")
	_, err := service.ResumeAttentionParents(context.Background(), ResumeAttentionCommand{
		Actor: access.Actor{UserID: ids.UserID(testUser)}, AccountID: ids.AccountID(testAccount),
		ParentIDs: []ids.WorkItemID{second, first, second}, CorrelationID: "attention-answer-test",
	})
	if err != nil || len(repository.resumedParents) != 2 || repository.resumedParents[0] != first || repository.resumedParents[1] != second ||
		repository.resumeRole != accounts.RoleMember || repository.resumeActor.ID != testUser || repository.resumeCorrelation != "attention-answer-test" {
		t.Fatalf("parents=%v role=%q actor=%+v correlation=%q err=%v", repository.resumedParents, repository.resumeRole, repository.resumeActor, repository.resumeCorrelation, err)
	}
}

func TestViewerCannotCreateWork(t *testing.T) {
	capacity := &fakeCapacity{}
	service, _ := NewService(fakeAuthorizer{role: accounts.RoleViewer}, capacity, &fakeRepository{}, fakeClock{time.Now()})
	_, err := service.Create(context.Background(), createCommand())
	if !access.IsDenied(err, access.DenialRole) || len(capacity.reserves) != 0 {
		t.Fatalf("error=%v reserves=%v", err, capacity.reserves)
	}
}

func TestProvenanceAndConversationCommandsAuthorizeAndPersistTypedMutations(t *testing.T) {
	now := time.Date(2026, 8, 21, 14, 0, 0, 0, time.UTC)
	repository := &fakeRepository{item: materialized(t, now)}
	service, _ := NewService(fakeAuthorizer{role: accounts.RoleMember}, &fakeCapacity{}, repository, fakeClock{now.Add(time.Minute)})
	runID := "40000000-0000-4000-8000-000000000004"
	linked, err := service.AttachProvenance(context.Background(), AttachProvenanceCommand{
		Actor: access.Actor{UserID: ids.UserID(testUser)}, AccountID: ids.AccountID(testAccount), WorkItemID: repository.item.ID,
		Kind: workdomain.ProvenanceRun, ReferenceID: runID, ExpectedVersion: repository.item.Version, Reason: "record the originating run", CorrelationID: "provenance-link-test",
	})
	if err != nil || linked.Provenance.RunID != runID || repository.mutation.Kind != MutationProvenance || repository.mutation.ReferenceKind != string(workdomain.ProvenanceRun) {
		t.Fatalf("provenance item=%+v mutation=%+v err=%v", linked, repository.mutation, err)
	}
	conversationID := "50000000-0000-4000-8000-000000000005"
	linked, err = service.LinkConversation(context.Background(), LinkConversationCommand{
		Actor: access.Actor{UserID: ids.UserID(testUser)}, AccountID: ids.AccountID(testAccount), WorkItemID: repository.item.ID,
		ConversationID: conversationID, ExpectedVersion: repository.item.Version, Reason: "preserve the related discussion", CorrelationID: "conversation-link-test",
	})
	if err != nil || linked.Provenance.ConversationID != conversationID || repository.mutation.Kind != MutationConversation || repository.mutation.ReferenceKind != "conversation" {
		t.Fatalf("conversation item=%+v mutation=%+v err=%v", linked, repository.mutation, err)
	}
}

func createCommand() CreateCommand {
	return CreateCommand{Actor: access.Actor{UserID: ids.UserID(testUser)}, AccountID: ids.AccountID(testAccount), RequestID: testRequest, Kind: workdomain.KindTodo, Title: "A valid work item", Priority: workdomain.PriorityNormal, Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared}, Provenance: workdomain.Provenance{Source: workdomain.SourceManual, CreatedBy: workdomain.Actor{Kind: workdomain.ActorUser, ID: testUser}}, CorrelationID: "work-test"}
}

func materialized(t *testing.T, now time.Time) workdomain.Item {
	t.Helper()
	command := createCommand()
	draft, err := workdomain.NewDraft(workdomain.Draft{ID: ids.WorkItemID(command.RequestID), AccountID: command.AccountID, Kind: command.Kind, Title: command.Title, Priority: command.Priority, Assignment: command.Assignment, Provenance: command.Provenance, CapacityReservationID: command.RequestID})
	if err != nil {
		t.Fatal(err)
	}
	item, err := workdomain.Materialize(draft, 1, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

type fakeAuthorizer struct{ role accounts.MembershipRole }

func (f fakeAuthorizer) Authorize(_ context.Context, _ access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	if requirement.Package != PackageCode {
		return access.AccountContext{}, errors.New("Work package was not required")
	}
	return access.AccountContext{AccountID: accountID, Role: f.role}, nil
}

type fakeCapacity struct {
	reserves               []usageadmission.ReserveCommand
	releases               []usageadmission.ReleaseCommand
	reserveErr, releaseErr error
	reservation            usageadmission.Reservation
}

func (f *fakeCapacity) Reserve(_ context.Context, command usageadmission.ReserveCommand) (usageadmission.Reservation, error) {
	f.reserves = append(f.reserves, command)
	return f.reservation, f.reserveErr
}
func (f *fakeCapacity) Release(_ context.Context, command usageadmission.ReleaseCommand) (usageadmission.Reservation, error) {
	f.releases = append(f.releases, command)
	return usageadmission.Reservation{}, f.releaseErr
}

type fakeRepository struct {
	item                workdomain.Item
	mutation            Mutation
	resumedParents      []ids.WorkItemID
	resumeRole          accounts.MembershipRole
	resumeActor         workdomain.Actor
	resumeCorrelation   string
	createErr           error
	updateErr           error
	releasedReservation string
}

func (f *fakeRepository) Create(_ context.Context, draft workdomain.Draft, mutation Mutation) (workdomain.Item, error) {
	f.mutation = mutation
	if f.createErr != nil {
		return workdomain.Item{}, f.createErr
	}
	item, err := workdomain.Materialize(draft, 1, 0, mutation.At)
	f.item = item
	return item, err
}
func (f *fakeRepository) Get(_ context.Context, _ ids.AccountID, _ ids.WorkItemID) (workdomain.Item, error) {
	return f.item, nil
}
func (f *fakeRepository) Update(_ context.Context, item workdomain.Item, _ uint64, mutation Mutation) (workdomain.Item, error) {
	f.item, f.mutation = item, mutation
	return item, f.updateErr
}
func (f *fakeRepository) ResumeAttentionParents(_ context.Context, _ ids.AccountID, parentIDs []ids.WorkItemID, role accounts.MembershipRole, actor workdomain.Actor, correlationID string, _ time.Time) ([]workdomain.Item, error) {
	f.resumedParents, f.resumeRole, f.resumeActor, f.resumeCorrelation = parentIDs, role, actor, correlationID
	return []workdomain.Item{f.item}, f.updateErr
}
func (f *fakeRepository) MarkCapacityReleased(_ context.Context, _ ids.AccountID, _ ids.WorkItemID, requestID string, _ time.Time) error {
	f.releasedReservation = requestID
	return nil
}
func (f *fakeRepository) List(context.Context, ids.AccountID, ListQuery) (Page, error) {
	return Page{}, nil
}
func (f *fakeRepository) Children(context.Context, ids.AccountID, ids.WorkItemID, int) ([]workdomain.Item, error) {
	return nil, nil
}
func (f *fakeRepository) Summary(context.Context, ids.AccountID) (Summary, error) {
	return Summary{}, nil
}

type fakeClock struct{ now time.Time }

func (f fakeClock) Now() time.Time { return f.now }
