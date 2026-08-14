package boardroom

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseResearchEventsShowsSearchesAndBoundedResults(t *testing.T) {
	events := json.RawMessage(`[
		{"event_type":"tool.requested","payload":{"id":"docs-1","name":"documents.search","arguments":{"query":"technician licensing"}}},
		{"event_type":"tool.completed","payload":{"request_id":"docs-1","name":"documents.search","result":{"results":[{"document_name":"License checklist.pdf","excerpt":"Verify the technician's active state license before dispatch."}]}}},
		{"event_type":"tool.requested","payload":{"id":"web-1","name":"web.search","arguments":{"query":"North Carolina HVAC licensing requirements"}}},
		{"event_type":"tool.completed","payload":{"request_id":"web-1","name":"web.search","result":{"query":"North Carolina HVAC licensing requirements","results":[{"title":"State Board guidance","url":"https://agency.example/licenses","description":"Official licensing guidance."}]}}},
		{"event_type":"tool.requested","payload":{"id":"read-1","name":"web.read","arguments":{"url":"https://agency.example/licenses"}}},
		{"event_type":"tool.completed","payload":{"request_id":"read-1","name":"web.read","result":{"results":[{"title":"State Board guidance","url":"https://agency.example/licenses","content":"` + strings.Repeat("evidence ", 100) + `"}]}}}
	]`)

	activities := parseResearchEvents(events)
	if len(activities) != 3 {
		t.Fatalf("got %d activities, want 3: %#v", len(activities), activities)
	}
	if activities[0].Query != "technician licensing" || activities[0].Results[0].Title != "License checklist.pdf" {
		t.Fatalf("document search was not made visible: %#v", activities[0])
	}
	if activities[1].Query != "North Carolina HVAC licensing requirements" || activities[1].Results[0].URL != "https://agency.example/licenses" {
		t.Fatalf("web search was not made visible: %#v", activities[1])
	}
	if len(activities[2].Results[0].Excerpt) > 283 || !strings.HasSuffix(activities[2].Results[0].Excerpt, "…") {
		t.Fatalf("read content was not bounded: %q", activities[2].Results[0].Excerpt)
	}
}

func TestParseResearchEventsShowsEmptyAndFailedSearches(t *testing.T) {
	events := json.RawMessage(`[
		{"event_type":"tool.requested","payload":{"id":"empty","name":"web.search","arguments":{"query":"obscure rule"}}},
		{"event_type":"tool.completed","payload":{"request_id":"empty","name":"web.search","result":{"results":[]}}},
		{"event_type":"tool.requested","payload":{"id":"failed","name":"documents.search","arguments":{"query":"internal policy"}}},
		{"event_type":"tool.failed","payload":{"request_id":"failed","name":"documents.search","error":"provider unavailable"}}
	]`)

	activities := parseResearchEvents(events)
	if len(activities) != 2 || activities[0].Status != "completed" || len(activities[0].Results) != 0 {
		t.Fatalf("empty search was not retained: %#v", activities)
	}
	if activities[1].Status != "failed" || activities[1].Query != "internal policy" {
		t.Fatalf("failed search was not associated with its query: %#v", activities)
	}
}

func TestParseResearchEventsIgnoresNonResearchTools(t *testing.T) {
	events := json.RawMessage(`[{"event_type":"tool.requested","payload":{"id":"ticket","name":"tickets.create","arguments":{"title":"Private action"}}}]`)
	if activities := parseResearchEvents(events); len(activities) != 0 {
		t.Fatalf("non-research tools leaked into research trace: %#v", activities)
	}
}
