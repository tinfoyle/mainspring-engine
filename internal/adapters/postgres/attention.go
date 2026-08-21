package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AttentionRepository struct {
	cell *database.CellPool
	ids  ids.Generator
}

func NewAttentionRepository(cell *database.CellPool, generator ids.Generator) (*AttentionRepository, error) {
	if cell == nil || generator == nil {
		return nil, errors.New("attention repository dependencies are required")
	}
	return &AttentionRepository{cell: cell, ids: generator}, nil
}

const informationSelect = `SELECT account_id,id,parent_work_item_id,fact_key,scope_kind,scope_work_item_id,scope_conversation_id,
	question,requested_by_kind,requested_by_id,state,fact_id,fact_version,answered_by_kind,answered_by_id,answered_at,
	canceled_by_kind,canceled_by_id,reason,version,created_at,updated_at FROM spyglass.attention_information_requests`

func (r *AttentionRepository) CreateInformation(ctx context.Context, item domain.InformationRequest, mutation attentionapp.Mutation) (domain.InformationRequest, error) {
	item, err := domain.RestoreInformationRequest(item)
	if err != nil || item.State != domain.InformationRequestOpen || item.Version != 1 || !mutation.Valid() || !item.CreatedAt.Equal(mutation.At) || !item.UpdatedAt.Equal(mutation.At) {
		return domain.InformationRequest{}, attentionapp.ErrCorrupt
	}
	var created domain.InformationRequest
	err = r.cell.WithAccountTx(ctx, item.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		existing, found, err := loadInformation(ctx, tx, item.AccountID, item.ID)
		if err != nil {
			return err
		}
		if found {
			if sameInformation(existing, item) {
				created = existing
				return nil
			}
			return attentionapp.ErrConflict
		}
		scopeWork, scopeConversation := informationScopeColumns(item.Requirement)
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.attention_information_requests
			(account_id,id,parent_work_item_id,fact_key,scope_kind,scope_work_item_id,scope_conversation_id,question,
			 requested_by_kind,requested_by_id,state,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, item.AccountID, item.ID, item.ParentWorkItemID,
			item.Requirement.Key, item.Requirement.Scope, scopeWork, scopeConversation, item.Question, item.RequestedBy.Kind, item.RequestedBy.ID,
			item.State, item.Version, item.CreatedAt, item.UpdatedAt)
		if err != nil {
			return err
		}
		if err := r.insertEvent(ctx, tx, "information_request", string(item.ID), "information_requested", 0, item.Version, mutation, map[string]any{"state": item.State, "scope_kind": item.Requirement.Scope}); err != nil {
			return err
		}
		created = item
		return nil
	})
	if err != nil {
		return domain.InformationRequest{}, classifyAttentionError(err)
	}
	return created, nil
}

func (r *AttentionRepository) GetInformation(ctx context.Context, accountID ids.AccountID, id ids.InformationRequestID) (domain.InformationRequest, error) {
	var item domain.InformationRequest
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		loaded, found, err := loadInformation(ctx, tx, accountID, id)
		if err != nil {
			return err
		}
		if !found {
			return attentionapp.ErrNotFound
		}
		item = loaded
		return nil
	})
	if err != nil {
		return domain.InformationRequest{}, classifyAttentionError(err)
	}
	return item, nil
}

