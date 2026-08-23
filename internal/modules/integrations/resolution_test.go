package integrations

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

func TestExecutionResolutionRequiresDistinctConfirmerAndContentFreeEvidence(t *testing.T) {
	now := time.Date(2026, 8, 23, 23, 45, 0, 0, time.UTC)
	value := ExecutionResolution{ID: "9a100000-0000-4000-8000-000000000001", AccountID: "9a200000-0000-4000-8000-000000000002",
		ExecutionID: "9a300000-0000-4000-8000-000000000003", RequestedOutcome: ExecutionSucceeded,
		EvidenceSHA256: sha256.Sum256([]byte("provider receipt 123")), RequestedByUserID: "9a400000-0000-4000-8000-000000000004",
		RequestedAt: now, State: ResolutionPending}
	if _, err := RestoreExecutionResolution(value); err != nil {
		t.Fatal(err)
	}
	confirmed := now.Add(time.Minute)
	value.State, value.ConfirmedByUserID, value.ConfirmedAt = ResolutionApplied, value.RequestedByUserID, &confirmed
	if _, err := RestoreExecutionResolution(value); !errors.Is(err, ErrInvalid) {
		t.Fatalf("same-actor confirmation=%v", err)
	}
	value.ConfirmedByUserID = "9a500000-0000-4000-8000-000000000005"
	if _, err := RestoreExecutionResolution(value); err != nil {
		t.Fatal(err)
	}
}
