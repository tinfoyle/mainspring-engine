package cellapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	schedulingapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	schedulingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/workloadidentity"
)

type scheduleExecutionTransportStub struct {
	snapshot      schedulingapp.ExecutionSnapshot
	loadCalls     int
	dispatchCalls int
	skipCalls     int
	command       schedulingapp.OccurrenceCommand
}

func (stub *scheduleExecutionTransportStub) Load(_ context.Context, _ schedulingapp.ExecutionClaim) (schedulingapp.ExecutionSnapshot, error) {
	stub.loadCalls++
	return stub.snapshot, nil
}

func (stub *scheduleExecutionTransportStub) Dispatch(_ context.Context, command schedulingapp.OccurrenceCommand) (bool, error) {
	stub.dispatchCalls++
	stub.command = command
	return true, nil
}

func (stub *scheduleExecutionTransportStub) Skip(_ context.Context, command schedulingapp.OccurrenceCommand) (bool, error) {
	stub.skipCalls++
	stub.command = command
	return false, nil
}

func TestScheduleExecutionPrivateTransportLoadsAndDispatchesExactContracts(t *testing.T) {
	claim, snapshot, command := scheduleExecutionTransportFixture(t)
	stub := &scheduleExecutionTransportStub{snapshot: snapshot}
	server, err := New(statusAcceptor{}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody,
		WithScheduleExecution(stub, ids.CellID("cell-us-east-01")))
	if err != nil {
		t.Fatal(err)
	}

	load := scheduleExecutionTransportRequest(server.Handler(), "/internal/v1/schedules/executions:load", map[string]any{"claim": claim})
	if load.Code != http.StatusOK || stub.loadCalls != 1 || !bytes.Contains(load.Body.Bytes(), []byte(`"account_id":"21000000-0000-4000-8000-000000000001"`)) ||
		bytes.Contains(load.Body.Bytes(), []byte(`"ScheduleVersion"`)) {
		t.Fatalf("load=%d calls=%d body=%s", load.Code, stub.loadCalls, load.Body.String())
	}
	dispatch := scheduleExecutionTransportRequest(server.Handler(), "/internal/v1/schedules/executions:dispatch", map[string]any{"command": command})
	if dispatch.Code != http.StatusOK || stub.dispatchCalls != 1 || stub.command.OccurrenceID != command.OccurrenceID ||
		!bytes.Contains(dispatch.Body.Bytes(), []byte(`"reconciled":true`)) {
		t.Fatalf("dispatch=%d calls=%d command=%+v body=%s", dispatch.Code, stub.dispatchCalls, stub.command, dispatch.Body.String())
	}
}

func TestScheduleExecutionPrivateTransportRejectsWrongVerifiedWorkload(t *testing.T) {
	claim, snapshot, _ := scheduleExecutionTransportFixture(t)
	stub := &scheduleExecutionTransportStub{snapshot: snapshot}
	server, err := New(statusAcceptor{}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody,
		WithScheduleExecution(stub, ids.CellID("cell-us-east-01")))
	if err != nil {
		t.Fatal(err)
	}
	wrongIdentity := "spiffe://infiniteocean.net/spyglass/cells/cell-us-west-01/schedule-execution-worker"
	secured, err := workloadidentity.RequireClientIdentity(server.Handler(), []string{wrongIdentity}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"claim": claim})
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/schedules/executions:load", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	identityURI, _ := url.Parse(wrongIdentity)
	certificate := &x509.Certificate{URIs: []*url.URL{identityURI}}
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}, VerifiedChains: [][]*x509.Certificate{{certificate}}}
	response := httptest.NewRecorder()
	secured.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || stub.loadCalls != 0 || !bytes.Contains(response.Body.Bytes(), []byte(`"schedule_workload_scope_denied"`)) {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, stub.loadCalls, response.Body.String())
	}
}

func scheduleExecutionTransportRequest(handler http.Handler, path string, value any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(value)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func scheduleExecutionTransportFixture(t *testing.T) (schedulingapp.ExecutionClaim, schedulingapp.ExecutionSnapshot, schedulingapp.OccurrenceCommand) {
	t.Helper()
	scheduledFor := time.Date(2026, 8, 24, 13, 0, 0, 0, time.UTC)
	nextRunAt := scheduledFor.Add(24 * time.Hour)
	schedule, err := schedulingdomain.Restore(schedulingdomain.Schedule{
		ID: "11000000-0000-4000-8000-000000000001", AccountID: "21000000-0000-4000-8000-000000000001", Name: "Daily review", Timezone: "America/New_York",
		Recurrence: schedulingdomain.Recurrence{Frequency: schedulingdomain.FrequencyDaily, LocalHour: 9, GapPolicy: schedulingdomain.GapSkip, OverlapPolicy: schedulingdomain.OverlapFirst}, MissedRunPolicy: schedulingdomain.MissedSkip,
		Template: schedulingdomain.AgentRunTemplate{BoardroomID: "31000000-0000-4000-8000-000000000001", Mode: "selected", PersonaIDs: []ids.PersonaID{"41000000-0000-4000-8000-000000000001"}, Subject: "Daily review", Prompt: "Review priorities."},
		State:    schedulingdomain.StateActive, Version: 1, NextRunAt: &scheduledFor, CreatedBy: "51000000-0000-4000-8000-000000000001", CreatedAt: scheduledFor.Add(-24 * time.Hour), UpdatedAt: scheduledFor.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	claim := schedulingapp.ExecutionClaim{AccountID: schedule.AccountID, ScheduleID: schedule.ID, ScheduleVersion: schedule.Version,
		ScheduledFor: scheduledFor, LeaseID: "61000000-0000-4000-8000-000000000001", Attempt: 1}
	command := schedulingapp.OccurrenceCommand{Claim: claim, Schedule: schedule, OccurrenceID: "71000000-0000-4000-8000-000000000001",
		RunID: "81000000-0000-4000-8000-000000000001", ConversationID: "91000000-0000-4000-8000-000000000001", UserMessageID: "a1000000-0000-4000-8000-000000000001",
		Subject: "Daily review — 2026-08-24", Authorization: schedulingapp.ExecutionAuthorization{EntitlementVersion: 3, MaximumConcurrentRun: 2, CanReadRestricted: true},
		NextRunAt: nextRunAt, At: scheduledFor.Add(time.Minute), RequestExpiresAt: scheduledFor.Add(time.Hour)}
	if !command.Valid() {
		t.Fatal("invalid Schedule execution transport fixture")
	}
	return claim, schedulingapp.ExecutionSnapshot{Schedule: schedule}, command
}