func (r *AttentionRepository) UpdateInformation(ctx context.Context, item domain.InformationRequest, expected uint64, mutation attentionapp.Mutation) (domain.InformationRequest, error) {
	item, err := domain.RestoreInformationRequest(item)
	if err != nil || item.Version != expected+1 || !mutation.Valid() || !item.UpdatedAt.Equal(mutation.At) {
		return domain.InformationRequest{}, attentionapp.ErrCorrupt
	}
	var factID any
	var factVersion any
	var answeredKind, answeredID, answeredAt any
	if item.AnswerRecord != nil {
		factID, factVersion = item.AnswerRecord.Fact.ID, item.AnswerRecord.Fact.Version
		answeredKind, answeredID, answeredAt = item.AnswerRecord.AnsweredBy.Kind, item.AnswerRecord.AnsweredBy.ID, item.AnswerRecord.AnsweredAt
	}
	var canceledKind, canceledID any
	if item.CanceledBy != nil {
		canceledKind, canceledID = item.CanceledBy.Kind, item.CanceledBy.ID
	}
	err = r.cell.WithAccountTx(ctx, item.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `UPDATE spyglass.attention_information_requests SET state=$3,fact_id=$4,fact_version=$5,
			answered_by_kind=$6,answered_by_id=$7,answered_at=$8,canceled_by_kind=$9,canceled_by_id=$10,reason=$11,version=$12,updated_at=$13
			WHERE account_id=$1 AND id=$2 AND version=$14`, item.AccountID, item.ID, item.State, factID, factVersion, answeredKind, answeredID, answeredAt,
			canceledKind, canceledID, item.Reason, item.Version, item.UpdatedAt, expected)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return attentionapp.ErrConflict
		}
		eventType := "information_answered"
		if item.State == domain.InformationRequestCanceled {
			eventType = "information_canceled"
		}
		return r.insertEvent(ctx, tx, "information_request", string(item.ID), eventType, expected, item.Version, mutation, map[string]any{"state": item.State})
	})
	if err != nil {
		return domain.InformationRequest{}, classifyAttentionError(err)
	}
	return item, nil
}

const reviewSelect = `SELECT account_id,id,work_item_id,work_version,proposal_sha256,question,requested_by_kind,requested_by_id,
	reviewer_user_id,state,decision,decision_reason,decided_by_user_id,decided_at,canceled_by_kind,canceled_by_id,cancel_reason,
	invalidated_at,version,created_at,updated_at FROM spyglass.attention_work_reviews`

func (r *AttentionRepository) CreateWorkReview(ctx context.Context, item domain.WorkReview, mutation attentionapp.Mutation) (domain.WorkReview, error) {
	item, err := domain.RestoreWorkReview(item)
	if err != nil || item.State != domain.WorkReviewOpen || item.Version != 1 || !mutation.Valid() || !item.CreatedAt.Equal(mutation.At) || !item.UpdatedAt.Equal(mutation.At) {
		return domain.WorkReview{}, attentionapp.ErrCorrupt
	}
	var created domain.WorkReview
	err = r.cell.WithAccountTx(ctx, item.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		existing, found, err := loadWorkReview(ctx, tx, item.AccountID, item.ID)
		if err != nil {
			return err
		}
		if found {
			if sameWorkReview(existing, item) {
				created = existing
				return nil
			}
			return attentionapp.ErrConflict
		}
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.attention_work_reviews
			(account_id,id,work_item_id,work_version,proposal_sha256,question,requested_by_kind,requested_by_id,reviewer_user_id,state,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, item.AccountID, item.ID, item.WorkItemID, item.WorkVersion,
			item.ProposalSHA256[:], item.Question, item.RequestedBy.Kind, item.RequestedBy.ID, item.ReviewerID, item.State, item.Version, item.CreatedAt, item.UpdatedAt)
		if err != nil {
			return err
		}
		if err := r.insertEvent(ctx, tx, "work_review", string(item.ID), "review_requested", 0, item.Version, mutation, map[string]any{"state": item.State, "work_version": item.WorkVersion}); err != nil {
			return err
		}
		created = item
		return nil
	})
	if err != nil {
		return domain.WorkReview{}, classifyAttentionError(err)
	}
	return created, nil
}

func (r *AttentionRepository) GetWorkReview(ctx context.Context, accountID ids.AccountID, id ids.WorkReviewID) (domain.WorkReview, error) {
	var item domain.WorkReview
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		loaded, found, err := loadWorkReview(ctx, tx, accountID, id)
		if err != nil {
			return err
		}
		if !found {
			return attentionapp.ErrNotFound
		}
		item = loaded
		return nil
	})
	if err != nil {
		return domain.WorkReview{}, classifyAttentionError(err)
	}
	return item, nil
}

