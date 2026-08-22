package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type KnowledgeRepository struct{ cell *database.CellPool }

func NewKnowledgeRepository(cell *database.CellPool) (*KnowledgeRepository, error) {
	if cell == nil {
		return nil, errors.New("Knowledge cell pool is required")
	}
	return &KnowledgeRepository{cell: cell}, nil
}

func (r *KnowledgeRepository) RegisterEvidence(ctx context.Context, value knowledgedomain.Evidence, mutation knowledgeapp.Mutation) (knowledgedomain.Evidence, error) {
	var result knowledgedomain.Evidence
	err := r.cell.WithAccountTx(ctx, value.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		existing, err := getKnowledgeEvidence(ctx, tx, value.AccountID, value.ID)
		if err == nil {
			if !sameKnowledgeEvidence(existing, value) {
				return knowledgeapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(err, knowledgeapp.ErrNotFound) {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.knowledge_evidence(account_id,id,source_kind,source_reference,source_revision,content_sha256,captured_at,created_by_kind,created_by_id,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, value.AccountID, value.ID, value.Kind, value.SourceReference, value.SourceRevision, value.ContentSHA256[:], value.CapturedAt, value.CreatedBy.Kind, value.CreatedBy.ID, value.CreatedAt)
		if err != nil {
			return err
		}
		if err := insertKnowledgeEvent(ctx, tx, value.AccountID, "evidence", string(value.ID), "evidence_registered", 0, 1, mutation, map[string]any{"source_kind": value.Kind}); err != nil {
			return err
		}
		result = value
		return nil
	})
	return result, classifyKnowledge(err)
}

func (r *KnowledgeRepository) ProposeClaim(ctx context.Context, value knowledgedomain.Claim, mutation knowledgeapp.Mutation) (knowledgedomain.Claim, error) {
	var result knowledgedomain.Claim
	err := r.cell.WithAccountTx(ctx, value.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		existing, err := getKnowledgeClaim(ctx, tx, value.AccountID, value.ID, false)
		if err == nil {
			if !sameKnowledgeClaim(existing, value) {
				return knowledgeapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(err, knowledgeapp.ErrNotFound) {
			return err
		}
		workID, conversationID := scopeColumns(value.Scope)
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.knowledge_claims(account_id,id,scope_kind,scope_work_item_id,scope_conversation_id,fact_key,canonical_value,value_sha256,hash_version,confidence,sensitivity,proposed_by_kind,proposed_by_id,state,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, value.AccountID, value.ID, value.Scope.Kind, workID, conversationID, value.Key, []byte(value.CanonicalValue), value.ValueSHA256[:], value.HashVersion, value.Confidence, value.Sensitivity, value.ProposedBy.Kind, value.ProposedBy.ID, value.State, value.Version, value.CreatedAt, value.UpdatedAt)
		if err != nil {
			return err
		}
		for _, citation := range value.Citations {
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.knowledge_claim_citations(account_id,claim_id,evidence_id,evidence_kind,relation,locator,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, value.AccountID, value.ID, citation.EvidenceID, citation.EvidenceKind, citation.Relation, citation.Locator, value.CreatedAt); err != nil {
				return err
			}
		}
		if err := insertKnowledgeEvent(ctx, tx, value.AccountID, "claim", string(value.ID), "claim_proposed", 0, 1, mutation, map[string]any{"scope_kind": value.Scope.Kind, "fact_key": value.Key, "sensitivity": value.Sensitivity, "confidence": value.Confidence, "citation_count": len(value.Citations)}); err != nil {
			return err
		}
		result = value
		return nil
	})
	return result, classifyKnowledge(err)
}

func (r *KnowledgeRepository) GetClaim(ctx context.Context, accountID ids.AccountID, claimID ids.KnowledgeClaimID) (knowledgedomain.Claim, error) {
	var result knowledgedomain.Claim
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := getKnowledgeClaim(ctx, tx, accountID, claimID, false)
		result = value
		return err
	})
	return result, classifyKnowledge(err)
}

func (r *KnowledgeRepository) ListClaims(ctx context.Context, accountID ids.AccountID, query knowledgeapp.ClaimListQuery) (knowledgeapp.ClaimPage, error) {
	page := knowledgeapp.ClaimPage{Items: make([]knowledgeapp.ClaimSummary, 0, query.Limit)}
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var scopeKind any
		var workID, conversationID any
		if query.Scope != nil {
			scopeKind = query.Scope.Kind
			workID, conversationID = scopeColumns(*query.Scope)
		}
		rows, err := tx.Query(ctx, `SELECT id,scope_kind,scope_work_item_id,scope_conversation_id,fact_key,confidence,sensitivity,state,proposed_by_kind,proposed_by_id,version,created_at,updated_at
			FROM spyglass.knowledge_claims WHERE account_id=$1 AND ($2='' OR state=$2)
			AND ($3::text IS NULL OR (scope_kind=$3 AND scope_work_item_id IS NOT DISTINCT FROM $4::uuid AND scope_conversation_id IS NOT DISTINCT FROM $5::uuid))
			AND ($6='' OR fact_key LIKE $6||'%') AND ($7::timestamptz IS NULL OR (updated_at,id)<($7,$8::uuid))
			ORDER BY updated_at DESC,id DESC LIMIT $9`, accountID, query.State, scopeKind, workID, conversationID, query.KeyPrefix, query.AfterUpdatedAt, nullableKnowledgeID(string(query.AfterID)), query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item knowledgeapp.ClaimSummary
			var scopeKind knowledgedomain.ScopeKind
			var work, conversation *string
			if err := rows.Scan(&item.ID, &scopeKind, &work, &conversation, &item.Key, &item.Confidence, &item.Sensitivity, &item.State, &item.ProposedBy.Kind, &item.ProposedBy.ID, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			item.Scope = restoreScope(scopeKind, work, conversation)
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return knowledgeapp.ClaimPage{}, classifyKnowledge(err)
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &knowledgeapp.ClaimCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	return page, nil
}

func (r *KnowledgeRepository) DecideClaim(ctx context.Context, accountID ids.AccountID, claimID ids.KnowledgeClaimID, factID ids.KnowledgeFactID, command knowledgedomain.DecideClaimCommand, mutation knowledgeapp.Mutation) (knowledgedomain.Claim, *knowledgedomain.Fact, error) {
	var decided knowledgedomain.Claim
	var fact *knowledgedomain.Fact
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := getKnowledgeClaim(ctx, tx, accountID, claimID, true)
		if err != nil {
			return err
		}
		if current.State == knowledgedomain.ClaimAccepted || current.State == knowledgedomain.ClaimRejected {
			desired := knowledgedomain.ClaimRejected
			if command.Accept {
				desired = knowledgedomain.ClaimAccepted
			}
			if current.State != desired || current.Decision == nil || current.Decision.Reason != strings.TrimSpace(command.Reason) || current.Decision.DecidedBy != ids.UserID(command.Actor.ID) {
				return knowledgeapp.ErrConflict
			}
			decided = current
			if current.State == knowledgedomain.ClaimAccepted {
				replayed, err := getKnowledgeFactByKey(ctx, tx, current)
				if err != nil || replayed.CurrentClaimID != current.ID {
					if err == nil {
						err = knowledgeapp.ErrConflict
					}
					return err
				}
				fact = &replayed
			}
			return nil
		}
		decided, err = current.Decide(command)
		if err != nil {
			return err
		}
		if err := updateKnowledgeClaim(ctx, tx, decided, current.Version); err != nil {
			return err
		}
		eventType := "claim_rejected"
		if decided.State == knowledgedomain.ClaimAccepted {
			eventType = "claim_accepted"
		}
		if err := insertKnowledgeEvent(ctx, tx, accountID, "claim", string(claimID), eventType, current.Version, decided.Version, mutation, map[string]any{"scope_kind": decided.Scope.Kind, "fact_key": decided.Key, "sensitivity": decided.Sensitivity}); err != nil {
			return err
		}
		if decided.State != knowledgedomain.ClaimAccepted {
			return nil
		}
		projected, previousClaimID, created, err := projectKnowledgeFact(ctx, tx, factID, decided)
		if err != nil {
			return err
		}
		fact = &projected
		factEvent := "fact_revised"
		fromVersion := projected.Revision - 1
		if created {
			factEvent, fromVersion = "fact_created", 0
		}
		if err := insertKnowledgeEvent(ctx, tx, accountID, "fact", string(projected.ID), factEvent, fromVersion, projected.Revision, mutation, map[string]any{"scope_kind": projected.Scope.Kind, "fact_key": projected.Key, "revision": projected.Revision}); err != nil {
			return err
		}
		if previousClaimID != "" && previousClaimID != decided.ID {
			previous, err := getKnowledgeClaim(ctx, tx, accountID, previousClaimID, true)
			if err != nil {
				return err
			}
			superseded, err := previous.Supersede(mutation.At)
			if err != nil {
				return err
			}
			if err := updateKnowledgeClaim(ctx, tx, superseded, previous.Version); err != nil {
				return err
			}
			if err := insertKnowledgeEvent(ctx, tx, accountID, "claim", string(previous.ID), "claim_superseded", previous.Version, superseded.Version, mutation, map[string]any{"scope_kind": previous.Scope.Kind, "fact_key": previous.Key}); err != nil {
				return err
			}
		}
		return nil
	})
	return decided, fact, classifyKnowledge(err)
}

func (r *KnowledgeRepository) ListFacts(ctx context.Context, accountID ids.AccountID, query knowledgeapp.FactListQuery) (knowledgeapp.FactPage, error) {
	page := knowledgeapp.FactPage{Items: make([]knowledgeapp.FactSummary, 0, query.Limit)}
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var scopeKind any
		var workID, conversationID any
		if query.Scope != nil {
			scopeKind = query.Scope.Kind
			workID, conversationID = scopeColumns(*query.Scope)
		}
		rows, err := tx.Query(ctx, `SELECT fact.id,fact.current_claim_id,fact.scope_kind,fact.scope_work_item_id,fact.scope_conversation_id,fact.fact_key,claim.sensitivity,fact.state,fact.revision,fact.accepted_at,fact.updated_at
			FROM spyglass.knowledge_facts fact JOIN spyglass.knowledge_claims claim ON claim.account_id=fact.account_id AND claim.id=fact.current_claim_id
			WHERE fact.account_id=$1 AND ($2::text IS NULL OR (fact.scope_kind=$2 AND fact.scope_work_item_id IS NOT DISTINCT FROM $3::uuid AND fact.scope_conversation_id IS NOT DISTINCT FROM $4::uuid))
			AND ($5='' OR fact.fact_key LIKE $5||'%') AND ($6::timestamptz IS NULL OR (fact.updated_at,fact.id)<($6,$7::uuid))
			ORDER BY fact.updated_at DESC,fact.id DESC LIMIT $8`, accountID, scopeKind, workID, conversationID, query.KeyPrefix, query.AfterUpdatedAt, nullableKnowledgeID(string(query.AfterID)), query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item knowledgeapp.FactSummary
			var scopeKind knowledgedomain.ScopeKind
			var work, conversation *string
			if err := rows.Scan(&item.ID, &item.CurrentClaimID, &scopeKind, &work, &conversation, &item.Key, &item.Sensitivity, &item.State, &item.Revision, &item.AcceptedAt, &item.UpdatedAt); err != nil {
				return err
			}
			item.Scope = restoreScope(scopeKind, work, conversation)
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return knowledgeapp.FactPage{}, classifyKnowledge(err)
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &knowledgeapp.FactCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	return page, nil
}

func getKnowledgeEvidence(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, evidenceID ids.KnowledgeEvidenceID) (knowledgedomain.Evidence, error) {
	var value knowledgedomain.Evidence
	var digest []byte
	err := tx.QueryRow(ctx, `SELECT id,account_id,source_kind,source_reference,source_revision,content_sha256,captured_at,created_by_kind,created_by_id,created_at FROM spyglass.knowledge_evidence WHERE account_id=$1 AND id=$2`, accountID, evidenceID).
		Scan(&value.ID, &value.AccountID, &value.Kind, &value.SourceReference, &value.SourceRevision, &digest, &value.CapturedAt, &value.CreatedBy.Kind, &value.CreatedBy.ID, &value.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, knowledgeapp.ErrNotFound
	}
	if err != nil || len(digest) != sha256.Size {
		return value, err
	}
	copy(value.ContentSHA256[:], digest)
	value, err = knowledgedomain.RestoreEvidence(value)
	if err != nil {
		return value, knowledgeapp.ErrRepository
	}
	return value, nil
}

func getKnowledgeClaim(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, claimID ids.KnowledgeClaimID, lock bool) (knowledgedomain.Claim, error) {
	var value knowledgedomain.Claim
	var workID, conversationID *string
	var raw, digest []byte
	var decisionReason string
	var decidedBy *ids.UserID
	var decidedAt *time.Time
	statement := `SELECT id,account_id,scope_kind,scope_work_item_id,scope_conversation_id,fact_key,canonical_value,value_sha256,hash_version,confidence,sensitivity,proposed_by_kind,proposed_by_id,state,decision_reason,decided_by_user_id,decided_at,version,created_at,updated_at FROM spyglass.knowledge_claims WHERE account_id=$1 AND id=$2`
	if lock {
		statement += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, statement, accountID, claimID).Scan(&value.ID, &value.AccountID, &value.Scope.Kind, &workID, &conversationID, &value.Key, &raw, &digest, &value.HashVersion, &value.Confidence, &value.Sensitivity, &value.ProposedBy.Kind, &value.ProposedBy.ID, &value.State, &decisionReason, &decidedBy, &decidedAt, &value.Version, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, knowledgeapp.ErrNotFound
	}
	if err != nil || len(digest) != sha256.Size {
		return value, err
	}
	value.Scope = restoreScope(value.Scope.Kind, workID, conversationID)
	value.CanonicalValue = append(value.CanonicalValue[:0], raw...)
	copy(value.ValueSHA256[:], digest)
	if decidedBy != nil && decidedAt != nil {
		value.Decision = &knowledgedomain.ClaimDecision{Reason: decisionReason, DecidedBy: *decidedBy, DecidedAt: *decidedAt}
	}
	rows, err := tx.Query(ctx, `SELECT evidence_id,evidence_kind,relation,locator FROM spyglass.knowledge_claim_citations WHERE account_id=$1 AND claim_id=$2 ORDER BY evidence_id`, accountID, claimID)
	if err != nil {
		return value, err
	}
	defer rows.Close()
	for rows.Next() {
		var citation knowledgedomain.Citation
		if err := rows.Scan(&citation.EvidenceID, &citation.EvidenceKind, &citation.Relation, &citation.Locator); err != nil {
			return value, err
		}
		value.Citations = append(value.Citations, citation)
	}
	if err := rows.Err(); err != nil {
		return value, err
	}
	value, err = knowledgedomain.RestoreClaim(value)
	if err != nil {
		return value, knowledgeapp.ErrRepository
	}
	return value, nil
}

func updateKnowledgeClaim(ctx context.Context, tx pgx.Tx, value knowledgedomain.Claim, expected uint64) error {
	var reason string
	var by any
	var at any
	if value.Decision != nil {
		reason, by, at = value.Decision.Reason, value.Decision.DecidedBy, value.Decision.DecidedAt
	}
	tag, err := tx.Exec(ctx, `UPDATE spyglass.knowledge_claims SET state=$4,decision_reason=$5,decided_by_user_id=$6,decided_at=$7,version=$8,updated_at=$9 WHERE account_id=$1 AND id=$2 AND version=$3`, value.AccountID, value.ID, expected, value.State, reason, by, at, value.Version, value.UpdatedAt)
	if err == nil && tag.RowsAffected() != 1 {
		return knowledgeapp.ErrConflict
	}
	return err
}

func projectKnowledgeFact(ctx context.Context, tx pgx.Tx, factID ids.KnowledgeFactID, claim knowledgedomain.Claim) (knowledgedomain.Fact, ids.KnowledgeClaimID, bool, error) {
	workID, conversationID := scopeColumns(claim.Scope)
	var current knowledgedomain.Fact
	var currentWork, currentConversation *string
	err := tx.QueryRow(ctx, `SELECT id,account_id,scope_kind,scope_work_item_id,scope_conversation_id,fact_key,current_claim_id,state,revision,accepted_by_user_id,accepted_at,created_at,updated_at
		FROM spyglass.knowledge_facts WHERE account_id=$1 AND scope_kind=$2 AND scope_work_item_id IS NOT DISTINCT FROM $3::uuid AND scope_conversation_id IS NOT DISTINCT FROM $4::uuid AND fact_key=$5 FOR UPDATE`, claim.AccountID, claim.Scope.Kind, workID, conversationID, claim.Key).
		Scan(&current.ID, &current.AccountID, &current.Scope.Kind, &currentWork, &currentConversation, &current.Key, &current.CurrentClaimID, &current.State, &current.Revision, &current.AcceptedBy, &current.AcceptedAt, &current.CreatedAt, &current.UpdatedAt)
	created := errors.Is(err, pgx.ErrNoRows)
	var previous ids.KnowledgeClaimID
	if created {
		current, err = knowledgedomain.NewFact(factID, claim)
	} else if err == nil {
		current.Scope = restoreScope(current.Scope.Kind, currentWork, currentConversation)
		current, err = knowledgedomain.RestoreFact(current)
		if err == nil {
			previous = current.CurrentClaimID
			current, err = current.Revise(claim)
		}
	}
	if err != nil {
		return current, previous, created, err
	}
	if created {
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.knowledge_facts(account_id,id,scope_kind,scope_work_item_id,scope_conversation_id,fact_key,current_claim_id,state,revision,accepted_by_user_id,accepted_at,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, current.AccountID, current.ID, current.Scope.Kind, workID, conversationID, current.Key, current.CurrentClaimID, current.State, current.Revision, current.AcceptedBy, current.AcceptedAt, current.CreatedAt, current.UpdatedAt)
	} else {
		_, err = tx.Exec(ctx, `UPDATE spyglass.knowledge_facts SET current_claim_id=$3,state=$4,revision=$5,accepted_by_user_id=$6,accepted_at=$7,updated_at=$8 WHERE account_id=$1 AND id=$2`, current.AccountID, current.ID, current.CurrentClaimID, current.State, current.Revision, current.AcceptedBy, current.AcceptedAt, current.UpdatedAt)
	}
	if err != nil {
		return current, previous, created, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO spyglass.knowledge_fact_revisions(account_id,fact_id,revision,claim_id,accepted_by_user_id,accepted_at) VALUES ($1,$2,$3,$4,$5,$6)`, current.AccountID, current.ID, current.Revision, current.CurrentClaimID, current.AcceptedBy, current.AcceptedAt)
	return current, previous, created, err
}

func getKnowledgeFactByKey(ctx context.Context, tx pgx.Tx, claim knowledgedomain.Claim) (knowledgedomain.Fact, error) {
	workID, conversationID := scopeColumns(claim.Scope)
	var value knowledgedomain.Fact
	var work, conversation *string
	err := tx.QueryRow(ctx, `SELECT id,account_id,scope_kind,scope_work_item_id,scope_conversation_id,fact_key,current_claim_id,state,revision,accepted_by_user_id,accepted_at,created_at,updated_at
		FROM spyglass.knowledge_facts WHERE account_id=$1 AND scope_kind=$2 AND scope_work_item_id IS NOT DISTINCT FROM $3::uuid AND scope_conversation_id IS NOT DISTINCT FROM $4::uuid AND fact_key=$5`, claim.AccountID, claim.Scope.Kind, workID, conversationID, claim.Key).
		Scan(&value.ID, &value.AccountID, &value.Scope.Kind, &work, &conversation, &value.Key, &value.CurrentClaimID, &value.State, &value.Revision, &value.AcceptedBy, &value.AcceptedAt, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, knowledgeapp.ErrNotFound
	}
	if err != nil {
		return value, err
	}
	value.Scope = restoreScope(value.Scope.Kind, work, conversation)
	value, err = knowledgedomain.RestoreFact(value)
	if err != nil {
		return value, knowledgeapp.ErrRepository
	}
	return value, nil
}

func insertKnowledgeEvent(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, kind, aggregateID, eventType string, from, to uint64, mutation knowledgeapp.Mutation, payload map[string]any) error {
	eventID, err := ids.Derive(mutation.CorrelationID, "knowledge-"+eventType+"-"+aggregateID)
	if err != nil {
		return knowledgeapp.ErrInvalid
	}
	var evidenceID, claimID, factID any
	switch kind {
	case "evidence":
		evidenceID = aggregateID
	case "claim":
		claimID = aggregateID
	case "fact":
		factID = aggregateID
	default:
		return knowledgeapp.ErrInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO spyglass.knowledge_events(account_id,id,aggregate_kind,evidence_id,claim_id,fact_id,event_type,from_version,to_version,actor_kind,actor_id,reason_code,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) ON CONFLICT (account_id,id) DO NOTHING`, accountID, eventID, kind, evidenceID, claimID, factID, eventType, from, to, mutation.Actor.Kind, mutation.Actor.ID, mutation.ReasonCode, mutation.CorrelationID, payload, mutation.At)
	return err
}

func scopeColumns(scope knowledgedomain.Scope) (any, any) {
	switch scope.Kind {
	case knowledgedomain.ScopeWorkItem:
		return scope.ID, nil
	case knowledgedomain.ScopeConversation:
		return nil, scope.ID
	default:
		return nil, nil
	}
}
func restoreScope(kind knowledgedomain.ScopeKind, workID, conversationID *string) knowledgedomain.Scope {
	scope := knowledgedomain.Scope{Kind: kind}
	if workID != nil {
		scope.ID = *workID
	} else if conversationID != nil {
		scope.ID = *conversationID
	}
	return scope
}
func nullableKnowledgeID(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func sameKnowledgeEvidence(left, right knowledgedomain.Evidence) bool {
	return left.ID == right.ID && left.AccountID == right.AccountID && left.Kind == right.Kind && left.SourceReference == right.SourceReference && left.SourceRevision == right.SourceRevision && left.ContentSHA256 == right.ContentSHA256 && left.CapturedAt.Equal(right.CapturedAt) && left.CreatedBy == right.CreatedBy
}
func sameKnowledgeClaim(left, right knowledgedomain.Claim) bool {
	return left.ID == right.ID && left.AccountID == right.AccountID && left.Scope == right.Scope && left.Key == right.Key && bytes.Equal(left.CanonicalValue, right.CanonicalValue) && left.ValueSHA256 == right.ValueSHA256 && left.HashVersion == right.HashVersion && left.Confidence == right.Confidence && left.Sensitivity == right.Sensitivity && left.ProposedBy == right.ProposedBy && left.State == right.State && left.Version == right.Version && slices.Equal(left.Citations, right.Citations)
}
func classifyKnowledge(err error) error {
	if err == nil || errors.Is(err, knowledgeapp.ErrInvalid) || errors.Is(err, knowledgeapp.ErrNotFound) || errors.Is(err, knowledgeapp.ErrConflict) || errors.Is(err, knowledgeapp.ErrConstraint) {
		return err
	}
	if errors.Is(err, knowledgedomain.ErrInvalid) {
		return knowledgeapp.ErrInvalid
	}
	if errors.Is(err, knowledgedomain.ErrConflict) {
		return knowledgeapp.ErrConflict
	}
	if errors.Is(err, knowledgedomain.ErrState) || errors.Is(err, knowledgedomain.ErrRole) {
		return knowledgeapp.ErrConstraint
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "40001":
			return knowledgeapp.ErrConflict
		case "23503", "23514":
			return knowledgeapp.ErrConstraint
		case "22023":
			return knowledgeapp.ErrInvalid
		}
		return fmt.Errorf("%w: database code %s", knowledgeapp.ErrRepository, pgErr.Code)
	}
	return fmt.Errorf("%w: %v", knowledgeapp.ErrRepository, err)
}

var _ knowledgeapp.Repository = (*KnowledgeRepository)(nil)
