package tools

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestDecodeWorkReviewPayload(t *testing.T) {
	payload, _ := json.Marshal(WorkReviewPayload{
		WorkItemID: uuid.NewString(), WorkItemNumber: 42, Title: "Document access controls", Summary: "Drafted and cited the control record.",
	})
	decoded, err := DecodeWorkReviewPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.WorkItemNumber != 42 || decoded.Summary == "" {
		t.Fatalf("decoded review = %#v", decoded)
	}
}

func TestDecodeWorkReviewPayloadRejectsIncompleteReview(t *testing.T) {
	if _, err := DecodeWorkReviewPayload(json.RawMessage(`{"work_item_id":"not-a-uuid"}`)); err == nil {
		t.Fatal("expected invalid work review payload to fail")
	}
}
