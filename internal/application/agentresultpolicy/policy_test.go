package agentresultpolicy

import (
	"errors"
	"testing"

	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	currentPersona = ids.PersonaID("10000000-0000-4000-8000-000000000001")
	nextPersona    = ids.PersonaID("20000000-0000-4000-8000-000000000002")
	documentID     = "30000000-0000-4000-8000-000000000003"
	chunkID        = "40000000-0000-4000-8000-000000000004"
)

func TestValidateBindsCitationsActionsAndForwardDelegations(t *testing.T) {
	policy := Policy{Version: CurrentVersion, CitationPolicy: "required", ActionPolicy: "propose", CurrentPersonaID: currentPersona,
		DelegatePersonaIDs: []ids.PersonaID{nextPersona}, CitationBindings: []CitationBinding{{DocumentID: documentID, ChunkID: chunkID}}}
	result := validResult()
	if err := Validate(policy, result); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*agentdomain.ResultEnvelope){
		"invented citation":  func(value *agentdomain.ResultEnvelope) { value.Citations[0].ChunkID = documentID },
		"duplicate citation": func(value *agentdomain.ResultEnvelope) { value.Citations = append(value.Citations, value.Citations[0]) },
		"invented delegate":  func(value *agentdomain.ResultEnvelope) { value.Delegations[0].PersonaID = currentPersona },
		"duplicate delegate": func(value *agentdomain.ResultEnvelope) {
			value.Delegations = append(value.Delegations, value.Delegations[0])
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := validResult()
			mutate(&value)
			if err := Validate(policy, value); !errors.Is(err, ErrDenied) {
				t.Fatalf("expected denial, got %v", err)
			}
		})
	}
}

func TestValidateEnforcesSuppressionAndRequiredEvidence(t *testing.T) {
	result := validResult()
	result.Citations = nil
	if err := Validate(Policy{Version: CurrentVersion, CitationPolicy: "required", ActionPolicy: "propose", CurrentPersonaID: currentPersona, DelegatePersonaIDs: []ids.PersonaID{nextPersona}}, result); !errors.Is(err, ErrDenied) {
		t.Fatalf("expected missing citation denial, got %v", err)
	}
	result = validResult()
	if err := Validate(Policy{Version: CurrentVersion, CitationPolicy: "best_effort", ActionPolicy: "none", CurrentPersonaID: currentPersona, DelegatePersonaIDs: []ids.PersonaID{nextPersona}, CitationBindings: []CitationBinding{{DocumentID: documentID, ChunkID: chunkID}}}, result); !errors.Is(err, ErrDenied) {
		t.Fatalf("expected action suppression, got %v", err)
	}
}

func validResult() agentdomain.ResultEnvelope {
	return agentdomain.ResultEnvelope{Contribution: "Review the dependency.", Findings: []string{}, Recommendations: []string{}, Questions: []string{},
		Citations:       []agentdomain.Citation{{ID: "source-1", DocumentID: documentID, ChunkID: chunkID, Label: "Dependency record"}},
		ProposedActions: []agentdomain.ProposedAction{{Kind: "work.create", Reason: "Track remediation", Payload: []byte(`{}`), Evidence: []string{"source-1"}}},
		Delegations:     []agentdomain.Delegation{{PersonaID: nextPersona, Request: "Assess delivery impact."}}, Confidence: agentdomain.ConfidenceHigh}
}
