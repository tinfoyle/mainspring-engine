package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDecodeUnknownAnswerTicketRequiresActionablePlan(t *testing.T) {
	valid := json.RawMessage(`{"title":"Determine compliance","description":"Work plan:\n1. Gather evidence.\n\nDefinition of done: Evidence and conclusion recorded.","priority":"high","origin":"unknown_answer","search_query":"legal compliance"}`)
	payload, err := DecodeTicketCreatePayload(valid)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Origin != "unknown_answer" || payload.Priority != "high" {
		t.Fatalf("decoded payload = %#v", payload)
	}

	for name, raw := range map[string]json.RawMessage{
		"missing search": json.RawMessage(`{"title":"Determine compliance","description":"Work plan:\n1. Gather evidence.\n\nDefinition of done: Recorded.","priority":"high","origin":"unknown_answer","search_query":""}`),
		"missing plan":   json.RawMessage(`{"title":"Determine compliance","description":"Investigate it.","priority":"high","origin":"unknown_answer","search_query":"legal compliance"}`),
		"bad priority":   json.RawMessage(`{"title":"Determine compliance","description":"Work plan:\n1. Gather evidence.\n\nDefinition of done: Recorded.","priority":"critical","origin":"unknown_answer","search_query":"legal compliance"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeTicketCreatePayload(raw); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDecodeTicketAcceptsValidParentAndRejectsInvalidParent(t *testing.T) {
	parentID := uuid.NewString()
	valid := json.RawMessage(`{"title":"Collect evidence","description":"Gather the records.","priority":"normal","origin":"direct_request","search_query":"","parent_work_item_id":"` + parentID + `"}`)
	payload, err := DecodeTicketCreatePayload(valid)
	if err != nil {
		t.Fatal(err)
	}
	if payload.ParentWorkItemID != parentID {
		t.Fatalf("parent = %q", payload.ParentWorkItemID)
	}
	invalid := json.RawMessage(`{"title":"Collect evidence","description":"Gather the records.","priority":"normal","origin":"direct_request","search_query":"","parent_work_item_id":"not-a-ticket"}`)
	if _, err := DecodeTicketCreatePayload(invalid); err == nil {
		t.Fatal("expected invalid parent error")
	}
}

func TestDecodeTicketRejectsOversizedTitle(t *testing.T) {
	payload := map[string]any{"title": strings.Repeat("x", 241), "description": "Work plan:\n1. Research.\nDefinition of done: recorded.", "priority": "normal", "origin": "unknown_answer", "search_query": "question"}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeTicketCreatePayload(raw); err == nil {
		t.Fatal("expected oversized title error")
	}
}
