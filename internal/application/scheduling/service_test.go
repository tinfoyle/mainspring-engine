package scheduling

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type scheduleServiceAuthorizer struct{ requirement access.Requirement }

func (authorizer *scheduleServiceAuthorizer) Authorize(_ context.Context, _ access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	authorizer.requirement = requirement
	return access.AccountContext{AccountID: accountID, Role: accounts.RoleMember, PackageAccess: &entitlements.PackageAccess{}}, nil
}

type scheduleServiceClock struct{ now time.Time }

func (clock *scheduleServiceClock) Now() time.Time {
	value := clock.now
	clock.now = clock.now.Add(time.Minute)
	return value
}

type scheduleServiceStore struct {
	value    domain.Schedule
	mutation Mutation
}

func (store *scheduleServiceStore) Create(_ context.Context, value domain.Schedule, mutation Mutation) (domain.Schedule, bool, error) {
	if store.value.ID != "" {
		return store.value, false, nil
	}
	store.value, store.mutation = value, mutation
	return value, true, nil
}

func (store *scheduleServiceStore) Get(context.Context, ids.AccountID, ids.ScheduleID) (domain.Schedule, error) {
	return store.value, nil
}

func (store *scheduleServiceStore) List(context.Context, ids.AccountID, ListQuery) (Page, error) {
	return Page{Items: []domain.Schedule{store.value}}, nil
}

func (store *scheduleServiceStore) Update(_ context.Context, value domain.Schedule, expected uint64, mutation Mutation) (domain.Schedule, error) {
	if store.value.Version == expected+1 {
		return store.value, nil
	}
	store.value, store.mutation = value, mutation
	return value, nil
}

func (store *scheduleServiceStore) EnqueueTrigger(_ context.Context, request TriggerRequest, mutation Mutation) (Trigger, bool, error) {
	store.mutation = mutation
	return Trigger{ID: request.ID, AccountID: request.AccountID, ScheduleID: request.ScheduleID, ScheduleVersion: request.ScheduleVersion,
		RequestedBy: request.RequestedBy, RequestedAt: request.RequestedAt, State: "accepted"}, true, nil
}

