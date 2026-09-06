// Package scheduleaction bridges agent proposals to approved, idempotent schedules.
// Preparation has no side effects; only the approval worker can create a schedule.
package scheduleaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"io"
	"time"
)

const PrepareCapability = "schedules.prepare"
const CreateCapability = "schedules.create"

var ErrInvalid = errors.New("invalid daily schedule proposal")

type Input struct {
	Name        string          `json:"name"`
	Timezone    string          `json:"timezone"`
	LocalHour   int             `json:"local_hour"`
	LocalMinute int             `json:"local_minute"`
	BoardroomID ids.BoardroomID `json:"boardroom_id"`
	PersonaIDs  []ids.PersonaID `json:"persona_ids"`
	Subject     string          `json:"subject"`
	Prompt      string          `json:"prompt"`
	SourceURLs  []string        `json:"source_urls"`
	EmailSelf   bool            `json:"email_self"`
	RunNow      bool            `json:"run_now"`
}

func (input Input) Schedule(accountID ids.AccountID, operationID string, userID ids.UserID, now time.Time) (domain.Schedule, error) {
	return domain.New(domain.Draft{ID: ids.ScheduleID(operationID), AccountID: accountID, Name: input.Name, Timezone: input.Timezone,
		Recurrence:      domain.Recurrence{Frequency: domain.FrequencyDaily, LocalHour: input.LocalHour, LocalMinute: input.LocalMinute, GapPolicy: domain.GapNextValid, OverlapPolicy: domain.OverlapFirst},
		MissedRunPolicy: domain.MissedSkip, CreatedBy: userID, CreatedAt: now,
		Template: domain.AgentRunTemplate{BoardroomID: input.BoardroomID, Mode: "selected", PersonaIDs: input.PersonaIDs, Subject: input.Subject, Prompt: input.Prompt,
			SourceURLs: input.SourceURLs, EmailSelf: input.EmailSelf, WorkItemIDs: []ids.WorkItemID{}, KnowledgeFactIDs: []ids.KnowledgeFactID{}, KnowledgeDocumentIDs: []ids.KnowledgeDocumentID{}, BaselineAssessmentIDs: []ids.BaselineAssessmentID{}}})
}
func Decode(raw json.RawMessage) (Input, error) {
	var input Input
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return input, ErrInvalid
	}
	// Domain validation uses placeholder identity only during side-effect-free preparation.
	const id = "00000000-0000-4000-8000-000000000001"
	if _, err := input.Schedule(ids.AccountID(id), id, ids.UserID(id), time.Now().UTC()); err != nil {
		return Input{}, ErrInvalid
	}
	return input, nil
}

type PrepareHandler struct{}

func (PrepareHandler) Execute(ctx context.Context, call runnercapability.AuthorizedCall) (json.RawMessage, error) {
	if call.Grant.Capability != PrepareCapability {
		return nil, runnercapability.NewExecutionFailure("schedule_input_invalid")
	}
	input, err := Decode(call.Input)
	if err != nil {
		return nil, runnercapability.NewExecutionFailure("schedule_input_invalid")
	}
	return json.Marshal(map[string]any{"status": "prepared_for_approval", "created": false,
		"instruction": "Include this proposal in proposed_actions. Approval creates the schedule and permits delivery to the approving user's verified email when email_self is true. Nothing has been scheduled or sent yet.",
		"proposal":    map[string]any{"kind": CreateCapability, "reason": "Create the requested daily report. Email delivery, if enabled, goes to the approving user.", "payload": input, "evidence": []string{}}})
}

type Store interface {
	CreateApprovedSchedule(context.Context, domain.Schedule, bool, string) error
	ApprovedScheduleExists(context.Context, ids.AccountID, ids.ScheduleID, Input, ids.UserID) (bool, error)
}
type Handler struct {
	Store Store
	Now   func() time.Time
}

func (h Handler) valid(call runnercapability.AuthorizedCall) bool {
	return h.Store != nil && h.Now != nil && call.Action != nil && call.Action.Capability == CreateCapability && call.Grant.Capability == CreateCapability &&
		call.Action.AccountID == call.Grant.AccountID && call.Action.OperationID == call.OperationID && call.Action.IdempotencyKey == call.OperationID &&
		ids.Validate(string(call.ApprovedByUserID)) == nil
}
func (h Handler) Execute(ctx context.Context, call runnercapability.AuthorizedCall) (json.RawMessage, error) {
	input, err := Decode(call.Input)
	if err != nil || !h.valid(call) {
		return nil, failure{true}
	}
	value, err := input.Schedule(call.Grant.AccountID, call.OperationID, call.ApprovedByUserID, h.Now().UTC())
	if err != nil {
		return nil, failure{true}
	}
	if err = h.Store.CreateApprovedSchedule(ctx, value, input.RunNow, call.OperationID); err != nil {
		return nil, failure{false}
	}
	return output(value.ID, input.RunNow), nil
}
func (h Handler) Reconcile(ctx context.Context, call runnercapability.AuthorizedCall) (json.RawMessage, runnercapability.ActionOutcome, error) {
	input, err := Decode(call.Input)
	if err != nil || !h.valid(call) {
		return nil, runnercapability.ActionFailed, failure{true}
	}
	found, err := h.Store.ApprovedScheduleExists(ctx, call.Grant.AccountID, ids.ScheduleID(call.OperationID), input, call.ApprovedByUserID)
	if err != nil {
		return nil, runnercapability.ActionUnknown, failure{false}
	}
	if !found {
		return nil, runnercapability.ActionFailed, failure{true}
	}
	return output(ids.ScheduleID(call.OperationID), input.RunNow), runnercapability.ActionSucceeded, nil
}
func output(id ids.ScheduleID, runNow bool) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"schedule_id": id, "created": true, "test_run_queued": runNow})
	return raw
}

type failure struct{ definitive bool }

func (f failure) Error() string { return f.Code() }
func (f failure) Code() string {
	if f.definitive {
		return "schedule_input_rejected"
	}
	return "schedule_creation_unknown"
}
func (f failure) Definitive() bool { return f.definitive }
