package boardroom

import (
	"strings"

	"github.com/tinfoyle/mainspring-engine/internal/agent"
	toolbroker "github.com/tinfoyle/mainspring-engine/internal/tools"
)

func removeUnsearchedUnknownAnswerActions(result agent.Result, documentSearchCompleted bool) agent.Result {
	if documentSearchCompleted {
		return result
	}
	filtered := result.Structured.ProposedActions[:0]
	removed := false
	for _, action := range result.Structured.ProposedActions {
		if action.ActionType == toolbroker.TicketCreateAction {
			payload, err := toolbroker.DecodeTicketCreatePayload(action.Payload)
			if err == nil && payload.Origin == "unknown_answer" {
				removed = true
				continue
			}
		}
		filtered = append(filtered, action)
	}
	result.Structured.ProposedActions = filtered
	if removed {
		result.Structured.Contribution = strings.TrimSpace(result.Structured.Contribution) + "\n\nCorrection: No work item was proposed in this run because the required internal-document search did not complete. Search the tenant document library first; if the evidence remains insufficient, the boardroom can then offer an approval-gated work item."
		result.Body = result.Structured.Contribution
	}
	return result
}

func applyUnknownAnswerEscalation(result agent.Result, question string, trace invocationToolTrace, complianceDelegate string, canCreateTicket bool, actionPolicy string) agent.Result {
	demo := agent.IsLegalComplianceDemo(question)
	if !agent.MatchesLegalComplianceQuestion(question) || trace.DocumentSearches == 0 || (result.Structured.Confidence != "low" && !demo) || len(result.Structured.Citations) > 0 || !canCreateTicket || (actionPolicy == "disabled" && !demo) {
		return result
	}
	complianceDelegate = strings.TrimSpace(complianceDelegate)
	if complianceDelegate != "" {
		result.Structured.Contribution = "Plan: You are asking whether the business is legally compliant. I searched the authorized document library first, but the available records do not contain sufficient evidence to support a yes-or-no conclusion.\n\nI am asking " + complianceDelegate + " to define the assessment scope, evidence requirements, and highest-priority gaps before I recommend tracked work. No compliance conclusion or work item has been created yet."
		result.Structured.Findings = []string{"The completed document search did not provide evidence sufficient to determine legal-compliance status."}
		result.Structured.Recommendations = []string{"Use the compliance specialist's assessment to define a reviewable work item."}
		result.Structured.Questions = []string{}
		result.Structured.Delegations = []agent.Delegation{{
			Agent:   complianceDelegate,
			Request: "Define the applicable legal and regulatory scope, identify the minimum evidence required, and provide a prioritized compliance-gap assessment. Distinguish verified evidence from missing records and flag conclusions requiring qualified legal counsel.",
		}}
		result.Structured.ProposedActions = []agent.ProposedAction{}
		result.Body = result.Structured.Contribution
		return result
	}
	result.Structured.Contribution = "Plan: You are asking whether the business is legally compliant. I searched the authorized document library first, but the available records do not contain sufficient evidence to support a yes-or-no conclusion.\n\nDecision: Legal-compliance status is unverified. I can create a high-priority compliance-assessment work item that establishes scope, gathers required records, maps obligations to controls and gaps, and obtains qualified legal review. The work item has not been created yet; approve the proposed next step below to add it to Work."
	result.Structured.Findings = []string{"The completed document search did not provide evidence sufficient to determine legal-compliance status."}
	result.Structured.Recommendations = []string{"Approve the proposed compliance-assessment work item and complete its evidence plan before making a compliance claim."}
	result.Structured.Questions = []string{}
	result.Structured.Delegations = []agent.Delegation{}
	result.Structured.ProposedActions = append(result.Structured.ProposedActions, agent.LegalComplianceWorkItemAction(trace.LastSearchQuery))
	result.Body = result.Structured.Contribution
	return result
}

