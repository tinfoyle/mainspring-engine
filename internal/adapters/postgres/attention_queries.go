package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (r *AttentionRepository) ListInformation(ctx context.Context, accountID ids.AccountID, query attentionapp.InformationListQuery) (attentionapp.InformationPage, error) {
	if !validInformationQuery(query) {
		return attentionapp.InformationPage{}, attentionapp.ErrInvalidCommand
	}
	page := attentionapp.InformationPage{Items: make([]domain.InformationRequest, 0, query.Limit)}
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, informationSelect+` WHERE account_id=$1 AND ($2='' OR state=$2) AND ($3::uuid IS NULL OR parent_work_item_id=$3)
			AND ($4::timestamptz IS NULL OR (updated_at,id)<($4,$5::uuid)) ORDER BY updated_at DESC,id DESC LIMIT $6`,
			accountID, query.State, nullableID(query.ParentWorkItem), nullableCursorTime(query.AfterUpdatedAt), nullableID(query.AfterID), query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanInformation(rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return attentionapp.InformationPage{}, classifyAttentionError(err)
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &attentionapp.InformationCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	return page, nil
}

func (r *AttentionRepository) ListWorkReviews(ctx context.Context, accountID ids.AccountID, query attentionapp.WorkReviewListQuery) (attentionapp.WorkReviewPage, error) {
	if !validReviewQuery(query) {
		return attentionapp.WorkReviewPage{}, attentionapp.ErrInvalidCommand
	}
	page := attentionapp.WorkReviewPage{Items: make([]domain.WorkReview, 0, query.Limit)}
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, reviewSelect+` WHERE account_id=$1 AND ($2='' OR state=$2) AND ($3::uuid IS NULL OR reviewer_user_id=$3)
			AND ($4::uuid IS NULL OR work_item_id=$4) AND ($5::timestamptz IS NULL OR (updated_at,id)<($5,$6::uuid))
			ORDER BY updated_at DESC,id DESC LIMIT $7`, accountID, query.State, nullableID(query.ReviewerID), nullableID(query.WorkItemID),
			nullableCursorTime(query.AfterUpdatedAt), nullableID(query.AfterID), query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanWorkReview(rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return attentionapp.WorkReviewPage{}, classifyAttentionError(err)
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &attentionapp.WorkReviewCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	return page, nil
}

func (r *AttentionRepository) ListApprovals(ctx context.Context, accountID ids.AccountID, query attentionapp.ApprovalListQuery) (attentionapp.ApprovalPage, error) {
	if !validApprovalQuery(query) {
		return attentionapp.ApprovalPage{}, attentionapp.ErrInvalidCommand
	}
	page := attentionapp.ApprovalPage{Items: make([]domain.ConsequentialApproval, 0, query.Limit)}
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, approvalSelect+` WHERE account_id=$1 AND ($2='' OR state=$2) AND ($3::uuid IS NULL OR work_item_id=$3)
			AND ($4::timestamptz IS NULL OR (updated_at,id)<($4,$5::uuid)) ORDER BY updated_at DESC,id DESC LIMIT $6`,
			accountID, query.State, nullableID(query.WorkItemID), nullableCursorTime(query.AfterUpdatedAt), nullableID(query.AfterID), query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanApproval(rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return attentionapp.ApprovalPage{}, classifyAttentionError(err)
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &attentionapp.ApprovalCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	return page, nil
}

func validInformationQuery(query attentionapp.InformationListQuery) bool {
	return query.Limit > 0 && query.Limit <= attentionapp.MaxPageSize && (query.State == "" || query.State.Valid()) &&
		(query.ParentWorkItem == "" || ids.Validate(string(query.ParentWorkItem)) == nil) && validAttentionCursor(query.AfterUpdatedAt, string(query.AfterID))
}

func validReviewQuery(query attentionapp.WorkReviewListQuery) bool {
	return query.Limit > 0 && query.Limit <= attentionapp.MaxPageSize && (query.State == "" || query.State.Valid()) &&
		(query.ReviewerID == "" || ids.Validate(string(query.ReviewerID)) == nil) && (query.WorkItemID == "" || ids.Validate(string(query.WorkItemID)) == nil) &&
		validAttentionCursor(query.AfterUpdatedAt, string(query.AfterID))
}

func validApprovalQuery(query attentionapp.ApprovalListQuery) bool {
	return query.Limit > 0 && query.Limit <= attentionapp.MaxPageSize && (query.State == "" || query.State.Valid()) &&
		(query.WorkItemID == "" || ids.Validate(string(query.WorkItemID)) == nil) && validAttentionCursor(query.AfterUpdatedAt, string(query.AfterID))
}

func validAttentionCursor(at *time.Time, id string) bool {
	if (at == nil) != (id == "") {
		return false
	}
	return at == nil || (!at.IsZero() && ids.Validate(id) == nil)
}

func nullableCursorTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

var _ attentionapp.Repository = (*AttentionRepository)(nil)
