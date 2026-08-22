package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	baselineapp "github.com/tinfoyle/spyglass-engine/internal/application/baseline"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type BaselineRepository struct{ cell *database.CellPool }

func NewBaselineRepository(cell *database.CellPool) (*BaselineRepository, error) {
	if cell == nil {
		return nil, errors.New("Baseline cell pool is required")
	}
	return &BaselineRepository{cell: cell}, nil
}

func (r *BaselineRepository) Create(ctx context.Context, assessment domain.Assessment, mutation baselineapp.Mutation) (domain.Assessment, error) {
	assessment, err := domain.RestoreAssessment(assessment)
	if err != nil || assessment.State != domain.StateInterview || assessment.Version != 1 || len(assessment.Answers) != 0 || len(assessment.Requirements) != 0 || assessment.Plan != nil || !mutation.Valid() || !assessment.CreatedAt.Equal(mutation.At) {
		return domain.Assessment{}, baselineapp.ErrInvalid
	}
	var result domain.Assessment
	err = r.cell.WithAccountTx(ctx, assessment.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, err := loadBaselineAssessment(ctx, tx, assessment.AccountID, assessment.ID, false)
		if err == nil {
			if !sameNewBaselineAssessment(existing, assessment) {
				return baselineapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(err, baselineapp.ErrNotFound) {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.baseline_assessments(account_id,id,catalog_version,scope_policy_version,state,created_by_user_id,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, assessment.AccountID, assessment.ID, assessment.CatalogVersion, assessment.ScopePolicyVersion, assessment.State, assessment.CreatedBy.UserID, assessment.Version, assessment.CreatedAt, assessment.UpdatedAt)
		if err != nil {
			return err
		}
		if err := insertBaselineEvent(ctx, tx, assessment, baselineapp.Transition{}, "assessment_started", 0, 1, mutation); err != nil {
			return err
		}
		result = assessment
		return nil
	})
	return result, classifyBaseline(err)
}

func (r *BaselineRepository) Get(ctx context.Context, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID) (domain.Assessment, error) {
	var result domain.Assessment
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := loadBaselineAssessment(ctx, tx, accountID, assessmentID, false)
		result = value
		return err
	})
	return result, classifyBaseline(err)
}

func (r *BaselineRepository) Update(ctx context.Context, updated domain.Assessment, expected uint64, transition baselineapp.Transition, mutation baselineapp.Mutation) (domain.Assessment, error) {
	updated, err := domain.RestoreAssessment(updated)
	if err != nil || updated.Version != expected+1 || !transition.Valid() || !mutation.Valid() || !updated.UpdatedAt.Equal(mutation.At) {
		return domain.Assessment{}, baselineapp.ErrInvalid
	}
	err = r.cell.WithAccountTx(ctx, updated.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadBaselineAssessment(ctx, tx, updated.AccountID, updated.ID, true)
		if err != nil {
			return err
		}
		if current.Version != expected {
			return baselineapp.ErrConflict
		}
		if err := validateBaselineTransition(current, updated, transition); err != nil {
			return err
		}
		if err := persistBaselineChildren(ctx, tx, current, updated); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `UPDATE spyglass.baseline_assessments SET state=$3,superseded_by_assessment_id=NULL,version=$4,updated_at=$5
			WHERE account_id=$1 AND id=$2 AND version=$6`, updated.AccountID, updated.ID, updated.State, updated.Version, updated.UpdatedAt, expected)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return baselineapp.ErrConflict
		}
		return insertBaselineEvent(ctx, tx, updated, transition, transition.EventType, expected, updated.Version, mutation)
	})
	if err != nil {
		return domain.Assessment{}, classifyBaseline(err)
	}
	return updated, nil
}