func (r *AttentionRepository) UpdateWorkReview(ctx context.Context, item domain.WorkReview, expected uint64, mutation attentionapp.Mutation) (domain.WorkReview, error) {
	item, err := domain.RestoreWorkReview(item)
	if err != nil || item.Version != expected+1 || !mutation.Valid() || !item.UpdatedAt.Equal(mutation.At) {
		return domain.WorkReview{}, attentionapp.ErrCorrupt
	}
	var decision, decisionReason, decidedBy, decidedAt any
	if item.Decision != nil {
		decision, decisionReason, decidedBy, decidedAt = item.Decision.Decision, item.Decision.Reason, item.Decision.DecidedBy.ID, item.Decision.DecidedAt
	}
	var canceledKind, canceledID any
	if item.CanceledBy != nil {
		canceledKind, canceledID = item.CanceledBy.Kind, item.CanceledBy.ID
	}
	err = r.cell.WithAccountTx(ctx, item.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `UPDATE spyglass.attention_work_reviews SET state=$3,decision=$4,decision_reason=$5,decided_by_user_id=$6,
			decided_at=$7,canceled_by_kind=$8,canceled_by_id=$9,cancel_reason=$10,invalidated_at=$11,version=$12,updated_at=$13
			WHERE account_id=$1 AND id=$2 AND version=$14`, item.AccountID, item.ID, item.State, decision, coalesceString(decisionReason), decidedBy, decidedAt,
			canceledKind, canceledID, item.CancelReason, item.InvalidatedAt, item.Version, item.UpdatedAt, expected)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return attentionapp.ErrConflict
		}
		eventType := map[domain.WorkReviewState]string{domain.WorkReviewApproved: "review_decided", domain.WorkReviewChangesRequested: "review_decided", domain.WorkReviewCanceled: "review_canceled", domain.WorkReviewInvalidated: "review_invalidated"}[item.State]
		if eventType == "" {
			return attentionapp.ErrCorrupt
		}
		return r.insertEvent(ctx, tx, "work_review", string(item.ID), eventType, expected, item.Version, mutation, map[string]any{"state": item.State, "work_version": item.WorkVersion})
	})
	if err != nil {
		return domain.WorkReview{}, classifyAttentionError(err)
	}
	return item, nil
}

const approvalSelect = `SELECT account_id,id,operation_id,invocation_id,work_item_id,capability,canonical_payload,input_sha256,hash_version,
	evidence_sha256,proposer_kind,proposer_id,policy_version,require_independent_review,expires_at,state,decision,decision_reason,
	decided_by_user_id,decided_at,canceled_by_kind,canceled_by_id,cancel_reason,canceled_at,invalidated_at,expired_at,version,created_at,updated_at
	FROM spyglass.attention_consequential_approvals`

func (r *AttentionRepository) CreateApproval(ctx context.Context, item domain.ConsequentialApproval, mutation attentionapp.Mutation) (domain.ConsequentialApproval, error) {
	item, err := domain.RestoreConsequentialApproval(item)
	if err != nil || item.State != domain.ConsequentialApprovalOpen || item.Version != 1 || !mutation.Valid() || !item.CreatedAt.Equal(mutation.At) || !item.UpdatedAt.Equal(mutation.At) {
		return domain.ConsequentialApproval{}, attentionapp.ErrCorrupt
	}
	var created domain.ConsequentialApproval
	err = r.cell.WithAccountTx(ctx, item.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		existing, found, err := loadApproval(ctx, tx, item.AccountID, item.ID)
		if err != nil {
			return err
		}
		if found {
			if sameApproval(existing, item) {
				created = existing
				return nil
			}
			return attentionapp.ErrConflict
		}
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.attention_consequential_approvals
			(account_id,id,operation_id,invocation_id,work_item_id,capability,canonical_payload,input_sha256,hash_version,evidence_sha256,
			 proposer_kind,proposer_id,policy_version,require_independent_review,expires_at,state,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, item.AccountID, item.ID, item.OperationID,
			item.InvocationID, nullableID(item.WorkItemID), item.Capability, []byte(item.CanonicalPayload), item.InputSHA256[:], item.HashVersion,
			item.EvidenceSHA256[:], item.Proposer.Kind, item.Proposer.ID, item.PolicyVersion, item.RequireIndependentReview, item.ExpiresAt,
			item.State, item.Version, item.CreatedAt, item.UpdatedAt)
		if err != nil {
			return err
		}
		if err := r.insertEvent(ctx, tx, "consequential_approval", string(item.ID), "approval_requested", 0, item.Version, mutation, map[string]any{"state": item.State, "capability": item.Capability, "hash_version": item.HashVersion, "policy_version": item.PolicyVersion}); err != nil {
			return err
		}
		created = item
		return nil
	})
	if err != nil {
		return domain.ConsequentialApproval{}, classifyAttentionError(err)
	}
	return created, nil
}

