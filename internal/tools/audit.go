package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresAuditor struct {
	pool *pgxpool.Pool
}

func NewPostgresAuditor(pool *pgxpool.Pool) *PostgresAuditor {
	return &PostgresAuditor{pool: pool}
}

func (a *PostgresAuditor) RecordToolCall(ctx context.Context, record AuditRecord) error {
	payload, err := json.Marshal(map[string]any{
		"capability": record.Capability,
		"allowed":    record.Allowed,
		"error":      record.Error,
	})
	if err != nil {
		return err
	}
	eventType := "tool.authorized"
	if !record.Allowed {
		eventType = "tool.denied"
	}
	_, err = a.pool.Exec(ctx, `
		INSERT INTO tenant_audit_events (actor_type, actor_id, run_id, event_type, correlation_id, payload, created_at)
		VALUES ('persona', $1, $2, $3, $4, $5, $6)
	`, record.PersonaID, record.RunID, eventType, record.InvocationID, payload, record.CreatedAt)
	if err != nil {
		return fmt.Errorf("record tool audit event: %w", err)
	}
	return nil
}
