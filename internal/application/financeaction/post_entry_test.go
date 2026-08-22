package financeaction

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type storeStub struct {
	postErr   error
	posted    bool
	queryErr  error
	postCall  int
	queryCall int
}

func (stub *storeStub) PostEntry(context.Context, ids.AccountID, ids.FinanceEntryID, uint64, string, ids.UserID) error {
	stub.postCall++
	return stub.postErr
}
func (stub *storeStub) EntryPostedByEvent(context.Context, ids.AccountID, ids.FinanceEntryID, uint64, string) (bool, error) {
	stub.queryCall++
	return stub.posted, stub.queryErr
}

func TestEntryPostExecutesOnlyApprovedExactInput(t *testing.T) {
	store := &storeStub{}
	handler, _ := NewEntryPostHandler(store)
	output, err := handler.Execute(context.Background(), approvedCall())
	if err != nil || store.postCall != 1 || string(output) != `{"entry_id":"51000000-0000-4000-8000-000000000001","state":"posted"}` {
		t.Fatalf("output=%s calls=%d err=%v", output, store.postCall, err)
	}
	call := approvedCall()
	call.ApprovedByUserID = ""
	if _, err := handler.Execute(context.Background(), call); err == nil || store.postCall != 1 {
		t.Fatalf("missing approval err=%v calls=%d", err, store.postCall)
	}
}

func TestEntryPostReconciliationNeverMutates(t *testing.T) {
	store := &storeStub{posted: true}
	handler, _ := NewEntryPostHandler(store)
	_, outcome, err := handler.Reconcile(context.Background(), approvedCall())
	if err != nil || outcome != runnercapability.ActionSucceeded || store.postCall != 0 || store.queryCall != 1 {
		t.Fatalf("outcome=%s post=%d query=%d err=%v", outcome, store.postCall, store.queryCall, err)
	}
	store.posted = false
	_, outcome, err = handler.Reconcile(context.Background(), approvedCall())
	var definitive interface{ Definitive() bool }
	if outcome != runnercapability.ActionFailed || !errors.As(err, &definitive) || !definitive.Definitive() || store.postCall != 0 {
		t.Fatalf("outcome=%s post=%d err=%v", outcome, store.postCall, err)
	}
}

func approvedCall() runnercapability.AuthorizedCall {
	payload := json.RawMessage(`{"entry_id":"51000000-0000-4000-8000-000000000001","expected_version":1}`)
	digest := sha256.Sum256(payload)
	lease := runnercapability.ActionLease{AccountID: "11000000-0000-4000-8000-000000000001",
		InvocationID: "21000000-0000-4000-8000-000000000001", OperationID: "31000000-0000-4000-8000-000000000001",
		AttemptID: "41000000-0000-4000-8000-000000000001", Capability: EntryPostCapability, InputDigest: digest,
		Mode: runnercapability.ActionExecute, IdempotencyKey: "31000000-0000-4000-8000-000000000001", LeaseExpiresAt: time.Now().Add(time.Minute)}
	return runnercapability.AuthorizedCall{Grant: runnerbroker.CapabilityGrant{AccountID: lease.AccountID}, OperationID: lease.OperationID,
		Input: payload, InputDigest: digest, ApprovedByUserID: "61000000-0000-4000-8000-000000000001", Action: &lease}
}
