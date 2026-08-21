package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type WorkRepository struct {
	cell *database.CellPool
	ids  ids.Generator
}

func NewWorkRepository(cell *database.CellPool, generator ids.Generator) (*WorkRepository, error) {
	if cell == nil || generator == nil {
		return nil, errors.New("work repository dependencies are required")
	}
	return &WorkRepository{cell: cell, ids: generator}, nil
}

func (r *WorkRepository) Create(ctx context.Context, draft workdomain.Draft, mutation workapp.Mutation) (workdomain.Item, error) {
	var created workdomain.Item
	err := r.cell.WithAccountTx(ctx, draft.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		existing, found, err := loadWorkItem(ctx, tx, draft.AccountID, draft.ID)
		if err != nil {
			return err
		}
		if found {
			if sameDraft(existing, draft) {
				created = existing
				return nil
			}
			return workapp.ErrConflict
		}
		if err := validateWorkAssignment(ctx, tx, draft.AccountID, draft.Assignment); err != nil {
			return err
		}
		depth := uint8(0)
		if draft.ParentID != "" {
			var parentDepth uint8
			if err := tx.QueryRow(ctx, `SELECT depth FROM spyglass.work_items WHERE account_id=$1 AND id=$2`, draft.AccountID, draft.ParentID).Scan(&parentDepth); errors.Is(err, pgx.ErrNoRows) {
				return workapp.ErrConstraint
			} else if err != nil {
				return err
			}
			if parentDepth >= workdomain.MaxDepth {
				return workapp.ErrConstraint
			}
			depth = parentDepth + 1
		}
		var number uint64
		if err := tx.QueryRow(ctx, `
			INSERT INTO spyglass.work_item_number_counters (account_id,next_number) VALUES ($1,2)
			ON CONFLICT (account_id) DO UPDATE SET next_number=spyglass.work_item_number_counters.next_number+1
			RETURNING next_number-1`, draft.AccountID).Scan(&number); err != nil {
			return err
		}
		created, err = workdomain.Materialize(draft, number, depth, mutation.At)
		if err != nil {
			return workapp.ErrCorrupt
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO spyglass.work_items
			(account_id,id,number,parent_id,depth,kind,title,description,state,priority,responsibility,
			 assignee_user_id,assignee_persona_id,external_assignee_ref,source,created_by_actor_kind,created_by_actor_id,
			 baseline_requirement_id,schedule_id,conversation_id,run_id,due_at,completed_at,capacity_reservation_id,
			 capacity_released_at,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28)`,
			created.AccountID, created.ID, created.Number, nullableID(created.ParentID), created.Depth, created.Kind, created.Title, created.Description, created.State, created.Priority,
			created.Assignment.Responsibility, nullableID(created.Assignment.UserID), nullableString(created.Assignment.PersonaID), nullableString(created.Assignment.ExternalRef),
			created.Provenance.Source, created.Provenance.CreatedBy.Kind, created.Provenance.CreatedBy.ID, nullableString(created.Provenance.BaselineRequirementID), nullableString(created.Provenance.ScheduleID), nullableString(created.Provenance.ConversationID), nullableString(created.Provenance.RunID),
			created.DueAt, created.CompletedAt, created.CapacityReservationID, created.CapacityReleasedAt, created.Version, created.CreatedAt, created.UpdatedAt)
		if err != nil {
			return err
		}
		return r.insertEvent(ctx, tx, created, 0, mutation)
	})
	if err != nil {
		return workdomain.Item{}, classifyWorkError(err)
	}
	return created, nil
}

func (r *WorkRepository) Get(ctx context.Context, accountID ids.AccountID, itemID ids.WorkItemID) (workdomain.Item, error) {
	var item workdomain.Item
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		loaded, found, err := loadWorkItem(ctx, tx, accountID, itemID)
		if err != nil {
			return err
		}
		if !found {
			return workapp.ErrNotFound
		}
		item = loaded
		return nil
	})
	if err != nil {
		return workdomain.Item{}, classifyWorkError(err)
	}
	return item, nil
}

func (r *WorkRepository) Update(ctx context.Context, item workdomain.Item, expectedVersion uint64, mutation workapp.Mutation) (workdomain.Item, error) {
	if item.Version != expectedVersion+1 || mutation.Kind == "" {
		return workdomain.Item{}, workapp.ErrCorrupt
	}
	err := r.cell.WithAccountTx(ctx, item.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if mutation.Kind == workapp.MutationAssigned {
			if err := validateWorkAssignment(ctx, tx, item.AccountID, item.Assignment); err != nil {
				return err
			}
		}
		result, err := tx.Exec(ctx, `
			UPDATE spyglass.work_items SET
			 state=$3,priority=$4,responsibility=$5,assignee_user_id=$6,assignee_persona_id=$7,external_assignee_ref=$8,
			 baseline_requirement_id=$9,schedule_id=$10,conversation_id=$11,run_id=$12,due_at=$13,completed_at=$14,
			 capacity_reservation_id=$15,capacity_released_at=$16,version=$17,updated_at=$18
			WHERE account_id=$1 AND id=$2 AND version=$19`,
			item.AccountID, item.ID, item.State, item.Priority, item.Assignment.Responsibility, nullableID(item.Assignment.UserID), nullableString(item.Assignment.PersonaID), nullableString(item.Assignment.ExternalRef),
			nullableString(item.Provenance.BaselineRequirementID), nullableString(item.Provenance.ScheduleID), nullableString(item.Provenance.ConversationID), nullableString(item.Provenance.RunID), item.DueAt, item.CompletedAt,
			item.CapacityReservationID, item.CapacityReleasedAt, item.Version, item.UpdatedAt, expectedVersion)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return workapp.ErrConflict
		}
		return r.insertEvent(ctx, tx, item, expectedVersion, mutation)
	})
	if err != nil {
		return workdomain.Item{}, classifyWorkError(err)
	}
	return item, nil
}

// validateWorkAssignment is the persistence-side Persona assignment policy.
// The lock keeps Persona and Boardroom lifecycle writes from racing the Work
// write; existing Work can continue to reference a Persona that is retired
// later, while every new assignment requires an active, published identity.
func validateWorkAssignment(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, assignment workdomain.Assignment) error {
	if assignment.Responsibility != workdomain.ResponsibilityPersona {
		return nil
	}
	var eligible bool
	err := tx.QueryRow(ctx, `SELECT true
		FROM spyglass.agent_personas p
		JOIN spyglass.agent_boardrooms b ON b.account_id=p.account_id AND b.id=p.boardroom_id
		JOIN spyglass.agent_persona_versions v ON v.account_id=p.account_id AND v.persona_id=p.id AND v.version=p.latest_version
		WHERE p.account_id=$1 AND p.id=$2 AND p.state='active' AND b.state='active' AND p.latest_version>0
		FOR SHARE OF p,b,v`, accountID, assignment.PersonaID).Scan(&eligible)
	if errors.Is(err, pgx.ErrNoRows) {
		return workapp.ErrConstraint
	}
	if err != nil {
		return err
	}
	if !eligible {
		return workapp.ErrConstraint
	}
	return nil
}

func (r *WorkRepository) MarkCapacityReleased(ctx context.Context, accountID ids.AccountID, itemID ids.WorkItemID, reservationID string, at time.Time) error {
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `UPDATE spyglass.work_items SET capacity_released_at=$4 WHERE account_id=$1 AND id=$2 AND capacity_reservation_id=$3 AND state IN ('done','canceled') AND capacity_released_at IS NULL`, accountID, itemID, reservationID, at.UTC())
		if err != nil {
			return err
		}
		if result.RowsAffected() == 1 {
			return completeQueuedCapacityRelease(ctx, tx, accountID, itemID, reservationID, at)
		}
		var currentReservation string
		var released *time.Time
		if err := tx.QueryRow(ctx, `SELECT capacity_reservation_id,capacity_released_at FROM spyglass.work_items WHERE account_id=$1 AND id=$2`, accountID, itemID).Scan(&currentReservation, &released); errors.Is(err, pgx.ErrNoRows) {
			return workapp.ErrNotFound
		} else if err != nil {
			return err
		}
		if currentReservation == reservationID && released != nil {
			return completeQueuedCapacityRelease(ctx, tx, accountID, itemID, reservationID, at)
		}
		return workapp.ErrConflict
	})
	return classifyWorkError(err)
}

func completeQueuedCapacityRelease(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, itemID ids.WorkItemID, reservationID string, at time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE spyglass.work_capacity_release_queue SET
		processing_state='completed',completed_at=COALESCE(completed_at,$4),next_attempt_at=NULL,
		lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL
		WHERE account_id=$1 AND work_item_id=$2 AND reservation_id=$3 AND processing_state<>'completed'`, accountID, itemID, reservationID, at.UTC())
	return err
}

func (r *WorkRepository) List(ctx context.Context, accountID ids.AccountID, query workapp.ListQuery) (workapp.Page, error) {
	page := workapp.Page{Items: make([]workdomain.Item, 0, query.Limit)}
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		states := make([]string, len(query.States))
		for index, value := range query.States {
			states[index] = string(value)
		}
		kinds := make([]string, len(query.Kinds))
		for index, value := range query.Kinds {
			kinds[index] = string(value)
		}
		var cursorID any
		if query.AfterID != "" {
			cursorID = query.AfterID
		}
		rows, err := tx.Query(ctx, workSelect+`
			WHERE account_id=$1
			  AND (cardinality($2::text[])=0 OR state=ANY($2::text[]))
			  AND (cardinality($3::text[])=0 OR kind=ANY($3::text[]))
			  AND ($4='' OR title ILIKE '%' || $4 || '%' OR description ILIKE '%' || $4 || '%')
			  AND ($5::timestamptz IS NULL OR (updated_at,id)<($5,$6::uuid))
			ORDER BY updated_at DESC,id DESC LIMIT $7`, accountID, states, kinds, strings.TrimSpace(query.Search), query.AfterUpdatedAt, cursorID, query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanWorkItem(rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(page.Items) > query.Limit {
			last := page.Items[query.Limit-1]
			page.Items = page.Items[:query.Limit]
			page.NextCursor = &workapp.Cursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
		}
		return nil
	})
	if err != nil {
		return workapp.Page{}, classifyWorkError(err)
	}
	return page, nil
}

func (r *WorkRepository) Children(ctx context.Context, accountID ids.AccountID, parentID ids.WorkItemID, limit int) ([]workdomain.Item, error) {
	items := make([]workdomain.Item, 0, limit)
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, workSelect+` WHERE account_id=$1 AND parent_id=$2 ORDER BY created_at,id LIMIT $3`, accountID, parentID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanWorkItem(rows)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, classifyWorkError(err)
	}
	return items, nil
}