func (r *AttentionRepository) GetApproval(ctx context.Context, accountID ids.AccountID, id ids.ConsequentialApprovalID) (domain.ConsequentialApproval, error) {
	var item domain.ConsequentialApproval
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		loaded, found, err := loadApproval(ctx, tx, accountID, id)
		if err != nil {
			return err
		}
		if !found {
			return attentionapp.ErrNotFound
		}
		item = loaded
		return nil
	})
	if err != nil {
		return domain.ConsequentialApproval{}, classifyAttentionError(err)
	}
	return item, nil
}

func (r *AttentionRepository) UpdateApproval(ctx context.Context, item domain.ConsequentialApproval, expected uint64, mutation attentionapp.Mutation) (domain.ConsequentialApproval, error) {
	item, err := domain.RestoreConsequentialApproval(item)
	if err != nil || item.Version != expected+1 || !mutation.Valid() || !item.UpdatedAt.Equal(mutation.At) {
		return domain.ConsequentialApproval{}, attentionapp.ErrCorrupt
	}
	var decision, decisionReason, decidedBy, decidedAt any
	if item.Decision != nil {
		decision, decisionReason, decidedBy, decidedAt = item.Decision.Decision, item.Decision.Reason, item.Decision.DecidedBy, item.Decision.DecidedAt
	}
	var canceledKind, canceledID any
	if item.CanceledBy != nil {
		canceledKind, canceledID = item.CanceledBy.Kind, item.CanceledBy.ID
	}
	err = r.cell.WithAccountTx(ctx, item.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `UPDATE spyglass.attention_consequential_approvals SET state=$3,decision=$4,decision_reason=$5,
			decided_by_user_id=$6,decided_at=$7,canceled_by_kind=$8,canceled_by_id=$9,cancel_reason=$10,canceled_at=$11,
			invalidated_at=$12,expired_at=$13,version=$14,updated_at=$15 WHERE account_id=$1 AND id=$2 AND version=$16`, item.AccountID, item.ID,
			item.State, decision, coalesceString(decisionReason), decidedBy, decidedAt, canceledKind, canceledID, item.Reason, item.CanceledAt,
			item.InvalidatedAt, item.ExpiredAt, item.Version, item.UpdatedAt, expected)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return attentionapp.ErrConflict
		}
		eventType := map[domain.ConsequentialApprovalState]string{domain.ConsequentialApprovalApproved: "approval_decided", domain.ConsequentialApprovalRejected: "approval_decided", domain.ConsequentialApprovalCanceled: "approval_canceled", domain.ConsequentialApprovalInvalidated: "approval_invalidated", domain.ConsequentialApprovalExpired: "approval_expired"}[item.State]
		if eventType == "" {
			return attentionapp.ErrCorrupt
		}
		return r.insertEvent(ctx, tx, "consequential_approval", string(item.ID), eventType, expected, item.Version, mutation, map[string]any{"state": item.State, "capability": item.Capability, "hash_version": item.HashVersion, "policy_version": item.PolicyVersion})
	})
	if err != nil {
		return domain.ConsequentialApproval{}, classifyAttentionError(err)
	}
	return item, nil
}