func (r *BaselineRepository) Reassess(ctx context.Context, archived, next domain.Assessment, expected uint64, mutation baselineapp.Mutation) (domain.Assessment, domain.Assessment, error) {
	archived, archivedErr := domain.RestoreAssessment(archived)
	next, nextErr := domain.RestoreAssessment(next)
	if archivedErr != nil || nextErr != nil || !mutation.Valid() || archived.Version != expected+1 || archived.State != domain.StateArchived || next.State != domain.StateInterview || next.Version != 1 || archived.AccountID != next.AccountID || archived.SupersededBy != next.ID || !archived.UpdatedAt.Equal(mutation.At) || !next.CreatedAt.Equal(mutation.At) {
		return domain.Assessment{}, domain.Assessment{}, baselineapp.ErrInvalid
	}
	err := r.cell.WithAccountTx(ctx, archived.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadBaselineAssessment(ctx, tx, archived.AccountID, archived.ID, true)
		if err != nil {
			return err
		}
		if current.Version != expected || current.State != domain.StateReady || !sameBaselineIdentity(current, archived) {
			return baselineapp.ErrConflict
		}
		result, err := tx.Exec(ctx, `UPDATE spyglass.baseline_assessments SET state='archived',superseded_by_assessment_id=$3,version=$4,updated_at=$5
			WHERE account_id=$1 AND id=$2 AND version=$6`, archived.AccountID, archived.ID, archived.SupersededBy, archived.Version, archived.UpdatedAt, expected)
		if err != nil || result.RowsAffected() != 1 {
			if err == nil {
				err = baselineapp.ErrConflict
			}
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.baseline_assessments(account_id,id,catalog_version,scope_policy_version,state,created_by_user_id,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, next.AccountID, next.ID, next.CatalogVersion, next.ScopePolicyVersion, next.State, next.CreatedBy.UserID, next.Version, next.CreatedAt, next.UpdatedAt)
		if err != nil {
			return err
		}
		if err := insertBaselineEvent(ctx, tx, archived, baselineapp.Transition{}, "assessment_archived", expected, archived.Version, mutation); err != nil {
			return err
		}
		return insertBaselineEvent(ctx, tx, next, baselineapp.Transition{}, "assessment_started", 0, 1, mutation)
	})
	if err != nil {
		return domain.Assessment{}, domain.Assessment{}, classifyBaseline(err)
	}
	return archived, next, nil
}

