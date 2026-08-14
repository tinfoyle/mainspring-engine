package agent

import (
	"encoding/json"
	"strings"
)

const LegalComplianceEscalationKey = "legal_compliance"

const MissingCredentialEscalationKey = "missing_external_credential"

func MatchesLegalComplianceQuestion(question string) bool {
	normalized := strings.ToLower(strings.TrimSpace(question))
	return IsLegalComplianceDemo(question) ||
		strings.Contains(normalized, "legal compliance") ||
		strings.Contains(normalized, "legally compliant")
}

func IsLegalComplianceDemo(question string) bool {
	return strings.Contains(strings.ToLower(question), "[demo:unknown-answer:legal-compliance]")
}

// MatchesExternalCredentialQuestion identifies questions where the answer is
// not merely information: the business may need an externally issued artifact
// that must be located, verified, applied for, paid for, and retained.
func MatchesExternalCredentialQuestion(question string) bool {
	normalized := strings.ToLower(strings.TrimSpace(question))
	credential := containsAny(normalized, "license", "licence", "permit", "registration", "certificate", "certification")
	intent := containsAny(normalized, "need", "require", "required", "which", "what", "have", "obtain", "acquire", "apply", "missing")
	return credential && intent
}

func MissingCredentialWorkItemAction(question, searchQuery string) ProposedAction {
	searchQuery = strings.TrimSpace(searchQuery)
	if searchQuery == "" {
		searchQuery = strings.TrimSpace(question)
	}
	if searchQuery == "" {
		searchQuery = "required business licenses permits registrations certificates"
	}
	payload, _ := json.Marshal(map[string]string{
		"title":        "Confirm and acquire required business credentials",
		"description":  "Outcome: Confirm which externally issued licenses, permits, registrations, or certificates the business needs; verify whether each credential already exists; and acquire and retain any missing credential.\n\nWork plan:\n1. Confirm the legal entity, operating jurisdictions, work locations, services, trade classifications, and regulated activities.\n2. Ask the owner for existing licenses, permits, application receipts, and renewal records; upload and attach anything already held.\n3. Verify each requirement against the authoritative issuing agency, including the exact credential and classification.\n4. Record prerequisites, responsible owner, application steps, fees, bonds or insurance, examinations, lead time, expiration, and renewal rules.\n5. For each missing credential, obtain owner approval for required payments or representations, submit the application through the authorized channel, and track agency follow-up.\n6. Upload the issued credential and record its number, issuer, effective date, expiration date, and renewal owner in Documents.\n\nDefinition of done: Every applicable credential is classified as not required, already held and attached, or acquired and attached; authoritative sources, costs, application status, renewal dates, and accountable owners are recorded.",
		"priority":     "high",
		"origin":       "unknown_answer",
		"search_query": searchQuery,
	})
	return ProposedAction{
		ActionType: "tickets.create",
		Reason:     "Treat the missing credential as acquisition work rather than ending at an empty document search.",
		Payload:    payload,
		Evidence: []string{
			"The authorized document search did not locate the credential or proof that the business holds it.",
			"The owner must confirm whether an existing credential or application record is available before a new application or payment is started.",
		},
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func LegalComplianceWorkItemAction(searchQuery string) ProposedAction {
	searchQuery = strings.TrimSpace(searchQuery)
	if searchQuery == "" {
		searchQuery = "legal compliance obligations licenses permits policies audits"
	}
	payload, _ := json.Marshal(map[string]string{
		"title":        "Determine legal-compliance status",
		"description":  "Outcome: Determine the business's legal-compliance status for its known operations. This work item tracks an assessment and does not itself provide legal advice.\n\nWork plan:\n1. Confirm jurisdictions, entity structure, industry, workforce, data handled, and regulated activities.\n2. Inventory applicable licenses, permits, registrations, contracts, policies, and reporting obligations.\n3. Collect supporting records and prior legal or audit advice.\n4. Map each obligation to an owner, control, evidence, renewal date, and gap.\n5. Escalate high-risk gaps to qualified legal counsel and record the conclusion.\n\nDefinition of done: The scope, obligations, supporting evidence, gaps, owners, remediation dates, and counsel-reviewed conclusion are recorded.",
		"priority":     "high",
		"origin":       "unknown_answer",
		"search_query": searchQuery,
	})
	return ProposedAction{
		ActionType: "tickets.create",
		Reason:     "Track the evidence-gathering and qualified review needed to determine the business's legal-compliance status.",
		Payload:    payload,
		Evidence: []string{
			"The authorized document search completed without evidence sufficient to answer the compliance question.",
			"A legal-compliance conclusion depends on business-specific scope, obligations, controls, and qualified review.",
		},
	}
}
