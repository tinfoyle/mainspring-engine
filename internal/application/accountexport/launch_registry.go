package accountexport

// LaunchRegistry is the reviewed Phase 3 portability policy. Included tables
// are exported through bounded, customer-safe projections; this does not grant
// permission to serialize raw rows. Excluded tables require an explicit class
// and reason so schema growth fails closed in the integration suite.
func LaunchRegistry() (*Registry, error) {
	sections := []Descriptor{
		{Code: "account", SchemaVersion: 1, Stores: []string{"cell-postgresql", "global-postgresql"}},
		{Code: "affiliate", SchemaVersion: 1, Stores: []string{"global-postgresql"}},
		{Code: "agents", SchemaVersion: 1, Stores: []string{"cell-postgresql"}},
		{Code: "attention", SchemaVersion: 1, Stores: []string{"cell-postgresql"}},
		{Code: "baseline", SchemaVersion: 1, Stores: []string{"cell-postgresql"}},
		{Code: "billing", SchemaVersion: 1, Stores: []string{"global-postgresql"}},
		{Code: "entitlements", SchemaVersion: 1, Stores: []string{"global-postgresql"}},
		{Code: "finance", SchemaVersion: 1, Stores: []string{"cell-postgresql"}},
		{Code: "integrations", SchemaVersion: 1, Stores: []string{"cell-postgresql"}},
		{Code: "knowledge", SchemaVersion: 1, Stores: []string{"cell-postgresql"}},
		{Code: "knowledge_objects", SchemaVersion: 1, Stores: []string{"versioned-object-store"}},
		{Code: "marketing", SchemaVersion: 1, Stores: []string{"cell-postgresql"}},
		{Code: "marketing_objects", SchemaVersion: 1, Stores: []string{"versioned-object-store"}},
		{Code: "migration", SchemaVersion: 1, Stores: []string{"cell-postgresql"}},
		{Code: "schedules", SchemaVersion: 1, Stores: []string{"cell-postgresql"}},
		{Code: "work", SchemaVersion: 1, Stores: []string{"cell-postgresql"}},
	}
	var tables []TableCoverage
	include := func(schema, section string, names ...string) {
		for _, name := range names {
			tables = append(tables, TableCoverage{Schema: schema, Table: name, Section: section, Disposition: Included})
		}
	}
	exclude := func(schema string, disposition Disposition, reason string, names ...string) {
		for _, name := range names {
			tables = append(tables, TableCoverage{Schema: schema, Table: name, Disposition: disposition, Reason: reason})
		}
	}

	include("public", "account", "accounts", "account_closure_requests", "account_lifecycle_events", "account_membership_events", "invitations", "memberships", "operations_access_events", "operations_support_grants")
	exclude("public", Derived, "Cell placement is reconstructed from the authoritative Account projection.", "account_directory")
	exclude("public", Operational, "Erasure workflow evidence is retained under the erasure policy and is not customer content.", "account_erasure_operator_events", "account_erasure_requests")
	exclude("public", Operational, "Export request workflow and audit rows describe artifact processing; the artifact contains the portable customer projections.", "account_export_events", "account_export_requests")
	exclude("public", Operational, "Movement coordination state is transient infrastructure metadata, not portable customer content.", "account_moves")
	include("public", "billing", "account_commissioning_purchases", "account_subscription_lifecycles", "ai_token_grants", "ai_token_ledger_entries", "billing_checkout_attempts", "billing_profiles", "subscriptions")
	exclude("public", Operational, "Subscription lifecycle events are immutable controller workflow evidence; the customer-safe lifecycle projection carries the current and terminal dates.", "account_subscription_lifecycle_events")
	exclude("public", Operational, "Subscription lifecycle notices and termination jobs contain internal delivery, provider, retry and lease state rather than portable customer content.", "account_subscription_lifecycle_notices", "account_subscription_termination_jobs")
	exclude("public", Operational, "AI Token reservations are transient execution controls; portable grant and immutable debit/release evidence is exported instead.", "ai_token_reservations")
	exclude("public", Operational, "AI Token promotion issuance rows enforce campaign caps and idempotency; the portable promotion grant and ledger evidence carry the customer state.", "ai_token_promotion_issuances")
	include("public", "affiliate", "affiliate_attributions")
	exclude("public", IdentityScoped, "Affiliate enrollment, earnings and settlement evidence belong to the Affiliate identity and are exported through the strong-authenticated Affiliate portability endpoint, not an Account artifact.", "affiliate_enrollments", "affiliate_provider_adverse_invoice_lines", "affiliate_commission_entries", "affiliate_commission_invoice_payments", "affiliate_commission_invoice_lines", "affiliate_credit_reservations", "affiliate_credit_allocations", "affiliate_credit_reservation_events", "affiliate_credit_reversal_adjustments", "affiliate_credit_reversal_adjustment_events")
	exclude("public", Operational, "Versioned Affiliate commission rules are controller commercial policy, not Account-owned customer content.", "affiliate_commission_rules")
	exclude("public", Operational, "Affiliate settlement policy publications and operator audit rows are controller commercial policy, not Account-owned customer content.", "affiliate_settlement_policies", "affiliate_settlement_policy_current", "affiliate_settlement_policy_events")
	exclude("public", IdentityScoped, "Consent subjects, consent receipts and pseudonymous analytics require the separate privacy-subject rights workflow.", "privacy_consent_subjects", "privacy_consent_decisions", "analytics_events")
	exclude("public", Secret, "Verified provider event envelopes and encrypted payload references are internal security material.", "billing_event_inbox")
	exclude("public", Operational, "Billing operator interventions are internal processing audit records.", "billing_operator_events")
	exclude("public", Operational, "Provider subscription reconciliation rows are transient billing synchronization controls.", "billing_reconciliation_queue")
	include("public", "entitlements", "entitlement_grants", "entitlement_snapshots", "entitlement_usage_counters")
	exclude("public", Operational, "Entitlement recomputation leases and reservations are transient concurrency controls.", "entitlement_recompute_queue", "entitlement_usage_reservations")
	exclude("public", Secret, "Notification outbox rows contain encrypted delivery payloads and internal retry state.", "identity_notification_outbox")
	exclude("public", Operational, "Tool context receipts are short-lived anti-replay and routing controls.", "tool_context_receipts")

	include("spyglass", "account", "account_audit_events")
	exclude("spyglass", Derived, "The cell namespace is reconstructed from the authoritative Account projection.", "account_namespaces")
	exclude("spyglass", Operational, "Movement checkpoints are transient cross-cell coordination state.", "account_move_checkpoints")
	include("spyglass", "agents", "agent_boardrooms", "agent_conversations", "agent_invocation_execution_plans", "agent_invocations", "agent_messages", "agent_persona_versions", "agent_personas", "agent_run_plan_turns", "agent_run_resolutions", "agent_runs", "agent_user_messages", "runner_action_attempts", "runner_action_authorizations", "runner_action_ledger", "runner_action_manual_resolutions", "runner_capability_events")
	exclude("spyglass", Operational, "Agent dispatch, projection, and operator queues are transient processing controls.", "agent_dispatch_queue", "agent_queue_operator_events", "agent_result_projection_queue")
	exclude("spyglass", Operational, "Runner scheduling and execution queues are transient workload controls.", "runner_account_scheduling", "runner_action_execution_queue", "runner_invocation_queue")
	exclude("spyglass", Secret, "Runner exchanges contain encrypted model and tool payloads; safe messages and action projections are exported instead.", "runner_invocation_exchanges")
	include("spyglass", "attention", "attention_consequential_approvals", "attention_events", "attention_information_requests", "attention_work_reviews")
	include("spyglass", "baseline", "baseline_assessments", "baseline_events", "baseline_evidence_decisions", "baseline_interview_answers", "baseline_plan_work", "baseline_plans", "baseline_requirements", "baseline_source_grant_events", "baseline_source_grants")
	exclude("spyglass", Operational, "Baseline maintenance leases and retries are transient processing controls.", "baseline_maintenance_queue")
	include("spyglass", "finance", "finance_accounts", "finance_entries", "finance_entry_evidence", "finance_entry_lines", "finance_events", "finance_ledger_close_evidence", "finance_ledgers", "finance_reconciliation_evidence", "finance_reconciliations")
	exclude("spyglass", Derived, "Finance entry numbers are present on exported entries; allocator counters are reconstructed.", "finance_entry_number_counters")
	include("spyglass", "integrations", "integration_connection_revisions", "integration_connections", "integration_events", "integration_execution_attempts", "integration_execution_resolutions", "integration_executions", "integration_health_observations", "integration_source_captures", "integration_web_research_captures")
	exclude("spyglass", Secret, "Provider authorization sessions retain state and PKCE digests plus transient credential-generation bindings; they are security controls, not portable customer content.", "integration_authorization_sessions")
	exclude("spyglass", Operational, "Provider authorization events are content-free security workflow evidence and are not customer content.", "integration_authorization_events")
	exclude("spyglass", Secret, "Provider authorization workflows contain code digests, credential-generation plans and recovery state; they are security controls, not portable customer content.", "integration_authorization_workflows")
	exclude("spyglass", Secret, "Credential revocation workflows contain broker attestations and operational convergence state; they are security controls, not portable customer content.", "integration_credential_revocation_workflows")
	exclude("spyglass", Secret, "Credential references and their digests are security material and never enter portability artifacts.", "integration_credentials")
	exclude("spyglass", Operational, "Integration execution queue leases and retries are transient processing controls.", "integration_execution_queue")
	exclude("spyglass", Derived, "Provider health probe leases are regenerated from active Integration connection bindings.", "integration_health_probe_queue")
	exclude("spyglass", Secret, "Encrypted provider cursors and their sync leases are discarded and regenerated after movement or restore.", "integration_source_sync_queue")
	include("spyglass", "knowledge", "knowledge_claim_citations", "knowledge_claims", "knowledge_document_events", "knowledge_document_revisions", "knowledge_documents", "knowledge_events", "knowledge_evidence", "knowledge_fact_revisions", "knowledge_facts")
	exclude("spyglass", Derived, "Search chunks are regenerated from the exported original document revisions.", "knowledge_document_chunks")
	exclude("spyglass", Operational, "Document processing and deletion queues and receipts are internal lifecycle controls.", "knowledge_document_deletion_queue", "knowledge_document_deletion_receipts", "knowledge_document_processing_queue")
	include("spyglass", "marketing", "marketing_asset_revisions", "marketing_assets", "marketing_campaign_channels", "marketing_campaigns", "marketing_events", "marketing_release_assets", "marketing_release_channels", "marketing_release_plans")
	include("spyglass", "migration", "prototype_migration_events", "prototype_migration_receipts", "prototype_migration_runs")
	exclude("spyglass", Operational, "Route receipts and cleanup leases are short-lived anti-replay and routing controls.", "route_context_receipt_cleanup_queue", "route_context_receipts")
	include("spyglass", "schedules", "schedule_events", "schedule_occurrences", "schedule_triggers", "schedules")
	exclude("spyglass", Operational, "Schedule dispatch, trigger, and operator queues are transient execution controls.", "schedule_dispatch_queue", "schedule_queue_operator_events", "schedule_trigger_queue")
	include("spyglass", "work", "work_agent_executions", "work_item_events", "work_items")
	exclude("spyglass", Operational, "Work execution and capacity queues plus operator interventions are internal processing controls.", "work_agent_execution_queue", "work_capacity_release_operator_events", "work_capacity_release_queue")
	exclude("spyglass", Derived, "Work item numbers are present on exported Work items; allocator counters are reconstructed.", "work_item_number_counters")

	return NewRegistry(sections, tables)
}
