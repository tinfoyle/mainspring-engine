package postgres

import "github.com/jackc/pgx/v5"

// AccountExportCellProjectionTables is assembled in reviewed feature cohorts.
// The Account/Work/Schedules cohort is complete here; remaining section
// cohorts are appended only with fresh-schema exported-or-omitted coverage.
func AccountExportCellProjectionTables(tx pgx.Tx) map[string][]AccountExportProjectionTable {
	result := map[string][]AccountExportProjectionTable{
		"account": {
			projectionTable(tx, "spyglass", "account_audit_events", "account_id", []string{"account_id", "id"},
				[]string{"account_id", "id", "event_type", "actor_kind", "actor_id", "correlation_id", "redacted_payload", "occurred_at"}, nil),
		},
		"attention": {
			projectionTable(tx, "spyglass", "attention_consequential_approvals", "account_id", []string{"account_id", "id"},
				[]string{"account_id", "id", "operation_id", "invocation_id", "work_item_id", "capability", "canonical_payload", "input_sha256", "hash_version", "evidence_sha256", "proposer_kind", "proposer_id", "policy_version", "require_independent_review", "expires_at", "state", "decision", "decision_reason", "decided_by_user_id", "decided_at", "canceled_by_kind", "canceled_by_id", "cancel_reason", "canceled_at", "invalidated_at", "expired_at", "version", "created_at", "updated_at"}, nil),
			projectionTable(tx, "spyglass", "attention_events", "account_id", []string{"account_id", "id"},
				[]string{"account_id", "id", "aggregate_kind", "information_request_id", "work_review_id", "consequential_approval_id", "event_type", "from_version", "to_version", "actor_kind", "actor_id", "reason", "correlation_id", "redacted_payload", "occurred_at"}, nil),
			projectionTable(tx, "spyglass", "attention_information_requests", "account_id", []string{"account_id", "id"},
				[]string{"account_id", "id", "parent_work_item_id", "fact_key", "scope_kind", "scope_work_item_id", "scope_conversation_id", "question", "requested_by_kind", "requested_by_id", "state", "fact_id", "fact_version", "answered_by_kind", "answered_by_id", "answered_at", "canceled_by_kind", "canceled_by_id", "reason", "version", "created_at", "updated_at"}, nil),
			projectionTable(tx, "spyglass", "attention_work_reviews", "account_id", []string{"account_id", "id"},
				[]string{"account_id", "id", "work_item_id", "work_version", "proposal_sha256", "question", "requested_by_kind", "requested_by_id", "reviewer_user_id", "state", "decision", "decision_reason", "decided_by_user_id", "decided_at", "canceled_by_kind", "canceled_by_id", "cancel_reason", "invalidated_at", "version", "created_at", "updated_at"}, nil),
		},
		"baseline": {
			projectionTable(tx, "spyglass", "baseline_assessments", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "catalog_version", "scope_policy_version", "state", "created_by_user_id", "superseded_by_assessment_id", "version", "created_at", "updated_at", "reassess_at"}, nil),
			projectionTable(tx, "spyglass", "baseline_events", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "assessment_id", "requirement_id", "plan_id", "event_type", "from_version", "to_version", "actor_user_id", "reason_code", "correlation_id", "redacted_payload", "occurred_at"}, nil),
			projectionTable(tx, "spyglass", "baseline_evidence_decisions", "account_id", []string{"account_id", "requirement_id", "evidence_id"}, []string{"account_id", "assessment_id", "requirement_id", "evidence_id", "decision", "reason", "decided_by_user_id", "decided_at"}, nil),
			projectionTable(tx, "spyglass", "baseline_interview_answers", "account_id", []string{"account_id", "assessment_id", "question_key"}, []string{"account_id", "assessment_id", "question_key", "answer_kind", "fact_id", "fact_revision", "reason", "answered_by_user_id", "answered_at"}, nil),
			projectionTable(tx, "spyglass", "baseline_plans", "account_id", []string{"account_id", "id"}, []string{"account_id", "assessment_id", "id", "assessment_version", "content_sha256", "proposed_work_count", "approved_by_user_id", "approved_at", "created_at"}, nil),
			projectionTable(tx, "spyglass", "baseline_plan_work", "account_id", []string{"account_id", "plan_id", "sequence"}, []string{"account_id", "plan_id", "sequence", "assessment_id", "requirement_id", "title", "description", "responsibility_kind", "responsible_user_id", "responsible_persona_id"}, nil),
			projectionTable(tx, "spyglass", "baseline_requirements", "account_id", []string{"account_id", "id"}, []string{"account_id", "assessment_id", "id", "requirement_code", "title", "responsibility_kind", "responsible_user_id", "responsible_persona_id", "renew_after_days", "catalog_version", "scope_policy_version", "disposition", "reason", "renew_at"}, nil),
			projectionTable(tx, "spyglass", "baseline_source_grants", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "assessment_id", "connection_id", "source_kind", "folders", "since_at", "until_at", "state", "granted_by_user_id", "revoked_by_user_id", "revoke_reason", "version", "created_at", "updated_at", "revoked_at"}, nil),
			projectionTable(tx, "spyglass", "baseline_source_grant_events", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "grant_id", "event_type", "from_version", "to_version", "actor_user_id", "reason_code", "correlation_id", "redacted_payload", "occurred_at"}, nil),
		},
		"finance": {
			projectionTable(tx, "spyglass", "finance_accounts", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "ledger_id", "parent_account_id", "code", "name", "description", "account_type", "normal_balance", "allow_posting", "state", "version", "created_by_kind", "created_by_id", "created_at", "updated_at"}, nil),
			projectionTable(tx, "spyglass", "finance_entries", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "ledger_id", "entry_number", "entry_date", "description", "reference", "currency", "total_minor", "source", "work_item_id", "run_id", "invocation_id", "state", "reversal_of_id", "reversed_by_id", "version", "created_by_kind", "created_by_id", "posted_by_user_id", "reversed_by_user_id", "created_at", "updated_at", "posted_at", "reversed_at"}, nil),
			projectionTable(tx, "spyglass", "finance_entry_evidence", "account_id", []string{"account_id", "entry_id", "evidence_id"}, []string{"account_id", "entry_id", "evidence_id"}, nil),
			projectionTable(tx, "spyglass", "finance_entry_lines", "account_id", []string{"account_id", "entry_id", "line_number"}, []string{"account_id", "entry_id", "line_number", "ledger_id", "posting_account_id", "memo", "debit_minor", "credit_minor"}, nil),
			projectionTable(tx, "spyglass", "finance_events", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "aggregate_kind", "aggregate_id", "event_type", "from_version", "to_version", "actor_kind", "actor_id", "correlation_id", "redacted_payload", "occurred_at"}, nil),
			projectionTable(tx, "spyglass", "finance_ledgers", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "name", "code", "description", "currency", "state", "closed_through", "version", "created_by_kind", "created_by_id", "created_at", "updated_at"}, nil),
			projectionTable(tx, "spyglass", "finance_ledger_close_evidence", "account_id", []string{"account_id", "ledger_id", "ledger_version", "evidence_id"}, []string{"account_id", "ledger_id", "ledger_version", "evidence_id", "closed_through", "created_at"}, nil),
			projectionTable(tx, "spyglass", "finance_reconciliations", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "ledger_id", "posting_account_id", "as_of", "currency", "statement_balance_minor", "ledger_balance_minor", "difference_minor", "primary_evidence_id", "state", "version", "created_by_user_id", "confirmed_by_user_id", "created_at", "updated_at", "confirmed_at"}, nil),
			projectionTable(tx, "spyglass", "finance_reconciliation_evidence", "account_id", []string{"account_id", "reconciliation_id", "evidence_id"}, []string{"account_id", "reconciliation_id", "evidence_id"}, nil),
		},
		"integrations": {
			projectionTable(tx, "spyglass", "integration_connections", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "name", "connector_kind", "state", "current_revision", "version", "created_by_user_id", "revoked_by_user_id", "created_at", "updated_at", "revoked_at"}, []string{"credential_id", "credential_generation"}),
			projectionTable(tx, "spyglass", "integration_connection_revisions", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "connection_id", "revision", "capabilities", "email_address", "audience_reference", "https_origin", "path_prefix", "created_by_user_id", "created_at"}, nil),
			projectionTable(tx, "spyglass", "integration_events", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "aggregate_kind", "aggregate_id", "event_type", "actor_kind", "actor_id", "correlation_id", "redacted_payload", "occurred_at"}, nil),
			projectionTable(tx, "spyglass", "integration_execution_attempts", "account_id", []string{"account_id", "id"}, []string{"account_id", "execution_id", "id", "attempt_number", "mode", "outcome", "error_code", "started_at", "completed_at"}, []string{"lease_expires_at"}),
			projectionTable(tx, "spyglass", "integration_execution_resolutions", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "execution_id", "requested_outcome", "evidence_sha256", "requested_by_user_id", "requested_at", "state", "confirmed_by_user_id", "confirmed_at"}, nil),
			projectionTable(tx, "spyglass", "integration_executions", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "release_id", "release_version", "approval_id", "capability", "connection_id", "connection_revision_id", "connection_revision", "payload_sha256", "state", "attempt_count", "last_error_code", "created_at", "updated_at", "completed_at"}, []string{"credential_id", "credential_generation", "current_attempt_id", "lease_expires_at", "next_attempt_at"}),
			projectionTable(tx, "spyglass", "integration_health_observations", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "connection_id", "connection_revision", "state", "error_code", "latency_milliseconds", "checked_at"}, []string{"credential_id", "credential_generation"}),
		},
		"migration": {
			projectionTable(tx, "spyglass", "prototype_migration_events", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "run_id", "event_type", "source_kind", "source_id", "target_kind", "target_id", "from_version", "to_version", "actor_id", "correlation_id", "occurred_at"}, nil),
			projectionTable(tx, "spyglass", "prototype_migration_receipts", "account_id", []string{"account_id", "run_id", "source_kind", "source_id", "target_kind"}, []string{"account_id", "run_id", "source_kind", "source_id", "target_kind", "target_id", "content_sha256", "created_at"}, nil),
			projectionTable(tx, "spyglass", "prototype_migration_runs", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "manifest_sha256", "source_tenant_id", "source_checkpoint", "source_inventory_sha256", "expected_documents", "expected_evidence", "expected_claims", "unresolved_records", "state", "reconciliation_sha256", "version", "created_at", "updated_at", "imported_at", "reconciled_at"}, nil),
		},
		"marketing": {
			projectionTable(tx, "spyglass", "marketing_assets", "account_id", []string{"account_id", "id"}, []string{"account_id", "campaign_id", "id", "created_at"}, nil),
			projectionTable(tx, "spyglass", "marketing_asset_revisions", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "campaign_id", "asset_id", "revision", "kind", "title", "media_type", "content_sha256", "content_bytes", "alternative_text", "created_by_kind", "created_by_id", "origin", "run_id", "invocation_id", "created_at"}, []string{"content_reference"}),
			projectionTable(tx, "spyglass", "marketing_campaigns", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "name", "objective", "audience", "state", "active_release_id", "version", "created_by_kind", "created_by_id", "origin", "run_id", "invocation_id", "created_at", "updated_at"}, nil),
			projectionTable(tx, "spyglass", "marketing_campaign_channels", "account_id", []string{"account_id", "campaign_id", "channel"}, []string{"account_id", "campaign_id", "channel"}, nil),
			projectionTable(tx, "spyglass", "marketing_events", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "aggregate_kind", "aggregate_id", "event_type", "from_version", "to_version", "actor_kind", "actor_id", "correlation_id", "redacted_payload", "occurred_at"}, nil),
			projectionTable(tx, "spyglass", "marketing_release_assets", "account_id", []string{"account_id", "release_id", "asset_revision_id"}, []string{"account_id", "campaign_id", "release_id", "asset_revision_id"}, nil),
			projectionTable(tx, "spyglass", "marketing_release_channels", "account_id", []string{"account_id", "release_id", "channel"}, []string{"account_id", "campaign_id", "release_id", "channel"}, nil),
			projectionTable(tx, "spyglass", "marketing_release_plans", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "campaign_id", "campaign_version", "name", "state", "approval_id", "version", "created_by_kind", "created_by_id", "origin", "run_id", "invocation_id", "submitted_by_user_id", "approved_by_user_id", "created_at", "updated_at"}, nil),
		},
		"schedules": {
			projectionTable(tx, "spyglass", "schedule_events", "account_id", []string{"account_id", "id"},
				[]string{"account_id", "id", "schedule_id", "event_type", "from_version", "to_version", "actor_kind", "actor_id", "reason", "correlation_id", "redacted_payload", "occurred_at"}, nil),
			projectionTable(tx, "spyglass", "schedule_occurrences", "account_id", []string{"account_id", "id"},
				[]string{"account_id", "id", "schedule_id", "schedule_version", "scheduled_for", "outcome", "run_id", "conversation_id", "created_by_user_id", "initiated_by_kind", "initiated_by_id", "occurred_at", "occurrence_kind", "trigger_id", "requested_by_user_id"}, nil),
			projectionTable(tx, "spyglass", "schedule_triggers", "account_id", []string{"account_id", "id"},
				[]string{"account_id", "id", "schedule_id", "schedule_version", "requested_by_user_id", "requested_at"}, nil),
			projectionTable(tx, "spyglass", "schedules", "account_id", []string{"account_id", "id"},
				[]string{"account_id", "id", "name", "timezone", "recurrence", "missed_run_policy", "execution_template", "state", "version", "next_run_at", "created_by_user_id", "created_at", "updated_at"}, nil),
		},
		"work": {
			projectionTable(tx, "spyglass", "work_agent_executions", "account_id", []string{"account_id", "execution_id"},
				[]string{"account_id", "execution_id", "work_item_id", "work_version", "initiating_user_id", "persona_id", "persona_version_id", "boardroom_id", "boardroom_version", "planned_run_id", "planned_conversation_id", "title", "description", "linked_run_id", "linked_conversation_id", "queued_at", "linked_at", "updated_at"}, nil),
			projectionTable(tx, "spyglass", "work_item_events", "account_id", []string{"account_id", "id"},
				[]string{"account_id", "id", "work_item_id", "event_type", "from_version", "to_version", "actor_kind", "actor_id", "reason", "correlation_id", "redacted_payload", "occurred_at"}, nil),
			projectionTable(tx, "spyglass", "work_items", "account_id", []string{"account_id", "id"},
				[]string{"account_id", "id", "number", "parent_id", "depth", "kind", "title", "description", "state", "priority", "responsibility", "assignee_user_id", "assignee_persona_id", "external_assignee_ref", "source", "created_by_actor_kind", "created_by_actor_id", "baseline_requirement_id", "schedule_id", "conversation_id", "run_id", "due_at", "completed_at", "capacity_released_at", "version", "created_at", "updated_at"},
				[]string{"capacity_reservation_id"}),
		},
	}
	sortAccountExportProjectionTables(result)
	return result
}