func (r *AttentionRepository) insertEvent(ctx context.Context, tx pgx.Tx, aggregateKind, aggregateID, eventType string, fromVersion, toVersion uint64, mutation attentionapp.Mutation, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return attentionapp.ErrCorrupt
	}
	var informationID, reviewID, approvalID any
	switch aggregateKind {
	case "information_request":
		informationID = aggregateID
	case "work_review":
		reviewID = aggregateID
	case "consequential_approval":
		approvalID = aggregateID
	default:
		return attentionapp.ErrCorrupt
	}
	_, err = tx.Exec(ctx, `INSERT INTO spyglass.attention_events
		(account_id,id,aggregate_kind,information_request_id,work_review_id,consequential_approval_id,event_type,from_version,to_version,
		 actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
		VALUES (nullif(current_setting('app.account_id',true),'')::uuid,$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		r.ids.New(), aggregateKind, informationID, reviewID, approvalID, eventType, fromVersion, toVersion, mutation.Actor.Kind, mutation.Actor.ID,
		mutation.ReasonCode, mutation.CorrelationID, raw, mutation.At.UTC())
	return err
}

func informationScopeColumns(requirement domain.FactRequirement) (any, any) {
	if requirement.Scope == domain.InformationScopeWorkItem {
		return requirement.ScopeID, nil
	}
	if requirement.Scope == domain.InformationScopeConversation {
		return nil, requirement.ScopeID
	}
	return nil, nil
}

func loadInformation(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, id ids.InformationRequestID) (domain.InformationRequest, bool, error) {
	item, err := scanInformation(tx.QueryRow(ctx, informationSelect+` WHERE account_id=$1 AND id=$2`, accountID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InformationRequest{}, false, nil
	}
	return item, err == nil, err
}

func scanInformation(row interface{ Scan(...any) error }) (domain.InformationRequest, error) {
	var item domain.InformationRequest
	var scopeWork, scopeConversation, factID, answeredKind, answeredID, canceledKind, canceledID *string
	var factVersion *uint64
	var answeredAt *time.Time
	if err := row.Scan(&item.AccountID, &item.ID, &item.ParentWorkItemID, &item.Requirement.Key, &item.Requirement.Scope, &scopeWork, &scopeConversation,
		&item.Question, &item.RequestedBy.Kind, &item.RequestedBy.ID, &item.State, &factID, &factVersion, &answeredKind, &answeredID, &answeredAt,
		&canceledKind, &canceledID, &item.Reason, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return domain.InformationRequest{}, err
	}
	if scopeWork != nil {
		item.Requirement.ScopeID = *scopeWork
	} else if scopeConversation != nil {
		item.Requirement.ScopeID = *scopeConversation
	}
	if factID != nil && factVersion != nil && answeredKind != nil && answeredID != nil && answeredAt != nil {
		item.AnswerRecord = &domain.InformationAnswer{Fact: domain.FactReference{ID: *factID, Version: *factVersion, Requirement: item.Requirement}, AnsweredBy: domain.Actor{Kind: domain.ActorKind(*answeredKind), ID: *answeredID}, AnsweredAt: *answeredAt}
	}
	if canceledKind != nil && canceledID != nil {
		item.CanceledBy = &domain.Actor{Kind: domain.ActorKind(*canceledKind), ID: *canceledID}
	}
	item, err := domain.RestoreInformationRequest(item)
	if err != nil {
		return domain.InformationRequest{}, attentionapp.ErrCorrupt
	}
	return item, nil
}

func loadWorkReview(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, id ids.WorkReviewID) (domain.WorkReview, bool, error) {
	item, err := scanWorkReview(tx.QueryRow(ctx, reviewSelect+` WHERE account_id=$1 AND id=$2`, accountID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WorkReview{}, false, nil
	}
	return item, err == nil, err
}

func scanWorkReview(row interface{ Scan(...any) error }) (domain.WorkReview, error) {
	var item domain.WorkReview
	var digest []byte
	var decision, decisionReason, decidedBy, canceledKind, canceledID *string
	var decidedAt *time.Time
	if err := row.Scan(&item.AccountID, &item.ID, &item.WorkItemID, &item.WorkVersion, &digest, &item.Question, &item.RequestedBy.Kind, &item.RequestedBy.ID,
		&item.ReviewerID, &item.State, &decision, &decisionReason, &decidedBy, &decidedAt, &canceledKind, &canceledID, &item.CancelReason,
		&item.InvalidatedAt, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return domain.WorkReview{}, err
	}
	if len(digest) != len(item.ProposalSHA256) {
		return domain.WorkReview{}, attentionapp.ErrCorrupt
	}
	copy(item.ProposalSHA256[:], digest)
	if decision != nil && decisionReason != nil && decidedBy != nil && decidedAt != nil {
		item.Decision = &domain.ReviewDecisionRecord{Decision: domain.WorkReviewDecision(*decision), Reason: *decisionReason, DecidedBy: domain.Actor{Kind: domain.ActorUser, ID: *decidedBy}, DecidedAt: *decidedAt}
	}
	if canceledKind != nil && canceledID != nil {
		item.CanceledBy = &domain.Actor{Kind: domain.ActorKind(*canceledKind), ID: *canceledID}
	}
	item, err := domain.RestoreWorkReview(item)
	if err != nil {
		return domain.WorkReview{}, attentionapp.ErrCorrupt
	}
	return item, nil
}

func loadApproval(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, id ids.ConsequentialApprovalID) (domain.ConsequentialApproval, bool, error) {
	item, err := scanApproval(tx.QueryRow(ctx, approvalSelect+` WHERE account_id=$1 AND id=$2`, accountID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ConsequentialApproval{}, false, nil
	}
	return item, err == nil, err
}

func scanApproval(row interface{ Scan(...any) error }) (domain.ConsequentialApproval, error) {
	var item domain.ConsequentialApproval
	var workID, decision, decisionReason, decidedBy, canceledKind, canceledID *string
	var payload, inputDigest, evidenceDigest []byte
	var decidedAt *time.Time
	if err := row.Scan(&item.AccountID, &item.ID, &item.OperationID, &item.InvocationID, &workID, &item.Capability, &payload, &inputDigest, &item.HashVersion,
		&evidenceDigest, &item.Proposer.Kind, &item.Proposer.ID, &item.PolicyVersion, &item.RequireIndependentReview, &item.ExpiresAt, &item.State,
		&decision, &decisionReason, &decidedBy, &decidedAt, &canceledKind, &canceledID, &item.Reason, &item.CanceledAt, &item.InvalidatedAt,
		&item.ExpiredAt, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return domain.ConsequentialApproval{}, err
	}
	if workID != nil {
		item.WorkItemID = ids.WorkItemID(*workID)
	}
	item.CanonicalPayload = append(json.RawMessage(nil), payload...)
	if len(inputDigest) != len(item.InputSHA256) || len(evidenceDigest) != len(item.EvidenceSHA256) {
		return domain.ConsequentialApproval{}, attentionapp.ErrCorrupt
	}
	copy(item.InputSHA256[:], inputDigest)
	copy(item.EvidenceSHA256[:], evidenceDigest)
	if decision != nil && decisionReason != nil && decidedBy != nil && decidedAt != nil {
		item.Decision = &domain.ApprovalDecisionRecord{Decision: domain.ApprovalDecision(*decision), Reason: *decisionReason, DecidedBy: ids.UserID(*decidedBy), DecidedAt: *decidedAt}
	}
	if canceledKind != nil && canceledID != nil {
		item.CanceledBy = &domain.Actor{Kind: domain.ActorKind(*canceledKind), ID: *canceledID}
	}
	item, err := domain.RestoreConsequentialApproval(item)
	if err != nil {
		return domain.ConsequentialApproval{}, attentionapp.ErrCorrupt
	}
	return item, nil
}

func sameInformation(left, right domain.InformationRequest) bool {
	return left.InformationRequestDraft == right.InformationRequestDraft && left.State == right.State && left.Version == right.Version && left.CreatedAt.Equal(right.CreatedAt)
}

func sameWorkReview(left, right domain.WorkReview) bool {
	return left.WorkReviewDraft == right.WorkReviewDraft && left.State == right.State && left.Version == right.Version && left.CreatedAt.Equal(right.CreatedAt)
}

func sameApproval(left, right domain.ConsequentialApproval) bool {
	return left.ID == right.ID && left.AccountID == right.AccountID && left.OperationID == right.OperationID && left.InvocationID == right.InvocationID && left.WorkItemID == right.WorkItemID &&
		left.Capability == right.Capability && bytes.Equal(left.CanonicalPayload, right.CanonicalPayload) && left.InputSHA256 == right.InputSHA256 && left.HashVersion == right.HashVersion &&
		left.EvidenceSHA256 == right.EvidenceSHA256 && left.Proposer == right.Proposer && left.PolicyVersion == right.PolicyVersion && left.RequireIndependentReview == right.RequireIndependentReview &&
		left.ExpiresAt.Equal(right.ExpiresAt) && left.State == right.State && left.Version == right.Version && left.CreatedAt.Equal(right.CreatedAt)
}

func coalesceString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func classifyAttentionError(err error) error {
	if err == nil || errors.Is(err, attentionapp.ErrNotFound) || errors.Is(err, attentionapp.ErrConflict) || errors.Is(err, attentionapp.ErrConstraint) || errors.Is(err, attentionapp.ErrCorrupt) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return attentionapp.ErrConflict
		case "23503", "23514", "22023", "42501":
			return fmt.Errorf("%w: %s", attentionapp.ErrConstraint, pgErr.ConstraintName)
		case "40001", "40P01":
			return attentionapp.ErrConflict
		}
	}
	return fmt.Errorf("attention repository unavailable: %w", err)
}
