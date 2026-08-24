package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/privacyconsent"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type PrivacyConsentRepository struct{ pool *pgxpool.Pool }

func NewPrivacyConsentRepository(pool *pgxpool.Pool) *PrivacyConsentRepository {
	return &PrivacyConsentRepository{pool: pool}
}

func (r *PrivacyConsentRepository) Append(ctx context.Context, decision privacy.Decision) error {
	if err := decision.Validate(); err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO privacy_consent_subjects (id,created_at)
		VALUES ($1,$2) ON CONFLICT (id) DO NOTHING`, decision.SubjectID, decision.EffectiveAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO privacy_consent_decisions
			(decision_id,subject_id,policy_version,surface,analytics,marketing,effective_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, decision.ID, decision.SubjectID, decision.PolicyVersion,
		decision.Surface, decision.Analytics, decision.Marketing, decision.EffectiveAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PrivacyConsentRepository) Current(ctx context.Context, subjectID ids.ConsentSubjectID, surface privacy.Surface) (privacy.Decision, error) {
	var value privacy.Decision
	err := r.pool.QueryRow(ctx, `
		SELECT decision_id,subject_id,policy_version,surface,analytics,marketing,effective_at
		FROM privacy_consent_decisions
		WHERE subject_id=$1 AND surface=$2
		ORDER BY effective_at DESC,decision_id DESC LIMIT 1`, subjectID, surface).Scan(
		&value.ID, &value.SubjectID, &value.PolicyVersion, &value.Surface, &value.Analytics, &value.Marketing, &value.EffectiveAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return privacy.Decision{}, privacyconsent.ErrNotFound
	}
	return value, err
}

func (r *PrivacyConsentRepository) History(ctx context.Context, subjectID ids.ConsentSubjectID, limit int) ([]privacy.Decision, error) {
	if limit < 1 || limit > 1000 {
		return nil, privacy.ErrInvalidDecision
	}
	rows, err := r.pool.Query(ctx, `
		SELECT decision_id,subject_id,policy_version,surface,analytics,marketing,effective_at
		FROM privacy_consent_decisions WHERE subject_id=$1
		ORDER BY effective_at DESC,decision_id DESC LIMIT $2`, subjectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]privacy.Decision, 0)
	for rows.Next() {
		var value privacy.Decision
		if err := rows.Scan(&value.ID, &value.SubjectID, &value.PolicyVersion, &value.Surface,
			&value.Analytics, &value.Marketing, &value.EffectiveAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *PrivacyConsentRepository) Erase(ctx context.Context, subjectID ids.ConsentSubjectID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('spyglass.privacy_erasure_subject_id',$1,true)`, subjectID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM privacy_consent_subjects WHERE id=$1`, subjectID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var _ privacyconsent.Repository = (*PrivacyConsentRepository)(nil)
