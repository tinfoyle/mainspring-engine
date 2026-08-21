package postgres

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"

	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const maximumInformationCompletion = 500

// CompleteEligibleInformation answers an exact requirement cohort and plans
// only waiting parent Work items with no remaining open information blocker.
// Serializable isolation makes a concurrent matching request a retryable
// conflict instead of returning a stale resumption plan.
func (r *AttentionRepository) CompleteEligibleInformation(ctx context.Context, accountID ids.AccountID, command attentionapp.CompleteInformationCommand) (attentionapp.InformationCompletion, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(string(command.TargetID)) != nil || command.ExpectedVersion == 0 ||
		!command.Actor.Valid() || !command.Mutation.Valid() || command.Actor != command.Mutation.Actor || command.Mutation.At.IsZero() {
		return attentionapp.InformationCompletion{}, attentionapp.ErrInvalidCommand
	}
	completion := attentionapp.InformationCompletion{Answered: []domain.InformationRequest{}, ResumableParents: []ids.WorkItemID{}}
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		target, err := scanInformation(tx.QueryRow(ctx, informationSelect+` WHERE account_id=$1 AND id=$2 FOR UPDATE`, accountID, command.TargetID))
		if errors.Is(err, pgx.ErrNoRows) {
			return attentionapp.ErrNotFound
		}
		if err != nil {
			return err
		}
		replay := target.State == domain.InformationRequestAnswered
		if replay {
			if target.Version != command.ExpectedVersion+1 || target.AnswerRecord == nil || target.AnswerRecord.Fact != command.Fact || target.AnswerRecord.AnsweredBy != command.Actor {
				return attentionapp.ErrConflict
			}
		} else if target.State != domain.InformationRequestOpen || target.Version != command.ExpectedVersion {
			return attentionapp.ErrConflict
		}
		if target.State == domain.InformationRequestOpen && !target.EligibleFor(command.Fact) {
			return domain.ErrRequirementMismatch
		}

		parents := make(map[ids.WorkItemID]struct{})
		if replay {
			if err := r.restoreInformationCompletionReplay(ctx, tx, target, command, &completion, parents); err != nil {
				return err
			}
		} else {
			if err := r.completeOpenInformationCohort(ctx, tx, target, command, &completion, parents); err != nil {
				return err
			}
		}
		if len(completion.Answered) == 0 {
			return attentionapp.ErrConflict
		}

		parentValues := make([]string, 0, len(parents))
		for parentID := range parents {
			parentValues = append(parentValues, string(parentID))
		}
		sort.Strings(parentValues)
		if len(parentValues) == 0 {
			return nil
		}
		parentRows, err := tx.Query(ctx, `SELECT work.id FROM spyglass.work_items work
			WHERE work.account_id=$1 AND work.id=ANY($2::uuid[]) AND work.state='waiting'
			AND NOT EXISTS (SELECT 1 FROM spyglass.attention_information_requests request
				WHERE request.account_id=work.account_id AND request.parent_work_item_id=work.id AND request.state='open')
			ORDER BY work.id`, accountID, parentValues)
		if err != nil {
			return err
		}
		defer parentRows.Close()
		for parentRows.Next() {
			var parentID ids.WorkItemID
			if err := parentRows.Scan(&parentID); err != nil {
				return err
			}
			completion.ResumableParents = append(completion.ResumableParents, parentID)
		}
		return parentRows.Err()
	})
	if err != nil {
		return attentionapp.InformationCompletion{}, classifyAttentionError(err)
	}
	return completion, nil
}

func (r *AttentionRepository) restoreInformationCompletionReplay(ctx context.Context, tx pgx.Tx, target domain.InformationRequest, command attentionapp.CompleteInformationCommand, completion *attentionapp.InformationCompletion, parents map[ids.WorkItemID]struct{}) error {
	answer := target.AnswerRecord
	scopeWork, scopeConversation := informationScopeColumns(target.Requirement)
	rows, err := tx.Query(ctx, informationSelect+` WHERE account_id=$1 AND state='answered' AND fact_key=$2 AND scope_kind=$3
		AND scope_work_item_id IS NOT DISTINCT FROM $4::uuid AND scope_conversation_id IS NOT DISTINCT FROM $5::uuid
		AND fact_id=$6 AND fact_version=$7 AND answered_by_kind=$8 AND answered_by_id=$9 AND answered_at=$10
		AND EXISTS (SELECT 1 FROM spyglass.attention_events event WHERE event.account_id=spyglass.attention_information_requests.account_id
			AND event.information_request_id=spyglass.attention_information_requests.id AND event.event_type='information_answered'
			AND event.to_version=spyglass.attention_information_requests.version AND event.correlation_id=$11)
		ORDER BY parent_work_item_id,id LIMIT $12 FOR SHARE`, target.AccountID, target.Requirement.Key, target.Requirement.Scope,
		scopeWork, scopeConversation, answer.Fact.ID, answer.Fact.Version, answer.AnsweredBy.Kind, answer.AnsweredBy.ID,
		answer.AnsweredAt, command.Mutation.CorrelationID, maximumInformationCompletion+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanInformation(rows)
		if err != nil {
			return err
		}
		completion.Answered = append(completion.Answered, item)
		if len(completion.Answered) > maximumInformationCompletion {
			return attentionapp.ErrCompletionTooLarge
		}
		parents[item.ParentWorkItemID] = struct{}{}
	}
	return rows.Err()
}

func (r *AttentionRepository) completeOpenInformationCohort(ctx context.Context, tx pgx.Tx, target domain.InformationRequest, command attentionapp.CompleteInformationCommand, completion *attentionapp.InformationCompletion, parents map[ids.WorkItemID]struct{}) error {
	scopeWork, scopeConversation := informationScopeColumns(target.Requirement)
	rows, err := tx.Query(ctx, informationSelect+` WHERE account_id=$1 AND state='open' AND fact_key=$2 AND scope_kind=$3
		AND scope_work_item_id IS NOT DISTINCT FROM $4::uuid AND scope_conversation_id IS NOT DISTINCT FROM $5::uuid
		AND created_at<=$6 ORDER BY parent_work_item_id,id LIMIT $7 FOR UPDATE`, target.AccountID, target.Requirement.Key, target.Requirement.Scope,
		scopeWork, scopeConversation, command.Mutation.At.UTC(), maximumInformationCompletion+1)
	if err != nil {
		return err
	}
	eligible := make([]domain.InformationRequest, 0)
	for rows.Next() {
		item, err := scanInformation(rows)
		if err != nil {
			rows.Close()
			return err
		}
		eligible = append(eligible, item)
		if len(eligible) > maximumInformationCompletion {
			rows.Close()
			return attentionapp.ErrCompletionTooLarge
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range eligible {
		answered, err := item.Answer(domain.AnswerInformationCommand{
			Fact: command.Fact, Role: command.Role, Actor: command.Actor, ExpectedVersion: item.Version, At: command.Mutation.At,
		})
		if err != nil {
			return err
		}
		if err := r.updateInformationTx(ctx, tx, answered, item.Version, command.Mutation,
			answered.AnswerRecord.Fact.ID, answered.AnswerRecord.Fact.Version, answered.AnswerRecord.AnsweredBy.Kind,
			answered.AnswerRecord.AnsweredBy.ID, answered.AnswerRecord.AnsweredAt, nil, nil); err != nil {
			return err
		}
		completion.Answered = append(completion.Answered, answered)
		parents[answered.ParentWorkItemID] = struct{}{}
	}
	return nil
}
