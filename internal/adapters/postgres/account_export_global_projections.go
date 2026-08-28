package postgres

import "github.com/jackc/pgx/v5"

// AccountExportGlobalProjectionTables is the reviewed global-database
// portability allowlist. Every physical column is either exported in Columns
// or intentionally withheld in OmittedColumns; the fresh-schema certificate
// enforces that union exactly.
func AccountExportGlobalProjectionTables(tx pgx.Tx) map[string][]AccountExportProjectionTable {
	result := map[string][]AccountExportProjectionTable{
		"affiliate": {
			projectionTable(tx, "public", "affiliate_attributions", "referred_account_id", []string{"attribution_id"},
				[]string{"attribution_id", "referred_account_id", "checkout_request_id", "offer_code", "offer_version", "state", "version", "created_at", "locked_at"},
				[]string{"affiliate_id", "rule_version", "provider_subscription_id"}),
		},
		"account": {
			projectionTable(tx, "public", "account_closure_requests", "account_id", []string{"id"},
				[]string{"id", "account_id", "state", "requested_by_user_id", "reason", "account_version", "requested_at", "execute_after", "blocker_code", "canceled_by_user_id", "cancel_reason", "canceled_at", "closed_at", "delete_after"},
				[]string{"next_attempt_at", "attempt_count", "lease_expires_at"}),
			projectionTable(tx, "public", "account_lifecycle_events", "account_id", []string{"id"},
				[]string{"id", "account_id", "closure_request_id", "action", "from_state", "to_state", "actor_kind", "actor_id", "reason", "blocker_code", "occurred_at"}, nil),
			projectionTable(tx, "public", "account_membership_events", "account_id", []string{"id"},
				[]string{"id", "account_id", "actor_user_id", "action", "target_membership_id", "previous_owner_membership_id", "previous_role", "new_role", "reason", "occurred_at", "previous_state", "new_state"}, nil),
			projectionTable(tx, "public", "accounts", "id", []string{"id"},
				[]string{"id", "slug", "display_name", "account_type", "state", "cell_id", "placement_generation", "entitlement_version", "created_by_user_id", "created_at", "last_catalog_reconciled_version", "version"}, nil),
			projectionTable(tx, "public", "invitations", "account_id", []string{"id"},
				[]string{"id", "account_id", "email", "role", "state", "invited_by_user_id", "expires_at", "created_at", "accepted_at", "revoked_at"}, []string{"token_hash"}),
			projectionTable(tx, "public", "memberships", "account_id", []string{"id"},
				[]string{"id", "account_id", "user_id", "role", "state", "version", "created_at"}, nil),
			projectionTable(tx, "public", "operations_access_events", "account_id", []string{"id"},
				[]string{"id", "action", "support_grant_id", "target_user_id", "account_id", "ticket", "reason", "environment", "occurred_at"},
				[]string{"staff_user_id", "session_id", "details"}),
			projectionTable(tx, "public", "operations_support_grants", "account_id", []string{"id"},
				[]string{"id", "target_user_id", "account_id", "state", "ticket", "reason", "created_at", "expires_at", "revoked_at", "version"},
				[]string{"staff_user_id"}),
		},
		"billing": {
			projectionTable(tx, "public", "account_subscription_lifecycles", "account_id", []string{"lifecycle_id"},
				[]string{"lifecycle_id", "account_id", "trigger_kind", "state", "effective_at", "restriction_at", "delete_at", "version", "created_at", "updated_at", "recovered_at", "closed_at"},
				[]string{"provider_subscription_id", "next_attempt_at", "lease_id", "lease_expires_at"}),
			projectionTable(tx, "public", "ai_token_grants", "account_id", []string{"id"},
				[]string{"id", "account_id", "origin", "definition_code", "catalog_version", "quantity", "available", "reserved", "consumed", "state", "expires_at", "created_at"},
				[]string{"source_reference"}),
			projectionTable(tx, "public", "ai_token_ledger_entries", "account_id", []string{"id"},
				[]string{"id", "account_id", "grant_id", "kind", "amount", "created_at"},
				[]string{"reservation_id", "event_key"}),
			projectionTable(tx, "public", "billing_checkout_attempts", "account_id", []string{"request_id"},
				[]string{"request_id", "account_id", "provider", "mode", "purchase_kind", "offer_code", "item_version", "catalog_version", "currency", "amount_minor", "quantity", "commissioning_code", "commissioning_version", "commissioning_catalog_version", "state", "expires_at", "created_at", "updated_at"},
				[]string{"provider_session_id", "provider_payment_intent_id", "hosted_url"}),
			projectionTable(tx, "public", "account_commissioning_purchases", "account_id", []string{"account_id"},
				[]string{"account_id", "checkout_request_id", "item_code", "item_version", "catalog_version", "amount_minor", "currency", "purchased_at"},
				[]string{"provider_reference"}),
			projectionTable(tx, "public", "billing_profiles", "account_id", []string{"account_id"},
				[]string{"account_id", "billing_email", "version", "created_at", "updated_at"}, []string{"stripe_customer_id"}),
			projectionTable(tx, "public", "subscriptions", "account_id", []string{"id"},
				[]string{"id", "account_id", "provider", "state", "offer_code", "offer_version", "current_period_start", "current_period_end", "cancel_at", "last_synced_at", "created_at", "updated_at", "provider_mode"},
				[]string{"provider_subscription_id", "provider_object_version", "provider_customer_id"}),
		},
		"entitlements": {
			projectionTable(tx, "public", "entitlement_grants", "account_id", []string{"id"},
				[]string{"id", "account_id", "package_code", "package_version", "mode", "source", "limits", "starts_at", "ends_at", "priority", "reason", "created_at"}, []string{"source_reference"}),
			projectionTable(tx, "public", "entitlement_snapshots", "account_id", []string{"account_id", "version"},
				[]string{"account_id", "version", "catalog_version", "evaluated_at", "source_hash", "effective_packages"}, nil),
			projectionTable(tx, "public", "entitlement_usage_counters", "account_id", []string{"account_id", "package_code", "limit_code"},
				[]string{"account_id", "package_code", "limit_code", "current_value", "version", "updated_at"}, nil),
		},
	}
	sortAccountExportProjectionTables(result)
	return result
}

func projectionTable(tx pgx.Tx, schema, table, accountColumn string, keyColumns, columns, omitted []string) AccountExportProjectionTable {
	return AccountExportProjectionTable{Tx: tx, Schema: schema, Table: table, AccountColumn: accountColumn,
		KeyColumns: keyColumns, Columns: columns, OmittedColumns: omitted}
}
