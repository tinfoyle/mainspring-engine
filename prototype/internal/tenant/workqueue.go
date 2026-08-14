package tenant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrWorkItemNotFound = errors.New("work item not found")

type WorkItem struct {
	ID             string
	Number         int64
	Kind           string
	Title          string
	Description    string
	Status         string
	Priority       string
	Source         string
	CreatedByName  string
	AssignedToName string
	AssignedToType string
	Responsibility string
	BoardroomID    string
	ConversationID string
	RunID          string
	ParentID       string
	ParentNumber   int64
	DueAt          *time.Time
	CompletedAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type WorkFilter struct {
	Status string
	Kind   string
	Query  string
}

type WorkSummary struct {
	Active     int
	InProgress int
	Waiting    int
	Urgent     int
	Done       int
}

type CreateWorkItemInput struct {
	Kind                  string
	Title                 string
	Description           string
	Priority              string
	Source                string
	CreatedByUserID       string
	AssignedUserID        string
	AssignedPersonaID     string
	BoardroomID           string
	ConversationID        string
	RunID                 string
	ParentID              string
	Responsibility        string
	BaselineRequirementID string
	DueAt                 *time.Time
}

func NormalizeWorkFilter(filter WorkFilter) WorkFilter {
	filter.Status = strings.ToLower(strings.TrimSpace(filter.Status))
	switch filter.Status {
	case "all", "open", "in_progress", "waiting", "done":
	default:
		filter.Status = "active"
	}
	filter.Kind = strings.ToLower(strings.TrimSpace(filter.Kind))
	if filter.Kind != "todo" && filter.Kind != "ticket" {
		filter.Kind = "all"
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if len(filter.Query) > 200 {
		filter.Query = filter.Query[:200]
	}
	return filter
}

func (s *Store) ListWorkItems(ctx context.Context, filter WorkFilter) ([]WorkItem, WorkSummary, error) {
	filter = NormalizeWorkFilter(filter)
	rows, err := s.pool.Query(ctx, `
		SELECT w.id::text, w.number, w.kind, w.title, w.description, w.status, w.priority, w.source,
		       COALESCE(creator.display_name, ''),
		       COALESCE(assignee.display_name, persona.name, ''),
		       CASE WHEN w.assigned_user_id IS NOT NULL THEN 'user'
		            WHEN w.assigned_persona_id IS NOT NULL THEN 'persona' ELSE '' END,
		       w.responsibility,
		       COALESCE(w.boardroom_id::text, ''), COALESCE(w.conversation_id::text, ''), COALESCE(w.run_id::text, ''),
		       COALESCE(w.parent_id::text, ''), COALESCE(parent.number, 0),
		       w.due_at, w.completed_at, w.created_at, w.updated_at
		FROM work_items w
		LEFT JOIN work_items parent ON parent.id = w.parent_id
		LEFT JOIN users creator ON creator.id = w.created_by
		LEFT JOIN users assignee ON assignee.id = w.assigned_user_id
		LEFT JOIN personas persona ON persona.id = w.assigned_persona_id
		WHERE ($1 = 'all'
		       OR ($1 = 'active' AND w.status IN ('open', 'in_progress', 'waiting'))
		       OR w.status = $1)
		  AND ($2 = 'all' OR w.kind = $2)
		  AND ($3 = '' OR w.title ILIKE '%' || $3 || '%' OR w.description ILIKE '%' || $3 || '%')
		ORDER BY
		  CASE w.status WHEN 'in_progress' THEN 0 WHEN 'waiting' THEN 1 WHEN 'open' THEN 2 WHEN 'done' THEN 3 ELSE 4 END,
		  CASE w.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END,
		  w.due_at ASC NULLS LAST, w.updated_at DESC
		LIMIT 250
	`, filter.Status, filter.Kind, filter.Query)
	if err != nil {
		return nil, WorkSummary{}, fmt.Errorf("list work items: %w", err)
	}
	defer rows.Close()

	var items []WorkItem
	for rows.Next() {
		var item WorkItem
		if err := rows.Scan(
			&item.ID, &item.Number, &item.Kind, &item.Title, &item.Description, &item.Status, &item.Priority, &item.Source,
			&item.CreatedByName, &item.AssignedToName, &item.AssignedToType, &item.Responsibility,
			&item.BoardroomID, &item.ConversationID, &item.RunID, &item.ParentID, &item.ParentNumber,
			&item.DueAt, &item.CompletedAt, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, WorkSummary{}, fmt.Errorf("scan work item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, WorkSummary{}, fmt.Errorf("iterate work items: %w", err)
	}

	var summary WorkSummary
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE status IN ('open', 'in_progress', 'waiting')),
		  count(*) FILTER (WHERE status = 'in_progress'),
		  count(*) FILTER (WHERE status = 'waiting'),
		  count(*) FILTER (WHERE priority = 'urgent' AND status IN ('open', 'in_progress', 'waiting')),
		  count(*) FILTER (WHERE status = 'done')
		FROM work_items
	`).Scan(&summary.Active, &summary.InProgress, &summary.Waiting, &summary.Urgent, &summary.Done); err != nil {
		return nil, WorkSummary{}, fmt.Errorf("summarize work queue: %w", err)
	}
	return items, summary, nil
}

func (s *Store) CreateWorkItem(ctx context.Context, input CreateWorkItemInput) (WorkItem, error) {
	input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	if input.Kind != "todo" && input.Kind != "ticket" {
		return WorkItem{}, errors.New("work item kind must be todo or ticket")
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" || len(input.Title) > 240 {
		return WorkItem{}, errors.New("work item title must contain between 1 and 240 characters")
	}
	input.Description = strings.TrimSpace(input.Description)
	if len(input.Description) > 6000 {
		return WorkItem{}, errors.New("work item description must not exceed 6000 characters")
	}
	input.Priority = strings.ToLower(strings.TrimSpace(input.Priority))
	switch input.Priority {
	case "low", "normal", "high", "urgent":
	default:
		return WorkItem{}, errors.New("work item priority is invalid")
	}
	input.Source = strings.ToLower(strings.TrimSpace(input.Source))
	switch input.Source {
	case "user", "persona", "schedule", "system":
	default:
		return WorkItem{}, errors.New("work item source is invalid")
	}
	input.Responsibility = strings.ToLower(strings.TrimSpace(input.Responsibility))
	if input.Responsibility == "" {
		if input.AssignedPersonaID != "" {
			input.Responsibility = "agent"
		} else {
			input.Responsibility = "owner"
		}
	}
	switch input.Responsibility {
	case "agent", "owner", "shared", "external":
	default:
		return WorkItem{}, errors.New("work item responsibility is invalid")
	}
	for name, value := range map[string]string{
		"created user": input.CreatedByUserID, "assigned user": input.AssignedUserID,
		"assigned persona": input.AssignedPersonaID, "boardroom": input.BoardroomID,
		"conversation": input.ConversationID, "run": input.RunID, "parent work item": input.ParentID,
		"baseline requirement": input.BaselineRequirementID,
	} {
		if value != "" {
			if _, err := uuid.Parse(value); err != nil {
				return WorkItem{}, fmt.Errorf("%s id is invalid", name)
			}
		}
	}

	var item WorkItem
	err := s.pool.QueryRow(ctx, `
		INSERT INTO work_items (
		  kind, title, description, priority, source, created_by, assigned_user_id, assigned_persona_id,
		  boardroom_id, conversation_id, run_id, parent_id, responsibility, baseline_requirement_id, due_at
		) VALUES (
		  $1, $2, $3, $4, $5, NULLIF($6, '')::uuid, NULLIF($7, '')::uuid, NULLIF($8, '')::uuid,
		  NULLIF($9, '')::uuid, NULLIF($10, '')::uuid, NULLIF($11, '')::uuid, NULLIF($12, '')::uuid, $13, NULLIF($14, '')::uuid, $15
		)
		RETURNING id::text, number, kind, title, description, status, priority, source,
		          responsibility, due_at, completed_at, created_at, updated_at
	`, input.Kind, input.Title, input.Description, input.Priority, input.Source,
		input.CreatedByUserID, input.AssignedUserID, input.AssignedPersonaID,
		input.BoardroomID, input.ConversationID, input.RunID, input.ParentID, input.Responsibility, input.BaselineRequirementID, input.DueAt,
	).Scan(
		&item.ID, &item.Number, &item.Kind, &item.Title, &item.Description, &item.Status, &item.Priority, &item.Source,
		&item.Responsibility, &item.DueAt, &item.CompletedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return WorkItem{}, fmt.Errorf("create work item: %w", err)
	}
	return item, nil
}

func (s *Store) GetWorkItem(ctx context.Context, id string) (WorkItem, error) {
	if _, err := uuid.Parse(id); err != nil {
		return WorkItem{}, ErrWorkItemNotFound
	}
	var item WorkItem
	err := s.pool.QueryRow(ctx, `
		SELECT w.id::text, w.number, w.kind, w.title, w.description, w.status, w.priority, w.source,
		       COALESCE(creator.display_name, ''), COALESCE(assignee.display_name, persona.name, ''),
		       CASE WHEN w.assigned_user_id IS NOT NULL THEN 'user' WHEN w.assigned_persona_id IS NOT NULL THEN 'persona' ELSE '' END,
		       w.responsibility,
		       COALESCE(w.boardroom_id::text, ''), COALESCE(w.conversation_id::text, ''), COALESCE(w.run_id::text, ''),
		       COALESCE(w.parent_id::text, ''), COALESCE(parent.number, 0),
		       w.due_at, w.completed_at, w.created_at, w.updated_at
		FROM work_items w
		LEFT JOIN work_items parent ON parent.id=w.parent_id
		LEFT JOIN users creator ON creator.id=w.created_by
		LEFT JOIN users assignee ON assignee.id=w.assigned_user_id
		LEFT JOIN personas persona ON persona.id=w.assigned_persona_id
		WHERE w.id=$1
	`, id).Scan(
		&item.ID, &item.Number, &item.Kind, &item.Title, &item.Description, &item.Status, &item.Priority, &item.Source,
		&item.CreatedByName, &item.AssignedToName, &item.AssignedToType, &item.Responsibility,
		&item.BoardroomID, &item.ConversationID, &item.RunID, &item.ParentID, &item.ParentNumber,
		&item.DueAt, &item.CompletedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkItem{}, ErrWorkItemNotFound
	}
	if err != nil {
		return WorkItem{}, fmt.Errorf("get work item: %w", err)
	}
	return item, nil
}

func (s *Store) ListSubtasks(ctx context.Context, parentID string) ([]WorkItem, error) {
	if _, err := uuid.Parse(parentID); err != nil {
		return nil, ErrWorkItemNotFound
	}
	items, _, err := s.ListWorkItems(ctx, WorkFilter{Status: "all", Kind: "all"})
	if err != nil {
		return nil, err
	}
	result := make([]WorkItem, 0)
	for _, item := range items {
		if item.ParentID == parentID {
			result = append(result, item)
		}
	}
	return result, nil
}

func (s *Store) LinkWorkItemConversation(ctx context.Context, id, boardroomID, conversationID, runID string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE work_items
		SET boardroom_id=$2, conversation_id=$3, run_id=$4, updated_at=now()
		WHERE id=$1
	`, id, boardroomID, conversationID, runID)
	if err != nil {
		return fmt.Errorf("link work item conversation: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrWorkItemNotFound
	}
	return nil
}

func (s *Store) UpdateWorkItemStatus(ctx context.Context, id, status string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrWorkItemNotFound
	}
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "open", "in_progress", "waiting", "done", "canceled":
	default:
		return errors.New("work item status is invalid")
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE work_items
		SET status = $2,
		    completed_at = CASE WHEN $2 = 'done' THEN COALESCE(completed_at, now()) ELSE NULL END,
		    updated_at = now()
		WHERE id = $1
	`, id, status)
	if err != nil {
		return fmt.Errorf("update work item status: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrWorkItemNotFound
	}
	if status == "done" {
		_, _ = s.pool.Exec(ctx, `
			WITH resolved AS (
				UPDATE evidence_requirements er
				SET status='confirmed', updated_at=now()
				FROM work_items wi
				WHERE wi.id=$1 AND wi.baseline_requirement_id=er.id
				RETURNING er.assessment_id, er.id AS requirement_id
			)
			UPDATE baseline_assessments ba
			SET status='ready', phase='baseline_ready', completed_at=now(), last_assessed_at=now(),
			    next_reassessment_at=now()+interval '3 months', updated_at=now()
			WHERE ba.id IN (SELECT assessment_id FROM resolved)
			  AND NOT EXISTS (
				SELECT 1 FROM evidence_requirements pending
				WHERE pending.assessment_id=ba.id AND pending.required
				  AND pending.id NOT IN (SELECT requirement_id FROM resolved)
				  AND pending.status NOT IN ('confirmed','not_applicable')
			  )
		`, id)
	}
	return nil
}
