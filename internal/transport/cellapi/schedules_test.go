package cellapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	schedulingapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	schedulingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/testsupport/openapifixture"
)

const (
	scheduleAccount   = "a1000000-0000-4000-8000-000000000001"
	scheduleUser      = "a2000000-0000-4000-8000-000000000001"
	scheduleOperation = "a3000000-0000-4000-8000-000000000001"
	scheduleID        = "a4000000-0000-4000-8000-000000000001"
	scheduleBoardroom = "a5000000-0000-4000-8000-000000000001"
	schedulePersona   = "a6000000-0000-4000-8000-000000000001"
)

type scheduleTransportService struct {
	create     schedulingapp.CreateCommand
	revise     schedulingapp.ReviseCommand
	transition schedulingapp.TransitionCommand
	list       schedulingapp.ListCommand
	now        time.Time
}

func (service *scheduleTransportService) Create(_ context.Context, command schedulingapp.CreateCommand) (schedulingdomain.Schedule, bool, error) {
	service.create = command
	return service.value(ids.ScheduleID(command.RequestID), 1, schedulingdomain.StateActive), true, nil
}

func (service *scheduleTransportService) Get(_ context.Context, query schedulingapp.GetQuery) (schedulingdomain.Schedule, error) {
	return service.value(query.ScheduleID, 2, schedulingdomain.StateActive), nil
}

func (service *scheduleTransportService) List(_ context.Context, command schedulingapp.ListCommand) (schedulingapp.Page, error) {
	service.list = command
	value := service.value(ids.ScheduleID(scheduleID), 2, schedulingdomain.StateActive)
	return schedulingapp.Page{Items: []schedulingdomain.Schedule{value}, NextCursor: &schedulingapp.Cursor{UpdatedAt: service.now, ID: value.ID}}, nil
}

func (service *scheduleTransportService) Revise(_ context.Context, command schedulingapp.ReviseCommand) (schedulingdomain.Schedule, error) {
	service.revise = command
	value := service.value(command.ScheduleID, command.ExpectedVersion+1, schedulingdomain.StateActive)
	value.Name = command.Name
	return value, nil
}

func (service *scheduleTransportService) Pause(_ context.Context, command schedulingapp.TransitionCommand) (schedulingdomain.Schedule, error) {
	service.transition = command
	return service.value(command.ScheduleID, command.ExpectedVersion+1, schedulingdomain.StatePaused), nil
}

func (service *scheduleTransportService) Resume(_ context.Context, command schedulingapp.TransitionCommand) (schedulingdomain.Schedule, error) {
	service.transition = command
	return service.value(command.ScheduleID, command.ExpectedVersion+1, schedulingdomain.StateActive), nil
}

func (service *scheduleTransportService) Delete(_ context.Context, command schedulingapp.TransitionCommand) (schedulingdomain.Schedule, error) {
	service.transition = command
	return service.value(command.ScheduleID, command.ExpectedVersion+1, schedulingdomain.StateDeleted), nil
}

func (service *scheduleTransportService) TriggerNow(_ context.Context, command schedulingapp.TransitionCommand) (schedulingapp.Trigger, bool, error) {
	service.transition = command
	return schedulingapp.Trigger{ID: command.RequestID, AccountID: command.AccountID, ScheduleID: command.ScheduleID,
		ScheduleVersion: command.ExpectedVersion, RequestedBy: command.Actor.UserID, RequestedAt: service.now, State: "accepted"}, true, nil
}

func (service *scheduleTransportService) value(id ids.ScheduleID, version uint64, state schedulingdomain.State) schedulingdomain.Schedule {
	next := service.now.Add(time.Hour)
	if state != schedulingdomain.StateActive {
		next = time.Time{}
	}
	value := schedulingdomain.Schedule{ID: id, AccountID: ids.AccountID(scheduleAccount), Name: "Daily review", Timezone: "America/New_York",
		Recurrence:      schedulingdomain.Recurrence{Frequency: schedulingdomain.FrequencyDaily, LocalHour: 9, GapPolicy: schedulingdomain.GapSkip, OverlapPolicy: schedulingdomain.OverlapFirst},
		MissedRunPolicy: schedulingdomain.MissedSkip, Template: schedulingdomain.AgentRunTemplate{BoardroomID: ids.BoardroomID(scheduleBoardroom), Mode: "selected", PersonaIDs: []ids.PersonaID{ids.PersonaID(schedulePersona)}, Subject: "Daily review", Prompt: "Review priorities."},
		State: state, Version: version, CreatedBy: ids.UserID(scheduleUser), CreatedAt: service.now, UpdatedAt: service.now}
	if !next.IsZero() {
		value.NextRunAt = &next
	}
	return value
}

