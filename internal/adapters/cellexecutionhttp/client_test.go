package cellexecutionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	schedulingapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	schedulingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestClientLoadsAndDispatchesPrivateOccurrenceContracts(t *testing.T) {
	claim, snapshot, command := executionHTTPFixture(t)
	paths := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/internal/v1/schedules/executions:load":
			var request loadRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.Claim != claim {
				t.Errorf("claim=%+v", request.Claim)
			}
			_ = json.NewEncoder(w).Encode(loadResponse{Snapshot: snapshot})
		case "/internal/v1/schedules/executions:dispatch":
			var request commandRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.Command.OccurrenceID != command.OccurrenceID {
				t.Errorf("command=%+v", request.Command)
			}
			_ = json.NewEncoder(w).Encode(commandResponse{Reconciled: true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := client.Load(context.Background(), claim)
	if err != nil || loaded.Schedule.ID != snapshot.Schedule.ID {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	reconciled, err := client.Dispatch(context.Background(), command)
	if err != nil || !reconciled || len(paths) != 2 {
		t.Fatalf("reconciled=%v paths=%v err=%v", reconciled, paths, err)
	}
}

func TestClientClassifiesBoundedPrivateFailures(t *testing.T) {
	for _, test := range []struct {
		code string
		want error
	}{
		{"schedule_execution_lease_lost", schedulingapp.ErrExecutionLeaseLost},
		{"schedule_execution_conflict", schedulingapp.ErrExecutionConflict},
		{"agent_run_capacity", schedulingapp.ErrExecutionCapacity},
		{"schedule_workload_scope_denied", schedulingapp.ErrExecutionAuthorization},
		{"unknown", schedulingapp.ErrExecutionServiceUnavailable},
	} {
		body, _ := json.Marshal(problemResponse{Code: test.code})
		if err := classify(http.StatusConflict, body); !errors.Is(err, test.want) {
			t.Fatalf("code=%s err=%v want=%v", test.code, err, test.want)
		}
	}
}

func executionHTTPFixture(t *testing.T) (schedulingapp.ExecutionClaim, schedulingapp.ExecutionSnapshot, schedulingapp.OccurrenceCommand) {
	t.Helper()
	scheduledFor := time.Date(2026, 8, 24, 13, 0, 0, 0, time.UTC)
	schedule, err := schedulingdomain.Restore(schedulingdomain.Schedule{
		ID: "11000000-0000-4000-8000-000000000001", AccountID: "21000000-0000-4000-8000-000000000001", Name: "Daily review", Timezone: "America/New_York",
		Recurrence: schedulingdomain.Recurrence{Frequency: schedulingdomain.FrequencyDaily, LocalHour: 9, GapPolicy: schedulingdomain.GapSkip, OverlapPolicy: schedulingdomain.OverlapFirst}, MissedRunPolicy: schedulingdomain.MissedSkip,
		Template: schedulingdomain.AgentRunTemplate{BoardroomID: "31000000-0000-4000-8000-000000000001", Mode: "selected", PersonaIDs: []ids.PersonaID{"41000000-0000-4000-8000-000000000001"}, Subject: "Daily review", Prompt: "Review priorities."},
		State:    schedulingdomain.StateActive, Version: 1, NextRunAt: &scheduledFor, CreatedBy: "51000000-0000-4000-8000-000000000001", CreatedAt: scheduledFor.Add(-24 * time.Hour), UpdatedAt: scheduledFor.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	claim := schedulingapp.ExecutionClaim{AccountID: schedule.AccountID, ScheduleID: schedule.ID, ScheduleVersion: 1,
		ScheduledFor: scheduledFor, LeaseID: "61000000-0000-4000-8000-000000000001", Attempt: 1}
	command := schedulingapp.OccurrenceCommand{Claim: claim, Schedule: schedule, OccurrenceID: "71000000-0000-4000-8000-000000000001",
		RunID: "81000000-0000-4000-8000-000000000001", ConversationID: "91000000-0000-4000-8000-000000000001", UserMessageID: "a1000000-0000-4000-8000-000000000001",
		Subject: "Daily review — 2026-08-24", Authorization: schedulingapp.ExecutionAuthorization{EntitlementVersion: 3, MaximumConcurrentRun: 2},
		NextRunAt: scheduledFor.Add(24 * time.Hour), At: scheduledFor.Add(time.Minute), RequestExpiresAt: scheduledFor.Add(time.Hour)}
	return claim, schedulingapp.ExecutionSnapshot{Schedule: schedule}, command
}
