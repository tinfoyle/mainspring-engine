package tools

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeWorkInputPayloadNormalizesAndDeduplicatesQuestions(t *testing.T) {
	raw, _ := json.Marshal(WorkInputPayload{
		ParentWorkItemID: "00000000-0000-0000-0000-000000000123",
		Questions:        []string{" Which system is authoritative? ", "which system is authoritative?", "Who owns it?"},
	})
	payload, err := DecodeWorkInputPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Questions) != 2 || payload.Questions[0] != "Which system is authoritative?" || payload.Questions[1] != "Who owns it?" {
		t.Fatalf("normalized questions = %#v", payload.Questions)
	}
}

func TestDecodeWorkInputPayloadAcceptsStructuredFactRequirements(t *testing.T) {
	raw, _ := json.Marshal(WorkInputPayload{
		ParentWorkItemID: "00000000-0000-0000-0000-000000000123",
		Requirements:     []WorkInputRequirement{{FactKey: "operations.system_of_record", Question: "Which system is authoritative?", Reason: "Needed by the launch checklist"}},
	})
	payload, err := DecodeWorkInputPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Questions) != 1 || len(payload.Requirements) != 1 || payload.Requirements[0].FactKey != "operations.system_of_record" {
		t.Fatalf("structured requirement = %#v", payload)
	}
}

func TestFactKeyForQuestionConsolidatesRelatedBusinessQuestions(t *testing.T) {
	tests := []struct {
		first  string
		second string
		want   string
	}{
		{"Which business licenses do we currently hold?", "Do we have the permits required to operate?", "compliance.licenses"},
		{"Who owns security?", "Who is responsible for approving production access?", "security.access_owner"},
		{"Which system is our source of truth?", "What is the authoritative system?", "operations.system_of_record"},
	}
	for _, test := range tests {
		if first, second := FactKeyForQuestion(test.first), FactKeyForQuestion(test.second); first != test.want || second != test.want {
			t.Fatalf("keys for %q and %q = %q, %q; want %q", test.first, test.second, first, second, test.want)
		}
	}
}

func TestFactKeyForQuestionDoesNotConfuseDocumentsWithAccountingSystem(t *testing.T) {
	question := "Please upload or attach the available source records for the desired period—accounting-system exports or ledger, bank and credit-card statements, invoices, and vendor bills."
	if key := FactKeyForQuestion(question); key != "evidence.financial_records" {
		t.Fatalf("financial records key = %q", key)
	}
}

func TestFactKeyForQuestionKeepsBroadBusinessQuestionsDistinct(t *testing.T) {
	questions := []string{
		"For each launch system, identify its location, privileged users, recovery method, and customer data.",
		"Who are the intended launch users and locations, and will the service handle sensitive information?",
		"What pricing and revenue model should the scorecard measure at launch: subscription, services, or usage based?",
	}
	keys := map[string]bool{}
	for _, question := range questions {
		key := FactKeyForQuestion(question)
		if key == "organization.primary_jurisdiction" || key == "organization.services" || key == "data.handling" {
			t.Fatalf("broad question %q collapsed into %q", question, key)
		}
		keys[key] = true
	}
	if len(keys) != len(questions) {
		t.Fatalf("broad questions collapsed into %#v", keys)
	}
}

func TestHeuristicFactKeysCannotSilentlyAnswerNewOwnerQuestions(t *testing.T) {
	question := "Which applications, vendors, users, disclosures, and retention rules apply to customer data?"
	if source := humanInputKeySource(WorkInputRequirement{FactKey: FactKeyForQuestion(question), Question: question}); source != "heuristic" {
		t.Fatalf("fallback fact source = %q, want heuristic", source)
	}
	if source := humanInputKeySource(WorkInputRequirement{FactKey: "data.launch_inventory", Question: question, Reason: "Exact launch inventory required"}); source != "agent" {
		t.Fatalf("explicit fact source = %q, want agent", source)
	}
}

func TestCoordinatorUsesSpecificFollowUpForKnownFact(t *testing.T) {
	question := "Before the first founder payment, provide the LLC's tax classification and accountant-confirmed treatment."
	label, prompt := coordinatorQuestionPresentation("organization.entity_type", []string{question}, true)
	if label != "Legal entity" {
		t.Fatalf("label = %q", label)
	}
	if prompt != question {
		t.Fatalf("prompt = %q, want exact ticket question %q", prompt, question)
	}
}

func TestCoordinatorPreservesAllCustomFollowUps(t *testing.T) {
	questions := []string{"Who owns the domain?", "Where is the renewal record kept?"}
	_, prompt := coordinatorQuestionPresentation("owner.domain_record", questions, false)
	for _, question := range questions {
		if !strings.Contains(prompt, question) {
			t.Fatalf("prompt %q does not contain %q", prompt, question)
		}
	}
}

func TestCoordinatorCanConsolidateFirstTimeKnownCategory(t *testing.T) {
	_, prompt := coordinatorQuestionPresentation("compliance.licenses", []string{
		"Which permits do we hold?",
		"Which licenses are current?",
	}, false)
	_, canonical := factPresentation("compliance.licenses", "")
	if prompt != canonical {
		t.Fatalf("prompt = %q, want canonical %q", prompt, canonical)
	}
}

func TestHumanInputDocumentPayloadIsAlwaysJSONArray(t *testing.T) {
	for _, documents := range [][]string{nil, {}, {"document-1"}} {
		payload, present := humanInputDocumentsJSON(documents)
		var decoded []string
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatalf("document payload %q is not an array: %v", payload, err)
		}
		if !reflect.DeepEqual(decoded, documents) && !(documents == nil && len(decoded) == 0) {
			t.Fatalf("decoded documents = %#v, want %#v", decoded, documents)
		}
		if present != (len(documents) > 0) {
			t.Fatalf("present = %v for %#v", present, documents)
		}
	}
}

func TestDecodeWorkInputPayloadRejectsMissingParent(t *testing.T) {
	if _, err := DecodeWorkInputPayload(json.RawMessage(`{"questions":["Who owns it?"]}`)); err == nil {
		t.Fatal("expected missing parent to fail")
	}
}

func TestLegacyProposalQuestionsExtractsNumberedChecklist(t *testing.T) {
	payload := TicketCreatePayload{Description: `The agent needs private information.

Please answer or provide:
1. Which accounting system is authoritative?
2. Which reporting period should be used?

Definition of done: Every item is answered.`}
	questions := legacyProposalQuestions(payload, "The assigned agent needs 2 private business answer(s) to continue.")
	if len(questions) != 2 || questions[0] != "Which accounting system is authoritative?" || questions[1] != "Which reporting period should be used?" {
		t.Fatalf("legacy questions = %#v", questions)
	}
}

func TestLegacyProposalQuestionsExtractsSingleQuestion(t *testing.T) {
	payload := TicketCreatePayload{Description: `The agent could not obtain this information.

Question: Who approves production access?

Definition of done: The owner answers.`}
	questions := legacyProposalQuestions(payload, "Who approves production access?")
	if len(questions) != 1 || questions[0] != "Who approves production access?" {
		t.Fatalf("legacy questions = %#v", questions)
	}
}
