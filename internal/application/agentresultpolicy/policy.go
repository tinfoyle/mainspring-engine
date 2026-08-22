// Package agentresultpolicy applies the immutable application-owned policy to
// a structurally valid Agent result. Models can propose citations, actions and
// delegations; this package decides whether those proposals may be published.
package agentresultpolicy

import (
	"errors"
	"slices"
	"strings"

	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	LegacyVersion           uint32 = 1
	ActionVersion           uint32 = 2
	CurrentVersion          uint32 = 3
	MaximumProjectedActions        = 8
	MaximumOwnerQuestions          = 8
)

var ErrDenied = errors.New("agent result violates immutable result policy")

type CitationBinding struct {
	DocumentID string
	ChunkID    string
}

type Policy struct {
	Version            uint32
	CitationPolicy     string
	ActionPolicy       string
	ActionCapabilities []string
	CurrentPersonaID   ids.PersonaID
	DelegatePersonaIDs []ids.PersonaID
	CitationBindings   []CitationBinding
}

func Validate(policy Policy, result agentdomain.ResultEnvelope) error {
	if (policy.Version < LegacyVersion || policy.Version > CurrentVersion) || ids.Validate(string(policy.CurrentPersonaID)) != nil ||
		!slices.Contains([]string{"none", "required", "best_effort"}, policy.CitationPolicy) ||
		!slices.Contains([]string{"none", "propose"}, policy.ActionPolicy) {
		return ErrDenied
	}
	for index, capability := range policy.ActionCapabilities {
		if strings.TrimSpace(capability) != capability || capability == "" || len(capability) > 128 || slices.Contains(policy.ActionCapabilities[:index], capability) {
			return ErrDenied
		}
	}
	for index, personaID := range policy.DelegatePersonaIDs {
		if ids.Validate(string(personaID)) != nil || personaID == policy.CurrentPersonaID || slices.Contains(policy.DelegatePersonaIDs[:index], personaID) {
			return ErrDenied
		}
	}
	for index, binding := range policy.CitationBindings {
		if ids.Validate(binding.DocumentID) != nil || ids.Validate(binding.ChunkID) != nil || slices.Contains(policy.CitationBindings[:index], binding) {
			return ErrDenied
		}
	}
	if (policy.CitationPolicy == "none" && len(result.Citations) != 0) || (policy.CitationPolicy == "required" && len(result.Citations) == 0) ||
		(policy.ActionPolicy == "none" && len(result.ProposedActions) != 0) {
		return ErrDenied
	}
	if policy.Version >= ActionVersion {
		if len(result.ProposedActions) > MaximumProjectedActions {
			return ErrDenied
		}
		for _, action := range result.ProposedActions {
			if !slices.Contains(policy.ActionCapabilities, action.Kind) {
				return ErrDenied
			}
		}
	}
	if policy.Version >= CurrentVersion && len(result.Questions) > MaximumOwnerQuestions {
		return ErrDenied
	}
	citationIDs := make([]string, 0, len(result.Citations))
	for _, citation := range result.Citations {
		binding := CitationBinding{DocumentID: strings.TrimSpace(citation.DocumentID), ChunkID: strings.TrimSpace(citation.ChunkID)}
		if strings.TrimSpace(citation.ID) == "" || strings.TrimSpace(citation.Label) == "" || slices.Contains(citationIDs, citation.ID) || !slices.Contains(policy.CitationBindings, binding) {
			return ErrDenied
		}
		citationIDs = append(citationIDs, citation.ID)
	}
	delegated := make([]ids.PersonaID, 0, len(result.Delegations))
	for _, delegation := range result.Delegations {
		if !slices.Contains(policy.DelegatePersonaIDs, delegation.PersonaID) || slices.Contains(delegated, delegation.PersonaID) {
			return ErrDenied
		}
		delegated = append(delegated, delegation.PersonaID)
	}
	return nil
}
