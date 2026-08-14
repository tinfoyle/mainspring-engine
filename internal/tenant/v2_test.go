package tenant

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
	toolbroker "github.com/tinfoyle/mainspring-engine/internal/tools"
)

func TestRequestWantsJSON(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v2/work", nil)
	request.Header.Set("Accept", "text/html, application/json")
	if !requestWantsJSON(request) {
		t.Fatal("expected JSON response negotiation")
	}
}

func TestRequestWantsV2PageNegotiatesBrowserAndAutomation(t *testing.T) {
	tests := []struct {
		name    string
		request *http.Request
		want    bool
	}{
		{"browser navigation", func() *http.Request {
			r := httptest.NewRequest("GET", "/work", nil)
			r.Header.Set("Sec-Fetch-Dest", "document")
			return r
		}(), true},
		{"mozilla fallback", func() *http.Request {
			r := httptest.NewRequest("GET", "/work", nil)
			r.Header.Set("User-Agent", "Mozilla/5.0")
			return r
		}(), true},
		{"curl smoke contract", httptest.NewRequest("GET", "/work", nil), false},
		{"explicit legacy", func() *http.Request {
			r := httptest.NewRequest("GET", "/work?legacy=1", nil)
			r.Header.Set("Sec-Fetch-Dest", "document")
			return r
		}(), false},
		{"explicit v2", httptest.NewRequest("GET", "/work?v2=1", nil), true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := requestWantsV2Page(test.request); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestApprovalViewsExposeProposedTicketTitleBeforeCreation(t *testing.T) {
	views := approvalViews([]toolbroker.Approval{{
		ID:             "approval-1",
		RunID:          domain.NewRunID(),
		ActionType:     toolbroker.TicketCreateAction,
		RequestPayload: []byte(`{"title":"Prepare the launch guardrails","description":"Define safe operating limits.","priority":"high","origin":"direct_request","search_query":""}`),
	}})
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	if views[0].WorkItemTitle != "Prepare the launch guardrails" {
		t.Fatalf("work item title = %q", views[0].WorkItemTitle)
	}
}
