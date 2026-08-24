package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsingest"
)

type AnalyticsEventSink struct{ pool *pgxpool.Pool }

func NewAnalyticsEventSink(pool *pgxpool.Pool) *AnalyticsEventSink {
	return &AnalyticsEventSink{pool: pool}
}

func (s *AnalyticsEventSink) Append(ctx context.Context, accepted analyticsingest.AcceptedEvent) error {
	fields, err := json.Marshal(accepted.Envelope.Fields)
	if err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `
		INSERT INTO analytics_events
			(event_id,subject_id,consent_decision_id,event_name,surface,occurred_at,fields)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (event_id) DO NOTHING`, accepted.Envelope.ID, accepted.Envelope.SubjectID,
		accepted.ConsentDecisionID, accepted.Envelope.Name, accepted.Envelope.Surface,
		accepted.Envelope.OccurredAt, fields)
	if err != nil || command.RowsAffected() == 1 {
		return err
	}
	var subjectID, decisionID, name, surface string
	var occurredAt time.Time
	var storedFields []byte
	err = s.pool.QueryRow(ctx, `
		SELECT subject_id::text,consent_decision_id::text,event_name,surface,occurred_at,fields
		FROM analytics_events WHERE event_id=$1`, accepted.Envelope.ID).Scan(
		&subjectID, &decisionID, &name, &surface, &occurredAt, &storedFields)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("analytics event idempotency row disappeared")
	}
	if err != nil {
		return err
	}
	var stored map[string]string
	if json.Unmarshal(storedFields, &stored) != nil || subjectID != string(accepted.Envelope.SubjectID) ||
		decisionID != string(accepted.ConsentDecisionID) || name != string(accepted.Envelope.Name) ||
		surface != string(accepted.Envelope.Surface) || !occurredAt.Equal(accepted.Envelope.OccurredAt) || !equalDimensions(stored, accepted.Envelope.Fields) {
		return fmt.Errorf("analytics event idempotency conflict")
	}
	return nil
}

func equalDimensions(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

var _ analyticsingest.Sink = (*AnalyticsEventSink)(nil)
