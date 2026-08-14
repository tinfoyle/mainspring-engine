package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestMessageBubbleIncludesStableMessageIdentity(t *testing.T) {
	var output bytes.Buffer
	err := MessageBubble(MessageView{ID: "message-123", Role: "agent", PersonaName: "Jordan", PersonaRole: "Security & Compliance Advisor", Body: "Assessment", CreatedAt: time.Now()}).Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `data-message-id="message-123"`) {
		t.Fatalf("message bubble did not expose a stable identity: %s", output.String())
	}
}

func TestMessageBubbleShowsResearchQueryAndResults(t *testing.T) {
	var output bytes.Buffer
	err := MessageBubble(MessageView{
		ID: "message-123", Role: "agent", PersonaName: "Jordan", PersonaRole: "Security & Compliance Advisor",
		Body: "Here is what I found.", CreatedAt: time.Now(),
		Research: []ResearchActivityView{{
			Tool: "web.search", Query: "North Carolina HVAC licensing", Status: "completed",
			Results: []ResearchResultView{{Title: "State Board licensing guidance", URL: "https://agency.example/licenses", Excerpt: "Official requirements."}},
		}},
	}).Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"Research activity", "Searched the web", "North Carolina HVAC licensing", "State Board licensing guidance", "Official requirements."} {
		if !strings.Contains(html, expected) {
			t.Fatalf("research trace missing %q: %s", expected, html)
		}
	}
	if !strings.Contains(html, `target="_blank"`) || !strings.Contains(html, `rel="noopener noreferrer"`) {
		t.Fatalf("external research link is not safely isolated: %s", html)
	}
}

func TestMessageBubbleDoesNotLinkUnsafeResearchURL(t *testing.T) {
	var output bytes.Buffer
	err := MessageBubble(MessageView{
		ID: "message-123", Role: "agent", PersonaName: "Jordan", Body: "Result", CreatedAt: time.Now(),
		Research: []ResearchActivityView{{Tool: "web.search", Query: "test", Status: "completed", Results: []ResearchResultView{{Title: "Unsafe", URL: "javascript:alert(1)"}}}},
	}).Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "javascript:") {
		t.Fatalf("unsafe research URL was rendered: %s", output.String())
	}
}

func TestFailedRunFollowUpDoesNotClaimBoardroomIsReady(t *testing.T) {
	var output bytes.Buffer
	if err := FollowUpForm("conversation-id", nil, "csrf", "failed").Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	if strings.Contains(html, "boardroom is ready") {
		t.Fatalf("failed run rendered the success message: %s", html)
	}
	if !strings.Contains(html, "could not complete this run") || !strings.Contains(html, "Retry conversation") {
		t.Fatalf("failed run did not render recovery guidance: %s", html)
	}
}

func TestApprovalOfferRendersWorkItemDecision(t *testing.T) {
	var output bytes.Buffer
	err := ApprovalOffers([]ApprovalView{{
		ID: "approval-id", ActionType: "tickets.create", Reason: "Track the research gap", PersonaName: "Main Manager",
		Evidence: []string{"Document search found no sufficient answer."}, RequestPayload: `{"title":"Determine compliance"}`,
	}}, "csrf").Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"Proposed next step", "Create work item", "Approve and create work item", "Document search found no sufficient answer"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("approval offer missing %q: %s", expected, html)
		}
	}
}

func TestWorkReviewUsesCompletionDecisionLabels(t *testing.T) {
	if got := ActionTypeLabel("work.review"); got != "Review agent work" {
		t.Fatalf("ActionTypeLabel(work.review) = %q", got)
	}
	if got := ApprovalButtonLabel("work.review"); got != "Accept and complete ticket" {
		t.Fatalf("ApprovalButtonLabel(work.review) = %q", got)
	}
	if got := ApprovalRejectLabel("work.review"); got != "Needs more work" {
		t.Fatalf("ApprovalRejectLabel(work.review) = %q", got)
	}
}
