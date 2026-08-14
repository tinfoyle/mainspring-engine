package tenant

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tinfoyle/mainspring-engine/internal/boardroom"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

func TestNormalizeWorkFilter(t *testing.T) {
	filter := NormalizeWorkFilter(WorkFilter{Status: " IN_PROGRESS ", Kind: " Ticket ", Query: "  SLA breach  "})
	if filter.Status != "in_progress" || filter.Kind != "ticket" || filter.Query != "SLA breach" {
		t.Fatalf("normalized filter = %#v", filter)
	}

	defaults := NormalizeWorkFilter(WorkFilter{Status: "not-real", Kind: "not-real"})
	if defaults.Status != "active" || defaults.Kind != "all" {
		t.Fatalf("default filter = %#v", defaults)
	}

	longQuery := NormalizeWorkFilter(WorkFilter{Query: strings.Repeat("x", 250)})
	if len(longQuery.Query) != 200 {
		t.Fatalf("query length = %d, want 200", len(longQuery.Query))
	}
}

func TestSelectedWorkItemPersonasRequiresEnabledUniqueAgents(t *testing.T) {
	first := domain.NewPersonaID()
	second := domain.NewPersonaID()
	personas := []boardroom.Persona{{ID: first}, {ID: second}}
	selected, err := selectedWorkItemPersonas([]string{first.String(), first.String(), second.String()}, personas, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0] != first.String() || selected[1] != second.String() {
		t.Fatalf("selected = %#v", selected)
	}
	if _, err := selectedWorkItemPersonas(nil, personas, 3); err == nil {
		t.Fatal("expected empty selection error")
	}
	if _, err := selectedWorkItemPersonas([]string{uuid.NewString()}, personas, 3); err == nil {
		t.Fatal("expected unavailable agent error")
	}
}

func TestCreateWorkItemRejectsInvalidInputBeforePersistence(t *testing.T) {
	store := &Store{}
	valid := CreateWorkItemInput{Kind: "todo", Title: "Call the customer", Priority: "normal", Source: "user"}
	tests := []struct {
		name   string
		mutate func(*CreateWorkItemInput)
	}{
		{"kind", func(input *CreateWorkItemInput) { input.Kind = "note" }},
		{"title", func(input *CreateWorkItemInput) { input.Title = "" }},
		{"description", func(input *CreateWorkItemInput) { input.Description = strings.Repeat("x", 6001) }},
		{"priority", func(input *CreateWorkItemInput) { input.Priority = "critical" }},
		{"source", func(input *CreateWorkItemInput) { input.Source = "unknown" }},
		{"persona id", func(input *CreateWorkItemInput) { input.AssignedPersonaID = "not-a-uuid" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.mutate(&input)
			if _, err := store.CreateWorkItem(t.Context(), input); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestUpdateWorkItemStatusRejectsInvalidStateBeforePersistence(t *testing.T) {
	store := &Store{}
	if err := store.UpdateWorkItemStatus(t.Context(), uuid.NewString(), "blocked_forever"); err == nil {
		t.Fatal("expected invalid status error")
	}
	if err := store.UpdateWorkItemStatus(t.Context(), "not-a-uuid", "done"); err != ErrWorkItemNotFound {
		t.Fatalf("invalid id error = %v, want ErrWorkItemNotFound", err)
	}
}
