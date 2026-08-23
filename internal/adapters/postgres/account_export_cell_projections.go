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
