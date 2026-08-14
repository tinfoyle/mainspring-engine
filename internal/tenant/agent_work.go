package tenant

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/boardroom"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

const agentWorkPrompt = `Work this ticket autonomously and bring the owner in only where their judgment or private business knowledge is necessary.

First inspect the ticket, its attached documents, and the tenant document library. Decide whether the ticket and definition of done are specific enough to begin. Do not guess missing business facts.

If a missing fact can be answered from public information, use web.search and web.read, prefer primary authoritative sources, and cite what you relied on. If it can be answered from tenant records, search those records first. Complete every part that can be completed safely with the available evidence.

If essential business-specific information remains unavailable after research, state only the smallest precise questions the owner must answer in the structured questions field. Mainspring will group them into one owner-input request attached to this ticket and resume you automatically after the owner responds.

Finish with a concise review package: work completed, evidence used, unresolved blockers, and the recommended disposition. Do not claim the ticket is complete and do not make external changes; the owner will review your work before Mainspring closes it.`

type AgentWorkJob struct {
	ID             string
	Number         int64
	Title          string
	PersonaID      string
	BoardroomID    string
	UserID         string
	ConversationID string
}

func (s *Store) ClaimAgentWork(ctx context.Context, maximum int) ([]AgentWorkJob, error) {
	if maximum <= 0 {
		maximum = 1
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Recover a process that claimed a ticket but stopped before creating its
	// durable run. Linked runs are reconciled separately and are never replayed.
	if _, err := tx.Exec(ctx, `
		UPDATE work_items SET status='open', updated_at=now()
		WHERE responsibility='agent' AND status='in_progress' AND run_id IS NULL
		  AND updated_at < now()-interval '2 minutes'
	`); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		WITH policy AS (
			SELECT LEAST($1, max_concurrent_invocations)::integer AS maximum
			FROM tenant_execution_policy WHERE singleton
		), capacity AS (
			SELECT GREATEST((SELECT maximum FROM policy)-count(*), 0)::integer AS available
			FROM work_items
			WHERE responsibility='agent' AND status='in_progress'
		), selected AS (
			SELECT w.id, w.number, w.title, w.assigned_persona_id,
			       p.boardroom_id, COALESCE(w.created_by, owner_user.id) AS user_id,
			       COALESCE(w.conversation_id::text,'') AS conversation_id
			FROM work_items w
			JOIN personas p ON p.id=w.assigned_persona_id AND p.enabled
			CROSS JOIN LATERAL (
				SELECT id FROM users WHERE role='owner' AND state='active' ORDER BY created_at LIMIT 1
			) owner_user
			WHERE w.responsibility='agent' AND (
				(w.status='open' AND w.run_id IS NULL)
				OR (
					w.status='waiting' AND w.conversation_id IS NOT NULL
					AND EXISTS (SELECT 1 FROM work_items child WHERE child.parent_id=w.id)
					AND NOT EXISTS (SELECT 1 FROM work_items child WHERE child.parent_id=w.id AND child.status IN ('open','in_progress','waiting'))
					AND NOT EXISTS (
						SELECT 1 FROM approval_requests ar WHERE ar.run_id=w.run_id AND ar.status='pending'
					)
				)
			)
			ORDER BY CASE w.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END,
			         w.created_at, w.number
			LIMIT (SELECT available FROM capacity)
			FOR UPDATE OF w SKIP LOCKED
		), claimed AS (
			UPDATE work_items w SET status='in_progress', updated_at=now()
			FROM selected s WHERE w.id=s.id
			RETURNING s.id::text, s.number, s.title, s.assigned_persona_id::text,
			          s.boardroom_id::text, s.user_id::text, s.conversation_id
		)
		SELECT * FROM claimed ORDER BY number
	`, maximum)
	if err != nil {
		return nil, fmt.Errorf("claim agent work: %w", err)
	}
	var jobs []AgentWorkJob
	for rows.Next() {
		var job AgentWorkJob
		if err := rows.Scan(&job.ID, &job.Number, &job.Title, &job.PersonaID, &job.BoardroomID, &job.UserID, &job.ConversationID); err != nil {
			rows.Close()
			return nil, err
		}
		jobs = append(jobs, job)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *Store) RequeueAgentWork(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE work_items SET status=CASE WHEN conversation_id IS NULL THEN 'open' ELSE 'waiting' END, updated_at=now()
		WHERE id=$1 AND responsibility='agent'
	`, id)
	return err
}