func TestServiceCreatesRevisesAndReplaysLifecycleCommands(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	authorizer := &scheduleServiceAuthorizer{}
	store := &scheduleServiceStore{}
	service, err := New(authorizer, store, &scheduleServiceClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	actor := access.Actor{UserID: "11000000-0000-4000-8000-000000000001"}
	accountID := ids.AccountID("21000000-0000-4000-8000-000000000001")
	requestID := "31000000-0000-4000-8000-000000000001"
	template := domain.AgentRunTemplate{BoardroomID: "41000000-0000-4000-8000-000000000001", Mode: "selected",
		PersonaIDs: []ids.PersonaID{"51000000-0000-4000-8000-000000000001"}, Subject: "Daily review", Prompt: "Review priorities."}
	create := CreateCommand{Actor: actor, AccountID: accountID, RequestID: requestID, Name: "Daily review", Timezone: "America/New_York",
		Recurrence:      domain.Recurrence{Frequency: domain.FrequencyDaily, LocalHour: 9, GapPolicy: domain.GapSkip, OverlapPolicy: domain.OverlapFirst},
		MissedRunPolicy: domain.MissedSkip, Template: template}
	created, fresh, err := service.Create(context.Background(), create)
	if err != nil || !fresh || created.Version != 1 || !authorizer.requirement.Mutation || authorizer.requirement.Package != PackageCode {
		t.Fatalf("created=%+v fresh=%v requirement=%+v err=%v", created, fresh, authorizer.requirement, err)
	}
	replayed, fresh, err := service.Create(context.Background(), create)
	if err != nil || fresh || !reflect.DeepEqual(replayed, created) {
		t.Fatalf("replayed=%+v fresh=%v err=%v", replayed, fresh, err)
	}

	revisionRequest := "61000000-0000-4000-8000-000000000001"
	revise := ReviseCommand{Actor: actor, AccountID: accountID, ScheduleID: created.ID, RequestID: revisionRequest,
		ExpectedVersion: 1, Name: "Daily priorities", Timezone: create.Timezone, Recurrence: create.Recurrence,
		MissedRunPolicy: domain.MissedCatchUpOne, Template: template}
	revised, err := service.Revise(context.Background(), revise)
	if err != nil || revised.Version != 2 || revised.Name != "Daily priorities" {
		t.Fatalf("revised=%+v err=%v", revised, err)
	}
	if replayedRevision, err := service.Revise(context.Background(), revise); err != nil || !reflect.DeepEqual(replayedRevision, revised) {
		t.Fatalf("revision replay=%+v err=%v", replayedRevision, err)
	}

	paused, err := service.Pause(context.Background(), TransitionCommand{Actor: actor, AccountID: accountID, ScheduleID: created.ID,
		RequestID: "71000000-0000-4000-8000-000000000001", ExpectedVersion: 2})
	if err != nil || paused.State != domain.StatePaused || paused.Version != 3 {
		t.Fatalf("paused=%+v err=%v", paused, err)
	}
	deleted, err := service.Delete(context.Background(), TransitionCommand{Actor: actor, AccountID: accountID, ScheduleID: created.ID,
		RequestID: "81000000-0000-4000-8000-000000000001", ExpectedVersion: 3})
	if err != nil || deleted.State != domain.StateDeleted || deleted.Version != 4 {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
}

func TestServiceEnqueuesTriggerWithoutMutatingDefinition(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	store := &scheduleServiceStore{}
	service, err := New(&scheduleServiceAuthorizer{}, store, &scheduleServiceClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	command := TransitionCommand{Actor: access.Actor{UserID: "11000000-0000-4000-8000-000000000001"}, AccountID: "21000000-0000-4000-8000-000000000001",
		ScheduleID: "31000000-0000-4000-8000-000000000001", RequestID: "41000000-0000-4000-8000-000000000001", ExpectedVersion: 7, Reason: "Run the review now"}
	trigger, created, err := service.TriggerNow(context.Background(), command)
	if err != nil || !created || !trigger.Valid() || trigger.ScheduleVersion != 7 || store.value.Version != 0 || store.mutation.Kind != "trigger_requested" {
		t.Fatalf("trigger=%+v created=%v stored=%+v mutation=%+v err=%v", trigger, created, store.value, store.mutation, err)
	}
}

func TestOtherMemberCannotActivateOrTriggerEmailToCreator(t *testing.T) {
	owner := ids.UserID("11000000-0000-4000-8000-000000000001")
	other := access.Actor{UserID: "11000000-0000-4000-8000-000000000002"}
	store := &scheduleServiceStore{value: domain.Schedule{ID: "31000000-0000-4000-8000-000000000001", AccountID: "21000000-0000-4000-8000-000000000001", CreatedBy: owner, Version: 1, State: domain.StatePaused, Template: domain.AgentRunTemplate{EmailSelf: true}}}
	service, _ := New(&scheduleServiceAuthorizer{}, store, &scheduleServiceClock{now: time.Now()})
	cmd := TransitionCommand{Actor: other, AccountID: store.value.AccountID, ScheduleID: store.value.ID, RequestID: "61000000-0000-4000-8000-000000000001", ExpectedVersion: 1, Reason: "Test permission"}
	if _, err := service.Resume(context.Background(), cmd); err != ErrInvalid {
		t.Fatalf("resume err=%v", err)
	}
	if _, _, err := service.TriggerNow(context.Background(), cmd); err != ErrInvalid {
		t.Fatalf("trigger err=%v", err)
	}
	if store.mutation.Kind != "" {
		t.Fatal("unauthorized schedule mutation")
	}
}