func applyMissingCredentialEscalation(result agent.Result, question string, trace invocationToolTrace, canCreateTicket bool, actionPolicy string) agent.Result {
	if !agent.MatchesExternalCredentialQuestion(question) || trace.DocumentSearches == 0 || hasDocumentEvidence(result.Structured.Citations) {
		return result
	}

	nextStep := "The document search did not locate the license, permit, registration, certificate, or application record. That is a missing-credential workflow, not a dead end. Do you already have the credential or an application/renewal record? If yes, upload and attach it so the boardroom can verify it. If not, the next step is to confirm the exact issuing authority and credential class, prerequisites, fees, application process, lead time, and renewal obligations, then acquire it through the authorized channel."
	if !strings.Contains(strings.ToLower(result.Structured.Contribution), "missing-credential workflow") {
		result.Structured.Contribution = strings.TrimSpace(result.Structured.Contribution) + "\n\nNext step: " + nextStep
	}
	result.Structured.Findings = appendUnique(result.Structured.Findings, "No matching credential or application record was found in the authorized document library.")
	result.Structured.Recommendations = appendUnique(result.Structured.Recommendations, "Confirm whether the owner already holds the credential; otherwise track its verification, application, payment approval, acquisition, and renewal evidence to completion.")
	result.Structured.Questions = appendUnique(result.Structured.Questions, "Do you already have this credential or an application/renewal record that can be uploaded?")
	result.Structured.Confidence = "low"

	if canCreateTicket && actionPolicy != "disabled" && !hasTicketProposal(result.Structured.ProposedActions) {
		result.Structured.ProposedActions = append(result.Structured.ProposedActions, agent.MissingCredentialWorkItemAction(question, trace.LastSearchQuery))
		result.Structured.Delegations = []agent.Delegation{}
		result.Structured.Contribution += " I can create the acquisition work item below now; it will not exist until you approve it."
	}
	result.Body = result.Structured.Contribution
	return result
}

func hasDocumentEvidence(citations []agent.Citation) bool {
	for _, citation := range citations {
		if strings.TrimSpace(citation.DocumentID) != "" || strings.TrimSpace(citation.ChunkID) != "" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(citation.ID)), "doc:") {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(value)) {
			return values
		}
	}
	return append(values, value)
}

func hasTicketProposal(actions []agent.ProposedAction) bool {
	for _, action := range actions {
		if action.ActionType == toolbroker.TicketCreateAction {
			return true
		}
	}
	return false
}

func applyFinalUnknownAnswerEscalation(result agent.Result, question, searchQuery, specialistName string, canCreateTicket bool, actionPolicy string) agent.Result {
	demo := agent.IsLegalComplianceDemo(question)
	if !agent.MatchesLegalComplianceQuestion(question) || strings.TrimSpace(searchQuery) == "" || !canCreateTicket || (actionPolicy == "disabled" && !demo) {
		return result
	}
	for _, action := range result.Structured.ProposedActions {
		if action.ActionType == "tickets.create" {
			result.Structured.Delegations = []agent.Delegation{}
			return result
		}
	}
	specialistName = strings.TrimSpace(specialistName)
	if specialistName == "" {
		specialistName = "The compliance specialist"
	}
	result.Structured.Contribution = strings.TrimSpace(result.Structured.Contribution) + "\n\nProposed next step: " + specialistName + "'s assessment confirms that a defensible answer requires scoped evidence gathering and qualified review. I can create the compliance-assessment work item below; it has not been created yet and requires owner approval."
	result.Structured.Recommendations = append(result.Structured.Recommendations, "Approve the proposed compliance-assessment work item and complete its evidence plan before making a compliance claim.")
	result.Structured.Delegations = []agent.Delegation{}
	result.Structured.ProposedActions = append(result.Structured.ProposedActions, agent.LegalComplianceWorkItemAction(searchQuery))
	result.Structured.Confidence = "low"
	result.Body = result.Structured.Contribution
	return result
}