func TestScheduleRoutesBindRoutedAuthorityAndLifecycleCommands(t *testing.T) {
	service := &scheduleTransportService{now: time.Date(2026, 8, 22, 17, 0, 0, 0, time.UTC)}
	server, err := New(claimAcceptor{claims: scheduleClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithScheduling(service))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := openapifixture.Load(filepath.Join("..", "..", "..", "api", "spyglass.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	definition := `"name":"Daily review","timezone":"America/New_York","recurrence":{"frequency":"daily","local_hour":9,"local_minute":0,"weekdays":[],"gap_policy":"skip","overlap_policy":"first"},"missed_run_policy":"skip","template":{"boardroom_id":"` + scheduleBoardroom + `","mode":"selected","persona_ids":["` + schedulePersona + `"],"subject":"Daily review","prompt":"Review priorities.","work_item_ids":[],"knowledge_fact_ids":[],"knowledge_document_ids":[],"baseline_assessment_ids":[]}`
	create := scheduleCommandRequest(server.Handler(), http.MethodPost, "/api/v1/accounts/"+scheduleAccount+"/schedules", `{`+definition+`}`)
	if create.Code != http.StatusCreated || create.Header().Get("Location") != "/api/v1/accounts/"+scheduleAccount+"/schedules/"+scheduleOperation || service.create.RequestID != scheduleOperation || service.create.Actor.UserID != ids.UserID(scheduleUser) {
		t.Fatalf("create=%d location=%q body=%s command=%+v", create.Code, create.Header().Get("Location"), create.Body.String(), service.create)
	}
	if err := contract.ValidateResponse(http.MethodPost, "/api/v1/accounts/"+scheduleAccount+"/schedules", create.Code, create.Header(), create.Body.Bytes()); err != nil {
		t.Fatal(err)
	}

	target := "/api/v1/accounts/" + scheduleAccount + "/schedules/" + scheduleID
	revise := scheduleCommandRequest(server.Handler(), http.MethodPut, target, `{"expected_version":2,`+definition+`}`)
	if revise.Code != http.StatusOK || service.revise.ExpectedVersion != 2 || service.revise.ScheduleID != ids.ScheduleID(scheduleID) || service.revise.RequestID != scheduleOperation {
		t.Fatalf("revise=%d body=%s command=%+v", revise.Code, revise.Body.String(), service.revise)
	}
	if err := contract.ValidateResponse(http.MethodPut, target, revise.Code, revise.Header(), revise.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	pause := scheduleCommandRequest(server.Handler(), http.MethodPost, target+"/pauses", `{"expected_version":3,"reason":"Pause for maintenance"}`)
	if pause.Code != http.StatusOK || service.transition.ExpectedVersion != 3 || service.transition.Reason != "Pause for maintenance" || !strings.Contains(pause.Body.String(), `"state":"paused"`) {
		t.Fatalf("pause=%d body=%s command=%+v", pause.Code, pause.Body.String(), service.transition)
	}
	trigger := scheduleCommandRequest(server.Handler(), http.MethodPost, target+"/triggers", `{"expected_version":4,"reason":"Run now"}`)
	if trigger.Code != http.StatusAccepted || service.transition.ExpectedVersion != 4 || !strings.Contains(trigger.Body.String(), `"state":"accepted"`) || !strings.Contains(trigger.Body.String(), `"id":"`+scheduleOperation+`"`) {
		t.Fatalf("trigger=%d body=%s command=%+v", trigger.Code, trigger.Body.String(), service.transition)
	}
	if err := contract.ValidateResponse(http.MethodPost, target+"/triggers", trigger.Code, trigger.Header(), trigger.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	deleted := scheduleCommandRequest(server.Handler(), http.MethodDelete, target, `{"expected_version":4}`)
	if deleted.Code != http.StatusOK || service.transition.ExpectedVersion != 4 || !strings.Contains(deleted.Body.String(), `"state":"deleted"`) {
		t.Fatalf("delete=%d body=%s command=%+v", deleted.Code, deleted.Body.String(), service.transition)
	}
	if err := contract.ValidateResponse(http.MethodDelete, target, deleted.Code, deleted.Header(), deleted.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleRoutesExposeBoundedCursorAndRejectBoundaryMismatch(t *testing.T) {
	service := &scheduleTransportService{now: time.Date(2026, 8, 22, 17, 0, 0, 0, time.UTC)}
	server, _ := New(claimAcceptor{claims: scheduleClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithScheduling(service))
	contract, err := openapifixture.Load(filepath.Join("..", "..", "..", "api", "spyglass.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	list := scheduleQueryRequest(server.Handler(), "/api/v1/accounts/"+scheduleAccount+"/schedules?limit=25")
	if list.Code != http.StatusOK || service.list.Query.Limit != 25 || !strings.Contains(list.Body.String(), `"next_cursor":"`) {
		t.Fatalf("list=%d body=%s query=%+v", list.Code, list.Body.String(), service.list.Query)
	}
	if err := contract.ValidateResponse(http.MethodGet, "/api/v1/accounts/"+scheduleAccount+"/schedules", list.Code, list.Header(), list.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	missingVersion := scheduleCommandRequest(server.Handler(), http.MethodPost, "/api/v1/accounts/"+scheduleAccount+"/schedules/"+scheduleID+"/pauses", `{}`)
	if missingVersion.Code != http.StatusBadRequest {
		t.Fatalf("missing version=%d body=%s", missingVersion.Code, missingVersion.Body.String())
	}
	crossAccount := scheduleQueryRequest(server.Handler(), "/api/v1/accounts/a9000000-0000-4000-8000-000000000009/schedules")
	if crossAccount.Code != http.StatusNotFound {
		t.Fatalf("cross account=%d body=%s", crossAccount.Code, crossAccount.Body.String())
	}
	mismatch := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+scheduleAccount+"/schedules/"+scheduleID+"/pauses", strings.NewReader(`{"expected_version":1}`))
	mismatch.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	mismatch.Header.Set("Content-Type", "application/json")
	mismatch.Header.Set("Idempotency-Key", "a8000000-0000-4000-8000-000000000008")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, mismatch)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_idempotency_key") {
		t.Fatalf("mismatch=%d body=%s", response.Code, response.Body.String())
	}
}

func scheduleCommandRequest(handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", scheduleOperation)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func scheduleQueryRequest(handler http.Handler, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func scheduleClaims() routecontext.Claims {
	return routecontext.Claims{Authority: routecontext.Authority{AccountID: ids.AccountID(scheduleAccount), ActorKind: "user", ActorID: scheduleUser,
		Role: "owner", CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 3, OperationID: scheduleOperation,
		PackageAccess: &routecontext.PackageAccess{Code: "agents", Version: 1, Mode: "enabled"}}}
}

var _ SchedulingService = (*scheduleTransportService)(nil)
