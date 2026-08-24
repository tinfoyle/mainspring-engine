package mcpapi

import (
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
)

// ToolRequirement is the global routing classification for a published MCP
// tool. The gateway uses it before a request enters a cell; the tool handler
// repeats the same authorization against the signed route claims in the cell.
func ToolRequirement(name string) (access.Requirement, bool) {
	read := func(code catalog.PackageCode) access.Requirement { return access.Requirement{Package: code} }
	write := func(code catalog.PackageCode) access.Requirement {
		return access.Requirement{Package: code, Mutation: true}
	}

	switch name {
	case "spyglass_attention_information_list", "spyglass_attention_information_get",
		"spyglass_attention_review_list", "spyglass_attention_review_get":
		return read(catalog.PackageWork), true
	case "spyglass_attention_information_create", "spyglass_attention_information_answer",
		"spyglass_attention_information_cancel", "spyglass_attention_review_create",
		"spyglass_attention_review_decide", "spyglass_attention_review_cancel":
		return write(catalog.PackageWork), true
	case "spyglass_attention_approval_list", "spyglass_attention_approval_get":
		return read(catalog.PackageAgents), true
	case "spyglass_attention_approval_create", "spyglass_attention_approval_decide",
		"spyglass_attention_approval_cancel":
		return write(catalog.PackageAgents), true
	case "spyglass_attention_action_list", "spyglass_attention_action_get":
		return access.Requirement{Roles: actionRecoveryRoles(), Package: catalog.PackageAgents}, true
	case "spyglass_attention_action_request_resolution", "spyglass_attention_action_confirm_resolution":
		return access.Requirement{Roles: actionRecoveryRoles(), Package: catalog.PackageAgents, Mutation: true}, true
	case "spyglass_knowledge_claim_get", "spyglass_knowledge_claim_list",
		"spyglass_knowledge_fact_list", "spyglass_knowledge_document_retrieve",
		"spyglass_knowledge_document_citation_get", "spyglass_baseline_get":
		return read(catalog.PackageKnowledge), true
	case "spyglass_knowledge_evidence_register", "spyglass_knowledge_claim_propose",
		"spyglass_knowledge_claim_decide", "spyglass_baseline_start", "spyglass_baseline_mutate":
		return write(catalog.PackageKnowledge), true
	case "spyglass_baseline_source_list":
		return read(catalog.PackageIntegrations), true
	case "spyglass_baseline_source_mutate":
		return write(catalog.PackageIntegrations), true
	case "spyglass_finance_ledger_list", "spyglass_finance_ledger_get",
		"spyglass_finance_posting_account_list", "spyglass_finance_posting_account_get",
		"spyglass_finance_entry_list", "spyglass_finance_entry_get",
		"spyglass_finance_reconciliation_list", "spyglass_finance_reconciliation_get":
		return read(catalog.PackageFinance), true
	case "spyglass_finance_ledger_create", "spyglass_finance_ledger_revise",
		"spyglass_finance_ledger_close_period", "spyglass_finance_ledger_archive",
		"spyglass_finance_posting_account_create", "spyglass_finance_posting_account_revise",
		"spyglass_finance_posting_account_archive", "spyglass_finance_entry_create_draft",
		"spyglass_finance_entry_revise_draft", "spyglass_finance_entry_post",
		"spyglass_finance_entry_reverse", "spyglass_finance_reconciliation_create",
		"spyglass_finance_reconciliation_confirm":
		return write(catalog.PackageFinance), true
	case "spyglass_marketing_campaign_list", "spyglass_marketing_campaign_get",
		"spyglass_marketing_asset_revision_list", "spyglass_marketing_release_list",
		"spyglass_marketing_release_get":
		return read(catalog.PackageMarketing), true
	case "spyglass_marketing_campaign_create_draft", "spyglass_marketing_campaign_revise",
		"spyglass_marketing_campaign_archive", "spyglass_marketing_asset_revision_create_draft",
		"spyglass_marketing_release_create_draft", "spyglass_marketing_release_submit",
		"spyglass_marketing_release_approve", "spyglass_marketing_release_cancel",
		"spyglass_marketing_campaign_activate", "spyglass_marketing_campaign_pause",
		"spyglass_marketing_campaign_complete":
		return write(catalog.PackageMarketing), true
	case "spyglass_integrations_connection_list", "spyglass_integrations_connection_get",
		"spyglass_integrations_health_list", "spyglass_integrations_execution_list",
		"spyglass_integrations_execution_get", "spyglass_integrations_authorization_status":
		return read(catalog.PackageIntegrations), true
	case "spyglass_integrations_connection_create", "spyglass_integrations_connection_revise",
		"spyglass_integrations_credential_activate", "spyglass_integrations_credential_rotate",
		"spyglass_integrations_connection_disable", "spyglass_integrations_connection_enable",
		"spyglass_integrations_connection_revoke", "spyglass_integrations_execution_prepare",
		"spyglass_integrations_authorization_begin", "spyglass_integrations_credential_revoke",
		"spyglass_integrations_execution_request_resolution", "spyglass_integrations_execution_confirm_resolution":
		return write(catalog.PackageIntegrations), true
	default:
		return access.Requirement{}, false
	}
}

func actionRecoveryRoles() []accounts.MembershipRole {
	return []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator}
}
