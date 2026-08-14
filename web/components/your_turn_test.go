package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestYourTurnInputRendersConsolidatedCoordinatorConversation(t *testing.T) {
	var output bytes.Buffer
	err := YourTurnPage("Example Co", UserView{DisplayName: "Owner", Role: "owner"}, "input", YourTurnCountsView{Inputs: 1}, []HumanInputRequestView{{
		ID: "request-1", ParentWorkItemID: "parent-1", ParentNumber: 9, ParentTitle: "Document access controls",
		PersonaName: "Jordan", Questions: []string{"Who owns security?", "Where are recovery codes stored?"}, Status: "pending", RequestedAt: time.Now(),
	}}, InputCoordinatorView{
		PendingQuestions: 2, PendingRequests: 1, RemainingTopics: 1, KnownFacts: 7,
		Messages: []InputCoordinatorMessageView{{Role: "agent", Body: "I consolidated the open questions.", CreatedAt: time.Now()}},
		Current: &InputCoordinatorQuestionView{FactKey: "security.access_owner", Label: "Security ownership", Prompt: "Who owns security?", TicketCount: 2, QuestionCount: 2,
			Tickets: []InputCoordinatorTicketView{{ID: "parent-1", Number: 9, Title: "Document access controls"}, {ID: "parent-2", Number: 12, Title: "Production release process"}}},
	}, nil, nil, "csrf", "", "", false).Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, ticket := range []string{"Document access controls", "Production release process"} {
		if !strings.Contains(html, ticket) {
			t.Fatalf("coordinated answer impact missing ticket %q: %s", ticket, html)
		}
	}
	for _, expected := range []string{"Your turn", "Talk with Mia", "I consolidated the open questions.", "Who owns security?", "Where are recovery codes stored?", "Answer and continue", `data-coordinator-stat="questions">2`, `data-coordinator-stat="sidebar-topics">1`} {
		if !strings.Contains(html, expected) {
			t.Fatalf("input request missing %q: %s", expected, html)
		}
	}
	if strings.Count(html, `<form class="coordinator-compose"`) != 1 {
		t.Fatalf("expected one coordinator answer form: %s", html)
	}
	for _, expected := range []string{`data-coordinator-form`, `data-message-count="1"`, `data-current-fact-key`, `data-current-label`, `data-current-question`, `data-current-prompt`, `data-current-impact`} {
		if !strings.Contains(html, expected) {
			t.Fatalf("coordinator form missing asynchronous update contract %q: %s", expected, html)
		}
	}
	for _, forbidden := range []string{"autofocus", "hx-post", "hx-target", "hx-select"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("coordinator form must not refresh or move the page via %q: %s", forbidden, html)
		}
	}
}

func TestYourTurnCaughtUpKeepsDormantComposerForLiveQuestions(t *testing.T) {
	var output bytes.Buffer
	err := InputCoordinator(InputCoordinatorView{}, nil, nil, "csrf").Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{`data-coordinator-form`, `coordinator-compose is-hidden`, `data-coordinator-complete`, `Mia is caught up.`} {
		if !strings.Contains(html, expected) {
			t.Fatalf("caught-up coordinator missing live-update hook %q: %s", expected, html)
		}
	}
}

func TestYourTurnSeparatesReviewsFromConsequentialApprovals(t *testing.T) {
	if ActionTypeLabel("work.review") != "Review agent work" {
		t.Fatal("work review label changed")
	}
	var output bytes.Buffer
	err := YourTurnPage("Example Co", UserView{DisplayName: "Owner", Role: "owner"}, "reviews", YourTurnCountsView{Reviews: 1, Approvals: 1}, nil, InputCoordinatorView{}, []ApprovalView{{
		ID: "review-1", ActionType: "work.review", Reason: "Review ticket #0009", PersonaName: "Jordan", Status: "pending", RequestedAt: time.Now(), ReviewSummary: "Completed the control record.",
	}}, nil, "csrf", "", "", false).Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{"Ready for review", "Completed the control record.", "Accept and complete ticket"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("review view missing %q: %s", expected, html)
		}
	}
}