func (r *WorkRepository) Summary(ctx context.Context, accountID ids.AccountID) (workapp.Summary, error) {
	var summary workapp.Summary
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE state IN ('open','in_progress','waiting')),
			count(*) FILTER (WHERE state='in_progress'),count(*) FILTER (WHERE state='waiting'),
			count(*) FILTER (WHERE priority='urgent' AND state IN ('open','in_progress','waiting')),
			count(*) FILTER (WHERE state='done') FROM spyglass.work_items WHERE account_id=$1`, accountID).Scan(&summary.Active, &summary.InProgress, &summary.Waiting, &summary.Urgent, &summary.Done)
	})
	if err != nil {
		return workapp.Summary{}, classifyWorkError(err)
	}
	return summary, nil
}

func (r *WorkRepository) insertEvent(ctx context.Context, tx pgx.Tx, item workdomain.Item, fromVersion uint64, mutation workapp.Mutation) error {
	switch mutation.Kind {
	case workapp.MutationProvenance:
		if mutation.ReferenceKind != string(workdomain.ProvenanceBaselineRequirement) && mutation.ReferenceKind != string(workdomain.ProvenanceSchedule) && mutation.ReferenceKind != string(workdomain.ProvenanceRun) {
			return workapp.ErrCorrupt
		}
	case workapp.MutationConversation:
		if mutation.ReferenceKind != "conversation" {
			return workapp.ErrCorrupt
		}
	case workapp.MutationCreated, workapp.MutationTransitioned, workapp.MutationAssigned:
		if mutation.ReferenceKind != "" {
			return workapp.ErrCorrupt
		}
	default:
		return workapp.ErrCorrupt
	}
	_, err := tx.Exec(ctx, `INSERT INTO spyglass.work_item_events
		(account_id,id,work_item_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,
			jsonb_strip_nulls(jsonb_build_object('state',$11::text,'responsibility',$12::text,'reference_kind',NULLIF($13,''))),$14)`,
		item.AccountID, r.ids.New(), item.ID, mutation.Kind, fromVersion, item.Version, mutation.Actor.Kind, mutation.Actor.ID, mutation.Reason, mutation.CorrelationID, item.State, item.Assignment.Responsibility, mutation.ReferenceKind, mutation.At.UTC())
	return err
}

const workSelect = `SELECT account_id,id,number,parent_id,depth,kind,title,description,state,priority,responsibility,
	assignee_user_id,assignee_persona_id,external_assignee_ref,source,created_by_actor_kind,created_by_actor_id,
	baseline_requirement_id,schedule_id,conversation_id,run_id,due_at,completed_at,capacity_reservation_id,
	capacity_released_at,version,created_at,updated_at FROM spyglass.work_items`

type workRowScanner interface{ Scan(...any) error }

func loadWorkItem(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, itemID ids.WorkItemID) (workdomain.Item, bool, error) {
	item, err := scanWorkItem(tx.QueryRow(ctx, workSelect+` WHERE account_id=$1 AND id=$2`, accountID, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return workdomain.Item{}, false, nil
	}
	return item, err == nil, err
}

func scanWorkItem(row workRowScanner) (workdomain.Item, error) {
	var item workdomain.Item
	var parent, user, persona, external, baseline, schedule, conversation, run *string
	err := row.Scan(&item.AccountID, &item.ID, &item.Number, &parent, &item.Depth, &item.Kind, &item.Title, &item.Description, &item.State, &item.Priority, &item.Assignment.Responsibility,
		&user, &persona, &external, &item.Provenance.Source, &item.Provenance.CreatedBy.Kind, &item.Provenance.CreatedBy.ID,
		&baseline, &schedule, &conversation, &run, &item.DueAt, &item.CompletedAt, &item.CapacityReservationID, &item.CapacityReleasedAt, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return workdomain.Item{}, err
	}
	if parent != nil {
		item.ParentID = ids.WorkItemID(*parent)
	}
	if user != nil {
		item.Assignment.UserID = ids.UserID(*user)
	}
	if persona != nil {
		item.Assignment.PersonaID = *persona
	}
	if external != nil {
		item.Assignment.ExternalRef = *external
	}
	if baseline != nil {
		item.Provenance.BaselineRequirementID = *baseline
	}
	if schedule != nil {
		item.Provenance.ScheduleID = *schedule
	}
	if conversation != nil {
		item.Provenance.ConversationID = *conversation
	}
	if run != nil {
		item.Provenance.RunID = *run
	}
	item, err = workdomain.Restore(item)
	if err != nil {
		return workdomain.Item{}, workapp.ErrCorrupt
	}
	return item, nil
}

func sameDraft(item workdomain.Item, draft workdomain.Draft) bool {
	return item.AccountID == draft.AccountID && item.ParentID == draft.ParentID && item.Kind == draft.Kind && item.Title == draft.Title && item.Description == draft.Description && item.Priority == draft.Priority &&
		item.Assignment == draft.Assignment && item.Provenance == draft.Provenance && equalTime(item.DueAt, draft.DueAt) && item.CapacityReservationID == draft.CapacityReservationID
}

func equalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func nullableString[T ~string](value T) any {
	if value == "" {
		return nil
	}
	return string(value)
}
func nullableID[T ~string](value T) any { return nullableString(value) }

func classifyWorkError(err error) error {
	if err == nil || errors.Is(err, workapp.ErrNotFound) || errors.Is(err, workapp.ErrConflict) || errors.Is(err, workapp.ErrConstraint) || errors.Is(err, workapp.ErrCorrupt) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503", "23505", "23514", "42501":
			return fmt.Errorf("%w: %s", workapp.ErrConstraint, pgErr.ConstraintName)
		case "40001", "40P01":
			return workapp.ErrConflict
		}
	}
	return fmt.Errorf("work repository unavailable: %w", err)
}

var _ workapp.Repository = (*WorkRepository)(nil)
