package marketingaction

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
	activateErr error
	activated   bool
	queryErr    error
	activations int
	queries     int
}

func (stub *storeStub) ActivateRelease(context.Context, ids.AccountID, ids.MarketingCampaignID, uint64, ids.MarketingReleaseID, uint64, string, ids.UserID) error {
	stub.activations++
	return stub.activateErr
}

func (stub *storeStub) ReleaseActivatedByEvent(context.Context, ids.AccountID, ids.MarketingCampaignID, uint64, ids.MarketingReleaseID, uint64, string) (bool, error) {
	stub.queries++
	return stub.activated, stub.queryErr
}

func TestReleaseActivationExecutesOnlyApprovedExactInput(t *testing.T) {
	store := &storeStub{}
	handler, _ := NewReleaseActivateHandler(store)
	output, err := handler.Execute(context.Background(), approvedCall())
	if err != nil || store.activations != 1 || string(output) != `{"campaign_id":"51000000-0000-4000-8000-000000000001","release_id":"52000000-0000-4000-8000-000000000002","state":"active"}` {
		t.Fatalf("output=%s calls=%d err=%v", output, store.activations, err)
	}
	call := approvedCall()
	call.ApprovedByUserID = ""
	if _, err := handler.Execute(context.Background(), call); err == nil || store.activations != 1 {
		t.Fatalf("missing approval err=%v calls=%d", err, store.activations)
	}
	call = approvedCall()
	call.Input = json.RawMessage(`{"campaign_id":"51000000-0000-4000-8000-000000000001","campaign_version":1,"release_id":"52000000-0000-4000-8000-000000000002","release_version":2,"approval_id":"61000000-0000-4000-8000-000000000001"}`)
	if _, err := handler.Execute(context.Background(), call); err == nil || store.activations != 1 {
		t.Fatalf("caller-supplied approval binding err=%v calls=%d", err, store.activations)
	}
}

func TestReleaseActivationReconciliationNeverMutates(t *testing.T) {
	store := &storeStub{activated: true}
	handler, _ := NewReleaseActivateHandler(store)
	_, outcome, err := handler.Reconcile(context.Background(), approvedCall())
	if err != nil || outcome != runnercapability.ActionSucceeded || store.activations != 0 || store.queries != 1 {
		t.Fatalf("outcome=%s activations=%d queries=%d err=%v", outcome, store.activations, store.queries, err)
	}
	store.activated = false
	_, outcome, err = handler.Reconcile(context.Background(), approvedCall())
	var definitive interface{ Definitive() bool }
	if outcome != runnercapability.ActionFailed || !errors.As(err, &definitive) || !definitive.Definitive() || store.activations != 0 {
		t.Fatalf("outcome=%s activations=%d err=%v", outcome, store.activations, err)
	}
}

func approvedCall() runnercapability.AuthorizedCall {
	payload := json.RawMessage(`{"campaign_id":"51000000-0000-4000-8000-000000000001","campaign_version":1,"release_id":"52000000-0000-4000-8000-000000000002","release_version":2}`)
	digest := sha256.Sum256(payload)
	lease := runnercapability.ActionLease{AccountID: "11000000-0000-4000-8000-000000000001",
		InvocationID: "21000000-0000-4000-8000-000000000001", OperationID: "31000000-0000-4000-8000-000000000001",
		AttemptID: "41000000-0000-4000-8000-000000000001", Capability: ReleaseActivateCapability, InputDigest: digest,
		Mode: runnercapability.ActionExecute, IdempotencyKey: "31000000-0000-4000-8000-000000000001", LeaseExpiresAt: time.Now().Add(time.Minute)}
	return runnercapability.AuthorizedCall{Grant: runnerbroker.CapabilityGrant{AccountID: lease.AccountID}, OperationID: lease.OperationID,
		Input: payload, InputDigest: digest, ApprovedByUserID: "61000000-0000-4000-8000-000000000001", Action: &lease}
}
