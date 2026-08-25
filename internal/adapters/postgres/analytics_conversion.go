package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsconversion"
	"github.com/tinfoyle/spyglass-engine/internal/modules/analytics"
)

type AnalyticsConversionRepository struct{ pool *pgxpool.Pool }

func NewAnalyticsConversionRepository(pool *pgxpool.Pool) *AnalyticsConversionRepository {
	return &AnalyticsConversionRepository{pool: pool}
}

func (r *AnalyticsConversionRepository) Append(ctx context.Context, handoff analytics.HandoffReference, envelope analytics.Envelope) error {
	fields, err := json.Marshal(envelope.Fields)
	if err != nil {
		return err
	}
	command, err := r.pool.Exec(ctx, `
		INSERT INTO analytics_conversion_events
			(receipt_event_id,source_subject_id,source_consent_decision_id,event_name,occurred_at,fields)
		SELECT e.event_id,e.subject_id,e.consent_decision_id,$3,$4,$5
		FROM analytics_events e
		WHERE e.event_id=$1 AND e.subject_id=$2
		  AND e.event_name='signup_handoff_started' AND e.surface='public'
		  AND EXISTS (
			SELECT 1 FROM privacy_consent_decisions current_decision
			WHERE current_decision.decision_id=(
				SELECT latest.decision_id FROM privacy_consent_decisions latest
				WHERE latest.subject_id=e.subject_id AND latest.surface='public'
				ORDER BY latest.effective_at DESC,latest.decision_id DESC LIMIT 1
			) AND current_decision.analytics
		  )
		ON CONFLICT (receipt_event_id,event_name) DO NOTHING`, handoff.ReceiptEventID, handoff.SubjectID,
		envelope.Name, envelope.OccurredAt, fields)
	if err != nil || command.RowsAffected() == 1 {
		return err
	}
	var validSource bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM analytics_events e WHERE e.event_id=$1 AND e.subject_id=$2
		AND e.event_name='signup_handoff_started' AND e.surface='public'
		AND EXISTS (
			SELECT 1 FROM privacy_consent_decisions current_decision
			WHERE current_decision.decision_id=(
				SELECT latest.decision_id FROM privacy_consent_decisions latest
				WHERE latest.subject_id=e.subject_id AND latest.surface='public'
				ORDER BY latest.effective_at DESC,latest.decision_id DESC LIMIT 1
			) AND current_decision.analytics
		))`, handoff.ReceiptEventID, handoff.SubjectID).Scan(&validSource); err != nil {
		return fmt.Errorf("verify analytics conversion source: %w", err)
	}
	if !validSource {
		return analyticsconversion.ErrInvalidHandoff
	}
	return nil
}

var _ analyticsconversion.Repository = (*AnalyticsConversionRepository)(nil)
