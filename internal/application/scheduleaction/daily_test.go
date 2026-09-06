package scheduleaction

import (
	"context"
	"encoding/json"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"testing"
	"time"
)

const testID = "10000000-0000-4000-8000-000000000001"

func testInput() Input {
	return Input{Name: "Daily report", Timezone: "America/New_York", LocalHour: 8, BoardroomID: ids.BoardroomID(testID), PersonaIDs: []ids.PersonaID{ids.PersonaID(testID)}, Subject: "Daily report", Prompt: "Summarize today's prices from the supplied sources. Do not create schedules.", SourceURLs: []string{"https://example.com/prices"}, EmailSelf: true, RunNow: true}
}

type storeStub struct {
	calls  int
	value  domain.Schedule
	runNow bool
}

func (s *storeStub) CreateApprovedSchedule(_ context.Context, v domain.Schedule, run bool, _ string) error {
	s.calls++
	s.value = v
	s.runNow = run
	return nil
}
func (s *storeStub) ApprovedScheduleExists(context.Context, ids.AccountID, ids.ScheduleID, Input, ids.UserID) (bool, error) {
	return s.calls > 0, nil
}
func TestPrepareDoesNotExecuteAndApprovalBindsRecipient(t *testing.T) {
	raw, _ := json.Marshal(testInput())
	result, err := (PrepareHandler{}).Execute(context.Background(), runnercapability.AuthorizedCall{Grant: runnerbroker.CapabilityGrant{Capability: PrepareCapability}, Input: raw})
	if err != nil || !json.Valid(result) {
		t.Fatal(err)
	}
	store := &storeStub{}
	h := Handler{Store: store, Now: time.Now}
	call := runnercapability.AuthorizedCall{Grant: runnerbroker.CapabilityGrant{Capability: CreateCapability, AccountID: ids.AccountID(testID)}, OperationID: testID, Input: raw}
	if _, err := h.Execute(context.Background(), call); err == nil || store.calls != 0 {
		t.Fatal("unapproved call executed")
	}
	call.ApprovedByUserID = ids.UserID("20000000-0000-4000-8000-000000000002")
	call.Action = &runnercapability.ActionLease{AccountID: ids.AccountID(testID), OperationID: testID, Capability: CreateCapability, IdempotencyKey: testID}
	if _, err := h.Execute(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	if store.value.CreatedBy != call.ApprovedByUserID || !store.runNow || !store.value.Template.EmailSelf {
		t.Fatal("recipient or immediate run lost")
	}
	if _, state, err := h.Reconcile(context.Background(), call); err != nil || state != runnercapability.ActionSucceeded || store.calls != 1 {
		t.Fatal("reconcile repeated the mutation")
	}
}
func TestPrepareRejectsUnknownRecipientAndInvalidSource(t *testing.T) {
	raw, _ := json.Marshal(testInput())
	var values map[string]any
	_ = json.Unmarshal(raw, &values)
	values["recipient"] = "someone@example.com"
	raw, _ = json.Marshal(values)
	if _, err := Decode(raw); err == nil {
		t.Fatal("accepted arbitrary recipient")
	}
	delete(values, "recipient")
	values["source_urls"] = []string{"http://localhost/private"}
	raw, _ = json.Marshal(values)
	if _, err := Decode(raw); err == nil {
		t.Fatal("accepted invalid URL")
	}
}
