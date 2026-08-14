package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestProductToolsExposeCoreWorkspace(t *testing.T) {
	tools := ProductTools(UserView{Role: "member"})
	byName := make(map[string]ProductToolView, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = tool
	}

	for name, href := range map[string]string{
		"Baseline":   "/baseline",
		"Boardrooms": "#boardrooms",
		"Work":       "/work",
		"Documents":  "/documents",
		"Finance":    "/finance",
		"Inbox":      "/inbox",
		"Approvals":  "/approvals",
		"Schedules":  "/schedules",
		"Email":      "/email",
	} {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("ProductTools() omitted %s", name)
		}
		if tool.Href != href {
			t.Errorf("ProductTools() %s href = %q, want %q", name, tool.Href, href)
		}
		if strings.TrimSpace(tool.Description) == "" || strings.TrimSpace(tool.Outcome) == "" {
			t.Errorf("ProductTools() %s needs both a description and outcome", name)
		}
	}
	if _, ok := byName["Agents"]; ok {
		t.Error("ProductTools() exposed owner-only Agents to a member")
	}
	if _, ok := byName["Operations"]; ok {
		t.Error("ProductTools() exposed owner-only Operations to a member")
	}
}

func TestProductToolsIncludeOwnerTools(t *testing.T) {
	tools := ProductTools(UserView{Role: "owner"})
	for _, name := range []string{"Agents", "Operations"} {
		found := false
		for _, tool := range tools {
			found = found || tool.Name == name
		}
		if !found {
			t.Errorf("ProductTools() omitted owner tool %s", name)
		}
	}
}

func TestDashboardWelcomeExplainsOnboardingHandoff(t *testing.T) {
	var rendered bytes.Buffer
	err := DashboardPage("Mainspring", UserView{DisplayName: "Owner", Role: "owner"}, nil, "csrf", true).Render(context.Background(), &rendered)
	if err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	html := rendered.String()
	for _, text := range []string{
		"Onboarding complete",
		"Welcome to Mainspring, Owner",
		"baseline work plan is running in the background",
		"Your business toolkit",
		"View work queue",
	} {
		if !strings.Contains(html, text) {
			t.Errorf("welcome dashboard omitted %q", text)
		}
	}
}
