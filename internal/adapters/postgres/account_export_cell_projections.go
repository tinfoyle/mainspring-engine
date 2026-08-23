package postgres

import "github.com/jackc/pgx/v5"

// AccountExportCellProjectionTables is assembled in reviewed feature cohorts.
// The Account/Work/Schedules cohort is complete here; remaining section
// cohorts are appended only with fresh-schema exported-or-omitted coverage.
func AccountExportCellProjectionTables(tx pgx.Tx) map[string][]AccountExportProjectionTable {
	return map[string][]AccountExportProjectionTable{
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
		"migration": {
			projectionTable(tx, "spyglass", "prototype_migration_events", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "run_id", "event_type", "source_kind", "source_id", "target_kind", "target_id", "from_version", "to_version", "actor_id", "correlation_id", "occurred_at"}, nil),
			projectionTable(tx, "spyglass", "prototype_migration_receipts", "account_id", []string{"account_id", "run_id", "source_kind", "source_id", "target_kind"}, []string{"account_id", "run_id", "source_kind", "source_id", "target_kind", "target_id", "content_sha256", "created_at"}, nil),
			projectionTable(tx, "spyglass", "prototype_migration_runs", "account_id", []string{"account_id", "id"}, []string{"account_id", "id", "manifest_sha256", "source_tenant_id", "source_checkpoint", "source_inventory_sha256", "expected_documents", "expected_evidence", "expected_claims", "unresolved_records", "state", "reconciliation_sha256", "version", "created_at", "updated_at", "imported_at", "reconciled_at"}, nil),
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
}