func (s *Store) ReconcileAgentWork(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE work_items w SET status='waiting', updated_at=now()
		FROM boardroom_runs r
		WHERE w.run_id=r.id AND w.responsibility='agent' AND w.status='in_progress'
		  AND r.status IN ('failed','canceled')
	`)
	return err
}

func (s *Store) HumanInputDocuments(ctx context.Context, workItemID string) ([]boardroom.DocumentAttachment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT document.id::text, document.name
		FROM human_input_requests request
		CROSS JOIN LATERAL jsonb_array_elements_text(request.response_document_ids) selected(document_id)
		JOIN documents document ON document.id=selected.document_id::uuid
		WHERE request.parent_work_item_id=$1 AND request.status='answered'
		ORDER BY document.name
	`, workItemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []boardroom.DocumentAttachment
	for rows.Next() {
		var item boardroom.DocumentAttachment
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func RunAgentWorkDispatcher(ctx context.Context, logger *slog.Logger, store *Store, rooms *boardroom.Store, dispatcher RunDispatcher, maximumConcurrent int, interval time.Duration) {
	if maximumConcurrent <= 0 {
		maximumConcurrent = 1
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	run := func() {
		if err := store.ReconcileAgentWork(ctx); err != nil {
			logger.Warn("reconcile agent work", "error", err)
			return
		}
		jobs, err := store.ClaimAgentWork(ctx, maximumConcurrent)
		if err != nil {
			logger.Warn("claim agent work", "error", err)
			return
		}
		for _, job := range jobs {
			boardroomID, parseErr := domain.ParseBoardroomID(job.BoardroomID)
			if parseErr != nil {
				_ = store.RequeueAgentWork(ctx, job.ID)
				continue
			}
			var run boardroom.Run
			var runErr error
			documents, documentErr := store.HumanInputDocuments(ctx, job.ID)
			if documentErr != nil {
				_ = store.RequeueAgentWork(ctx, job.ID)
				logger.Error("load agent work owner documents", "work_item_id", job.ID, "error", documentErr)
				continue
			}
			if job.ConversationID == "" {
				run, runErr = rooms.CreateTargetedRunWithDocuments(ctx, boardroomID, job.UserID,
					fmt.Sprintf("Agent work #%04d: %s", job.Number, job.Title), agentWorkPrompt, job.ID, []string{job.PersonaID}, documents)
			} else if conversationID, parseConversationErr := domain.ParseConversationID(job.ConversationID); parseConversationErr != nil {
				runErr = parseConversationErr
			} else {
				run, runErr = rooms.CreateTargetedFollowUpRunWithDocuments(ctx, conversationID, job.UserID,
					"This ticket is ready for another autonomous pass. Review the complete conversation and ticket context, including any newly completed owner subtasks or evidence from the prior attempt. Continue the work, research externally answerable gaps, and return only the remaining necessary owner decisions for approval.",
					job.ID, []string{job.PersonaID}, documents)
			}
			if runErr == nil {
				runErr = store.LinkWorkItemConversation(ctx, job.ID, job.BoardroomID, run.ConversationID.String(), run.ID.String())
			}
			if runErr == nil {
				runErr = dispatcher.Dispatch(ctx, run.ID)
			}
			if runErr != nil {
				_ = store.RequeueAgentWork(ctx, job.ID)
				logger.Error("start agent work", "work_item_id", job.ID, "error", runErr)
				continue
			}
			logger.Info("agent work started", "work_item_id", job.ID, "run_id", run.ID.String(), "persona_id", job.PersonaID)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