func loadBaselineAssessment(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID, lock bool) (domain.Assessment, error) {
	var result domain.Assessment
	var superseded *string
	query := `SELECT account_id,id,catalog_version,scope_policy_version,state,created_by_user_id,superseded_by_assessment_id,version,created_at,updated_at
		FROM spyglass.baseline_assessments WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, query, accountID, assessmentID).Scan(&result.AccountID, &result.ID, &result.CatalogVersion, &result.ScopePolicyVersion, &result.State, &result.CreatedBy.UserID, &superseded, &result.Version, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Assessment{}, baselineapp.ErrNotFound
	}
	if err != nil {
		return domain.Assessment{}, err
	}
	if superseded != nil {
		result.SupersededBy = ids.BaselineAssessmentID(*superseded)
	}
	answers, err := loadBaselineAnswers(ctx, tx, accountID, assessmentID)
	if err != nil {
		return domain.Assessment{}, err
	}
	result.Answers = answers
	requirements, err := loadBaselineRequirements(ctx, tx, accountID, assessmentID)
	if err != nil {
		return domain.Assessment{}, err
	}
	result.Requirements = requirements
	plan, err := loadBaselinePlan(ctx, tx, accountID, assessmentID)
	if err != nil {
		return domain.Assessment{}, err
	}
	result.Plan = plan
	result, err = domain.RestoreAssessment(result)
	if err != nil {
		return domain.Assessment{}, baselineapp.ErrRepository
	}
	return result, nil
}

func loadBaselineAnswers(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID) ([]domain.InterviewAnswer, error) {
	rows, err := tx.Query(ctx, `SELECT question_key,answer_kind,fact_id,fact_revision,reason,answered_by_user_id,answered_at
		FROM spyglass.baseline_interview_answers WHERE account_id=$1 AND assessment_id=$2 ORDER BY question_key`, accountID, assessmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.InterviewAnswer, 0)
	for rows.Next() {
		var answer domain.InterviewAnswer
		var factID *string
		var factRevision *uint64
		if err := rows.Scan(&answer.QuestionKey, &answer.Kind, &factID, &factRevision, &answer.Reason, &answer.AnsweredBy.UserID, &answer.AnsweredAt); err != nil {
			return nil, err
		}
		if factID != nil && factRevision != nil {
			answer.Fact = &domain.FactReference{FactID: ids.KnowledgeFactID(*factID), Revision: *factRevision}
		}
		result = append(result, answer)
	}
	return result, rows.Err()
}

func loadBaselineRequirements(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID) ([]domain.Requirement, error) {
	rows, err := tx.Query(ctx, `SELECT id,requirement_code,title,responsibility_kind,responsible_user_id,responsible_persona_id,renew_after_days,catalog_version,scope_policy_version,disposition,reason,renew_at
		FROM spyglass.baseline_requirements WHERE account_id=$1 AND assessment_id=$2 ORDER BY requirement_code,id`, accountID, assessmentID)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Requirement, 0)
	for rows.Next() {
		var requirement domain.Requirement
		var userID, personaID *string
		if err := rows.Scan(&requirement.ID, &requirement.Code, &requirement.Title, &requirement.Responsibility.Kind, &userID, &personaID, &requirement.RenewAfterDays, &requirement.CatalogVersion, &requirement.ScopePolicyVersion, &requirement.Disposition, &requirement.Reason, &requirement.RenewAt); err != nil {
			return nil, err
		}
		if userID != nil {
			requirement.Responsibility.ID = *userID
		} else if personaID != nil {
			requirement.Responsibility.ID = *personaID
		}
		result = append(result, requirement)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for index := range result {
		decisions, err := loadBaselineEvidence(ctx, tx, accountID, assessmentID, result[index].ID)
		if err != nil {
			return nil, err
		}
		result[index].Evidence = decisions
	}
	return result, nil
}

func loadBaselineEvidence(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID, requirementID ids.BaselineRequirementID) ([]domain.EvidenceDecision, error) {
	rows, err := tx.Query(ctx, `SELECT evidence_id,decision,reason,decided_by_user_id,decided_at FROM spyglass.baseline_evidence_decisions
		WHERE account_id=$1 AND assessment_id=$2 AND requirement_id=$3 ORDER BY decided_at,evidence_id`, accountID, assessmentID, requirementID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.EvidenceDecision, 0)
	for rows.Next() {
		var decision domain.EvidenceDecision
		if err := rows.Scan(&decision.EvidenceID, &decision.Decision, &decision.Reason, &decision.DecidedBy.UserID, &decision.DecidedAt); err != nil {
			return nil, err
		}
		result = append(result, decision)
	}
	return result, rows.Err()
}

func loadBaselinePlan(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID) (*domain.PlanBinding, error) {
	var plan domain.PlanBinding
	var digest []byte
	var approvedBy *string
	err := tx.QueryRow(ctx, `SELECT id,assessment_version,content_sha256,proposed_work_count,approved_by_user_id,approved_at
		FROM spyglass.baseline_plans WHERE account_id=$1 AND assessment_id=$2`, accountID, assessmentID).Scan(&plan.ID, &plan.AssessmentVersion, &digest, &plan.ProposedWorkCount, &approvedBy, &plan.ApprovedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(digest) != sha256.Size {
		return nil, baselineapp.ErrRepository
	}
	copy(plan.ContentSHA256[:], digest)
	if approvedBy != nil {
		plan.ApprovedBy = &domain.Actor{UserID: ids.UserID(*approvedBy)}
	}
	return &plan, nil
}

func persistBaselineChildren(ctx context.Context, tx pgx.Tx, current, updated domain.Assessment) error {
	currentAnswers := make(map[string]domain.InterviewAnswer, len(current.Answers))
	for _, answer := range current.Answers {
		currentAnswers[answer.QuestionKey] = answer
	}
	for _, answer := range updated.Answers {
		if existing, exists := currentAnswers[answer.QuestionKey]; exists && reflect.DeepEqual(existing, answer) {
			continue
		}
		var factID, factRevision any
		if answer.Fact != nil {
			factID, factRevision = answer.Fact.FactID, answer.Fact.Revision
		}
		_, err := tx.Exec(ctx, `INSERT INTO spyglass.baseline_interview_answers(account_id,assessment_id,question_key,answer_kind,fact_id,fact_revision,reason,answered_by_user_id,answered_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (account_id,assessment_id,question_key) DO UPDATE SET answer_kind=EXCLUDED.answer_kind,fact_id=EXCLUDED.fact_id,fact_revision=EXCLUDED.fact_revision,reason=EXCLUDED.reason,answered_by_user_id=EXCLUDED.answered_by_user_id,answered_at=EXCLUDED.answered_at`,
			updated.AccountID, updated.ID, answer.QuestionKey, answer.Kind, factID, factRevision, answer.Reason, answer.AnsweredBy.UserID, answer.AnsweredAt)
		if err != nil {
			return err
		}
	}
	currentRequirements := make(map[ids.BaselineRequirementID]domain.Requirement, len(current.Requirements))
	for _, requirement := range current.Requirements {
		currentRequirements[requirement.ID] = requirement
	}
	for _, requirement := range updated.Requirements {
		existing, exists := currentRequirements[requirement.ID]
		if !exists {
			userID, personaID := responsibilityColumns(requirement.Responsibility)
			_, err := tx.Exec(ctx, `INSERT INTO spyglass.baseline_requirements(account_id,assessment_id,id,requirement_code,title,responsibility_kind,responsible_user_id,responsible_persona_id,renew_after_days,catalog_version,scope_policy_version,disposition,reason,renew_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, updated.AccountID, updated.ID, requirement.ID, requirement.Code, requirement.Title, requirement.Responsibility.Kind, userID, personaID, requirement.RenewAfterDays, requirement.CatalogVersion, requirement.ScopePolicyVersion, requirement.Disposition, requirement.Reason, requirement.RenewAt)
			if err != nil {
				return err
			}
		} else {
			result, err := tx.Exec(ctx, `UPDATE spyglass.baseline_requirements SET disposition=$4,reason=$5,renew_at=$6
				WHERE account_id=$1 AND assessment_id=$2 AND id=$3`, updated.AccountID, updated.ID, requirement.ID, requirement.Disposition, requirement.Reason, requirement.RenewAt)
			if err != nil || result.RowsAffected() != 1 {
				if err == nil {
					err = baselineapp.ErrConflict
				}
				return err
			}
			for _, decision := range requirement.Evidence {
				if evidenceDecisionExists(existing.Evidence, decision.EvidenceID) {
					continue
				}
				_, err := tx.Exec(ctx, `INSERT INTO spyglass.baseline_evidence_decisions(account_id,assessment_id,requirement_id,evidence_id,decision,reason,decided_by_user_id,decided_at)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, updated.AccountID, updated.ID, requirement.ID, decision.EvidenceID, decision.Decision, decision.Reason, decision.DecidedBy.UserID, decision.DecidedAt)
				if err != nil {
					return err
				}
			}
		}
	}
	if current.Plan == nil && updated.Plan != nil {
		_, err := tx.Exec(ctx, `INSERT INTO spyglass.baseline_plans(account_id,assessment_id,id,assessment_version,content_sha256,proposed_work_count,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, updated.AccountID, updated.ID, updated.Plan.ID, updated.Plan.AssessmentVersion, updated.Plan.ContentSHA256[:], updated.Plan.ProposedWorkCount, updated.UpdatedAt)
		if err != nil {
			return err
		}
	} else if current.Plan != nil && updated.Plan != nil && current.Plan.ApprovedAt == nil && updated.Plan.ApprovedAt != nil {
		result, err := tx.Exec(ctx, `UPDATE spyglass.baseline_plans SET approved_by_user_id=$4,approved_at=$5
			WHERE account_id=$1 AND assessment_id=$2 AND id=$3 AND approved_at IS NULL`, updated.AccountID, updated.ID, updated.Plan.ID, updated.Plan.ApprovedBy.UserID, updated.Plan.ApprovedAt)
		if err != nil || result.RowsAffected() != 1 {
			if err == nil {
				err = baselineapp.ErrConflict
			}
			return err
		}
	}
	return nil
}

func validateBaselineTransition(current, updated domain.Assessment, transition baselineapp.Transition) error {
	if !sameBaselineIdentity(current, updated) || updated.SupersededBy != "" {
		return baselineapp.ErrConflict
	}
	states := map[string][2]domain.AssessmentState{
		"interview_answered":        {domain.StateInterview, domain.StateInterview},
		"inventory_started":         {domain.StateInterview, domain.StateInventory},
		"inventory_completed":       {domain.StateInventory, domain.StateGapReview},
		"evidence_decided":          {domain.StateGapReview, domain.StateGapReview},
		"requirement_dispositioned": {domain.StateGapReview, domain.StateGapReview},
		"plan_submitted":            {domain.StateGapReview, domain.StatePlanApproval},
		"plan_approved":             {domain.StatePlanApproval, domain.StateActive},
		"assessment_ready":          {domain.StateActive, domain.StateReady},
	}
	expected, exists := states[transition.EventType]
	if !exists || current.State != expected[0] || updated.State != expected[1] {
		return baselineapp.ErrConstraint
	}
	if transition.RequirementID != "" && requirementByID(updated.Requirements, transition.RequirementID) == nil {
		return baselineapp.ErrConstraint
	}
	if transition.PlanID != "" && (updated.Plan == nil || updated.Plan.ID != transition.PlanID) {
		return baselineapp.ErrConstraint
	}
	return nil
}

func insertBaselineEvent(ctx context.Context, tx pgx.Tx, assessment domain.Assessment, transition baselineapp.Transition, eventType string, from, to uint64, mutation baselineapp.Mutation) error {
	eventID, err := ids.Derive(mutation.CorrelationID, fmt.Sprintf("baseline-%s-%s", eventType, assessment.ID))
	if err != nil {
		return baselineapp.ErrInvalid
	}
	var requirementID, planID any
	if transition.RequirementID != "" {
		requirementID = transition.RequirementID
	}
	if transition.PlanID != "" {
		planID = transition.PlanID
	}
	payload := map[string]any{"state": assessment.State, "answer_count": len(assessment.Answers), "requirement_count": len(assessment.Requirements)}
	if requirement := requirementByID(assessment.Requirements, transition.RequirementID); requirement != nil {
		payload["disposition"] = requirement.Disposition
		payload["evidence_count"] = len(requirement.Evidence)
	}
	_, err = tx.Exec(ctx, `INSERT INTO spyglass.baseline_events(account_id,id,assessment_id,requirement_id,plan_id,event_type,from_version,to_version,actor_user_id,reason_code,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT (account_id,id) DO NOTHING`, assessment.AccountID, eventID, assessment.ID, requirementID, planID, eventType, from, to, mutation.Actor.UserID, mutation.ReasonCode, mutation.CorrelationID, payload, mutation.At)
	return err
}

func sameBaselineIdentity(left, right domain.Assessment) bool {
	return left.ID == right.ID && left.AccountID == right.AccountID && left.CatalogVersion == right.CatalogVersion && left.ScopePolicyVersion == right.ScopePolicyVersion && left.CreatedBy == right.CreatedBy && left.CreatedAt.Equal(right.CreatedAt)
}

func sameNewBaselineAssessment(left, right domain.Assessment) bool {
	return sameBaselineIdentity(left, right) && left.State == right.State && left.Version == right.Version &&
		left.UpdatedAt.Equal(right.UpdatedAt) && len(left.Answers) == 0 && len(right.Answers) == 0 &&
		len(left.Requirements) == 0 && len(right.Requirements) == 0 && left.Plan == nil && right.Plan == nil &&
		left.SupersededBy == "" && right.SupersededBy == ""
}

func responsibilityColumns(value domain.Responsibility) (any, any) {
	switch value.Kind {
	case domain.ResponsibilityUser:
		return value.ID, nil
	case domain.ResponsibilityPersona:
		return nil, value.ID
	default:
		return nil, nil
	}
}

func evidenceDecisionExists(values []domain.EvidenceDecision, evidenceID ids.KnowledgeEvidenceID) bool {
	for _, value := range values {
		if value.EvidenceID == evidenceID {
			return true
		}
	}
	return false
}

func requirementByID(values []domain.Requirement, requirementID ids.BaselineRequirementID) *domain.Requirement {
	for index := range values {
		if values[index].ID == requirementID {
			return &values[index]
		}
	}
	return nil
}

func classifyBaseline(err error) error {
	if err == nil || errors.Is(err, baselineapp.ErrInvalid) || errors.Is(err, baselineapp.ErrNotFound) || errors.Is(err, baselineapp.ErrConflict) || errors.Is(err, baselineapp.ErrConstraint) || errors.Is(err, baselineapp.ErrRepository) {
		return err
	}
	if errors.Is(err, domain.ErrInvalid) {
		return baselineapp.ErrRepository
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23503", "23514", "23502":
			return baselineapp.ErrConstraint
		case "23505", "40001", "40P01":
			return baselineapp.ErrConflict
		}
	}
	return fmt.Errorf("%w: %v", baselineapp.ErrRepository, err)
}

var _ baselineapp.Repository = (*BaselineRepository)(nil)
