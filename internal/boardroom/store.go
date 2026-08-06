package boardroom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/mainspring-engine/internal/agent"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

var (
	ErrBoardroomNotFound    = errors.New("boardroom not found")
	ErrRunNotFound          = errors.New("boardroom run not found")
	ErrConversationNotFound = errors.New("conversation not found")
	ErrConversationBusy     = errors.New("conversation already has an active boardroom run")
)

type Summary struct {
	ID           domain.BoardroomID
	Name         string
	Description  string
	MaxTurns     int
	Status       string
	PersonaCount int
	CreatedAt    time.Time
}

type Persona struct {
	ID                 domain.PersonaID
	Name               string
	Role               string
	SystemInstructions string
	Position           int
	Grants             []domain.ToolGrant
}

type Run struct {
	ID             domain.RunID
	BoardroomID    domain.BoardroomID
	ConversationID domain.ConversationID
	Status         domain.RunStatus
	Prompt         string
	TurnCount      int
	MaxTurns       int
	Error          string
	CreatedAt      time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
}

type Conversation struct {
	ID           domain.ConversationID
	BoardroomID  domain.BoardroomID
	Title        string
	Source       string
	Status       string
	LatestRunID  domain.RunID
	LatestStatus domain.RunStatus
	LatestPrompt string
	MessageCount int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Message struct {
	ID          string
	RunID       domain.RunID
	PersonaID   *domain.PersonaID
	PersonaName string
	PersonaRole string
	Role        domain.MessageRole
	Body        string
	Sequence    int64
	CreatedAt   time.Time
}

type Event struct {
	ID        int64
	RunID     domain.RunID
	Type      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) List(ctx context.Context) ([]Summary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.id::text, b.name, b.description, b.max_turns, b.status, b.created_at,
		       count(p.id) FILTER (WHERE p.enabled)
		FROM boardrooms b
		LEFT JOIN personas p ON p.boardroom_id = b.id
		WHERE b.status <> 'archived'
		GROUP BY b.id
		ORDER BY b.created_at
	`)
	if err != nil {
		return nil, fmt.Errorf("list boardrooms: %w", err)
	}
	defer rows.Close()

	var result []Summary
	for rows.Next() {
		var item Summary
		var id string
		if err := rows.Scan(&id, &item.Name, &item.Description, &item.MaxTurns, &item.Status, &item.CreatedAt, &item.PersonaCount); err != nil {
			return nil, fmt.Errorf("scan boardroom: %w", err)
		}
		item.ID, err = domain.ParseBoardroomID(id)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) Get(ctx context.Context, id domain.BoardroomID) (Summary, error) {
	var item Summary
	var idText string
	err := s.pool.QueryRow(ctx, `
		SELECT b.id::text, b.name, b.description, b.max_turns, b.status, b.created_at,
		       count(p.id) FILTER (WHERE p.enabled)
		FROM boardrooms b
		LEFT JOIN personas p ON p.boardroom_id = b.id
		WHERE b.id = $1 AND b.status <> 'archived'
		GROUP BY b.id
	`, id.String()).Scan(&idText, &item.Name, &item.Description, &item.MaxTurns, &item.Status, &item.CreatedAt, &item.PersonaCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Summary{}, ErrBoardroomNotFound
	}
	if err != nil {
		return Summary{}, fmt.Errorf("get boardroom: %w", err)
	}
	item.ID, err = domain.ParseBoardroomID(idText)
	return item, err
}

func (s *Store) Personas(ctx context.Context, boardroomID domain.BoardroomID) ([]Persona, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id::text, p.name, p.role, p.system_instructions, p.position,
		       COALESCE(jsonb_agg(jsonb_build_object('capability', g.capability, 'conditions', g.conditions))
		           FILTER (WHERE g.capability IS NOT NULL), '[]'::jsonb)
		FROM personas p
		LEFT JOIN persona_tool_grants g ON g.persona_id = p.id
		WHERE p.boardroom_id = $1 AND p.enabled
		GROUP BY p.id
		ORDER BY p.position
	`, boardroomID.String())
	if err != nil {
		return nil, fmt.Errorf("list personas: %w", err)
	}
	defer rows.Close()

	var result []Persona
	for rows.Next() {
		var item Persona
		var id string
		var grantsJSON []byte
		if err := rows.Scan(&id, &item.Name, &item.Role, &item.SystemInstructions, &item.Position, &grantsJSON); err != nil {
			return nil, fmt.Errorf("scan persona: %w", err)
		}
		item.ID, err = domain.ParsePersonaID(id)
		if err != nil {
			return nil, err
		}
		var grants []struct {
			Capability string            `json:"capability"`
			Conditions map[string]string `json:"conditions"`
		}
		if err := json.Unmarshal(grantsJSON, &grants); err != nil {
			return nil, fmt.Errorf("decode persona grants: %w", err)
		}
		for _, grant := range grants {
			item.Grants = append(item.Grants, domain.ToolGrant{Capability: domain.Capability(grant.Capability), Conditions: grant.Conditions})
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ListConversations(ctx context.Context, boardroomID domain.BoardroomID, limit int) ([]Conversation, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.id::text, c.boardroom_id::text, c.title, c.source, c.status,
		       latest.id::text, latest.status, latest.prompt,
		       (SELECT count(*) FROM boardroom_messages m
		        JOIN boardroom_runs mr ON mr.id = m.run_id WHERE mr.conversation_id = c.id),
		       c.created_at, c.updated_at
		FROM conversations c
		JOIN LATERAL (
			SELECT r.id, r.status, r.prompt
			FROM boardroom_runs r WHERE r.conversation_id = c.id
			ORDER BY r.created_at DESC LIMIT 1
		) latest ON true
		WHERE c.boardroom_id = $1 AND c.status <> 'archived'
		ORDER BY c.updated_at DESC
		LIMIT $2
	`, boardroomID.String(), limit)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	var result []Conversation
	for rows.Next() {
		conversation, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, conversation)
	}
	return result, rows.Err()
}

func (s *Store) GetConversation(ctx context.Context, conversationID domain.ConversationID) (Conversation, error) {
	conversation, err := scanConversation(s.pool.QueryRow(ctx, `
		SELECT c.id::text, c.boardroom_id::text, c.title, c.source, c.status,
		       latest.id::text, latest.status, latest.prompt,
		       (SELECT count(*) FROM boardroom_messages m
		        JOIN boardroom_runs mr ON mr.id = m.run_id WHERE mr.conversation_id = c.id),
		       c.created_at, c.updated_at
		FROM conversations c
		JOIN LATERAL (
			SELECT r.id, r.status, r.prompt
			FROM boardroom_runs r WHERE r.conversation_id = c.id
			ORDER BY r.created_at DESC LIMIT 1
		) latest ON true
		WHERE c.id = $1
	`, conversationID.String()))
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrConversationNotFound) {
		return Conversation{}, ErrConversationNotFound
	}
	return conversation, err
}

func scanConversation(row rowScanner) (Conversation, error) {
	var item Conversation
	var id, boardroomID, latestRunID, latestStatus string
	if err := row.Scan(
		&id, &boardroomID, &item.Title, &item.Source, &item.Status,
		&latestRunID, &latestStatus, &item.LatestPrompt, &item.MessageCount,
		&item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Conversation{}, ErrConversationNotFound
		}
		return Conversation{}, fmt.Errorf("scan conversation: %w", err)
	}
	var err error
	item.ID, err = domain.ParseConversationID(id)
	if err != nil {
		return Conversation{}, err
	}
	item.BoardroomID, err = domain.ParseBoardroomID(boardroomID)
	if err != nil {
		return Conversation{}, err
	}
	item.LatestRunID, err = domain.ParseRunID(latestRunID)
	if err != nil {
		return Conversation{}, err
	}
	item.LatestStatus = domain.RunStatus(latestStatus)
	return item, nil
}

func (s *Store) CreateRun(ctx context.Context, boardroomID domain.BoardroomID, userID, prompt string) (Run, error) {
	conversationID := domain.NewConversationID()
	return s.createRun(ctx, boardroomID, conversationID, &userID, nil, conversationTitle(prompt), "user", nil, prompt, true)
}

func (s *Store) CreateFollowUpRun(ctx context.Context, conversationID domain.ConversationID, userID, prompt string) (Run, error) {
	conversation, err := s.GetConversation(ctx, conversationID)
	if err != nil {
		return Run{}, err
	}
	return s.createRun(ctx, conversation.BoardroomID, conversationID, &userID, nil, "", "", nil, prompt, false)
}

func (s *Store) CreateScheduledRun(ctx context.Context, boardroomID domain.BoardroomID, workflowID, title, prompt, scheduleID string) (Run, error) {
	conversationID := domain.NewConversationID()
	var scheduleIDValue *string
	if scheduleID != "" {
		scheduleIDValue = &scheduleID
	}
	return s.createRun(ctx, boardroomID, conversationID, nil, &workflowID, title, "schedule", scheduleIDValue, prompt, true)
}

func (s *Store) createRun(
	ctx context.Context,
	boardroomID domain.BoardroomID,
	conversationID domain.ConversationID,
	userID, workflowID *string,
	title, source string,
	scheduleID *string,
	prompt string,
	createConversation bool,
) (Run, error) {
	boardroom, err := s.Get(ctx, boardroomID)
	if err != nil {
		return Run{}, err
	}
	if boardroom.Status != "active" {
		return Run{}, errors.New("boardroom is not active")
	}

	runID := domain.NewRunID()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("begin run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if createConversation {
		if strings.TrimSpace(title) == "" {
			title = conversationTitle(prompt)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversations (id, boardroom_id, title, source, schedule_id, created_by)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, conversationID.String(), boardroomID.String(), title, source, scheduleID, userID); err != nil {
			return Run{}, fmt.Errorf("create conversation: %w", err)
		}
	} else {
		var active bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM boardroom_runs
				WHERE conversation_id = $1 AND status IN ('pending', 'preparing', 'running', 'awaiting_approval')
			)
		`, conversationID.String()).Scan(&active); err != nil {
			return Run{}, fmt.Errorf("check active conversation run: %w", err)
		}
		if active {
			return Run{}, ErrConversationBusy
		}
	}

	run := Run{ID: runID, BoardroomID: boardroomID, ConversationID: conversationID, Status: domain.RunPending, Prompt: prompt, MaxTurns: boardroom.MaxTurns}
	if err := tx.QueryRow(ctx, `
		INSERT INTO boardroom_runs (id, boardroom_id, conversation_id, workflow_id, status, prompt, created_by, configuration_snapshot)
		VALUES ($1, $2, $3, $4, 'pending', $5, $6, jsonb_build_object('max_turns', $7::integer))
		ON CONFLICT (workflow_id) DO NOTHING
		RETURNING created_at
	`, runID.String(), boardroomID.String(), conversationID.String(), workflowID, prompt, userID, boardroom.MaxTurns).Scan(&run.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) && workflowID != nil {
			_ = tx.Rollback(ctx)
			return s.GetRunByWorkflowID(ctx, *workflowID)
		}
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.ConstraintName == "boardroom_runs_one_active_per_conversation_idx" {
			return Run{}, ErrConversationBusy
		}
		return Run{}, fmt.Errorf("create run: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO boardroom_messages (run_id, role, body, sequence)
		VALUES ($1, 'user', $2, 1)
	`, runID.String(), prompt); err != nil {
		return Run{}, fmt.Errorf("create run prompt: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{"status": domain.RunPending, "prompt": prompt})
	if _, err := tx.Exec(ctx, `
		INSERT INTO boardroom_events (run_id, event_type, payload)
		VALUES ($1, 'run.created', $2)
	`, runID.String(), payload); err != nil {
		return Run{}, fmt.Errorf("create run event: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE conversations SET updated_at = now() WHERE id = $1`, conversationID.String()); err != nil {
		return Run{}, fmt.Errorf("update conversation activity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, fmt.Errorf("commit run: %w", err)
	}
	return run, nil
}

func conversationTitle(prompt string) string {
	value := strings.Join(strings.Fields(prompt), " ")
	runes := []rune(value)
	if len(runes) > 80 {
		return string(runes[:77]) + "..."
	}
	if value == "" {
		return "New conversation"
	}
	return value
}

func (s *Store) GetRunByWorkflowID(ctx context.Context, workflowID string) (Run, error) {
	var run Run
	var id, boardroomID, conversationID, status string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, boardroom_id::text, conversation_id::text, status, prompt, turn_count,
		       COALESCE(error, ''), created_at, started_at, completed_at,
		       COALESCE((configuration_snapshot->>'max_turns')::integer, 6)
		FROM boardroom_runs WHERE workflow_id = $1
	`, workflowID).Scan(&id, &boardroomID, &conversationID, &status, &run.Prompt, &run.TurnCount, &run.Error,
		&run.CreatedAt, &run.StartedAt, &run.CompletedAt, &run.MaxTurns)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("get run by workflow ID: %w", err)
	}
	run.ID, err = domain.ParseRunID(id)
	if err != nil {
		return Run{}, err
	}
	run.BoardroomID, err = domain.ParseBoardroomID(boardroomID)
	if err != nil {
		return Run{}, err
	}
	run.ConversationID, err = domain.ParseConversationID(conversationID)
	if err != nil {
		return Run{}, err
	}
	run.Status = domain.RunStatus(status)
	return run, nil
}

func (s *Store) GetRun(ctx context.Context, runID domain.RunID) (Run, error) {
	var run Run
	var id, boardroomID, conversationID string
	err := s.pool.QueryRow(ctx, `
		SELECT r.id::text, r.boardroom_id::text, r.conversation_id::text, r.status, r.prompt, r.turn_count,
		       COALESCE((r.configuration_snapshot->>'max_turns')::integer, b.max_turns),
		       COALESCE(r.error, ''), r.created_at, r.started_at, r.completed_at
		FROM boardroom_runs r
		JOIN boardrooms b ON b.id = r.boardroom_id
		WHERE r.id = $1
	`, runID.String()).Scan(
		&id, &boardroomID, &conversationID, &run.Status, &run.Prompt, &run.TurnCount, &run.MaxTurns,
		&run.Error, &run.CreatedAt, &run.StartedAt, &run.CompletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("get run: %w", err)
	}
	run.ID, err = domain.ParseRunID(id)
	if err != nil {
		return Run{}, err
	}
	run.BoardroomID, err = domain.ParseBoardroomID(boardroomID)
	if err != nil {
		return Run{}, err
	}
	run.ConversationID, err = domain.ParseConversationID(conversationID)
	return run, err
}

func (s *Store) Messages(ctx context.Context, runID domain.RunID) ([]Message, error) {
	return queryMessages(ctx, s.pool, runID)
}

func (s *Store) ConversationMessages(ctx context.Context, conversationID domain.ConversationID) ([]Message, error) {
	return queryConversationMessages(ctx, s.pool, conversationID)
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type rowScanner interface {
	Scan(...any) error
}

func queryMessages(ctx context.Context, source queryer, runID domain.RunID) ([]Message, error) {
	rows, err := source.Query(ctx, `
		SELECT m.id::text, m.run_id::text, m.persona_id::text, COALESCE(p.name, ''), COALESCE(p.role, ''),
		       m.role, m.body, m.sequence, m.created_at
		FROM boardroom_messages m
		LEFT JOIN personas p ON p.id = m.persona_id
		WHERE m.run_id = $1
		ORDER BY m.sequence
	`, runID.String())
	if err != nil {
		return nil, fmt.Errorf("list run messages: %w", err)
	}
	defer rows.Close()

	var result []Message
	for rows.Next() {
		var item Message
		var runIDText string
		var personaID *string
		if err := rows.Scan(&item.ID, &runIDText, &personaID, &item.PersonaName, &item.PersonaRole, &item.Role, &item.Body, &item.Sequence, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		item.RunID, err = domain.ParseRunID(runIDText)
		if err != nil {
			return nil, err
		}
		if personaID != nil {
			parsed, parseErr := domain.ParsePersonaID(*personaID)
			if parseErr != nil {
				return nil, parseErr
			}
			item.PersonaID = &parsed
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func queryConversationMessages(ctx context.Context, source queryer, conversationID domain.ConversationID) ([]Message, error) {
	rows, err := source.Query(ctx, `
		SELECT m.id::text, m.run_id::text, m.persona_id::text, COALESCE(p.name, ''), COALESCE(p.role, ''),
		       m.role, m.body, m.sequence, m.created_at
		FROM boardroom_messages m
		JOIN boardroom_runs r ON r.id = m.run_id
		LEFT JOIN personas p ON p.id = m.persona_id
		WHERE r.conversation_id = $1
		ORDER BY r.created_at, m.sequence
	`, conversationID.String())
	if err != nil {
		return nil, fmt.Errorf("list conversation messages: %w", err)
	}
	defer rows.Close()
	var result []Message
	for rows.Next() {
		var item Message
		var runIDText string
		var personaID *string
		if err := rows.Scan(&item.ID, &runIDText, &personaID, &item.PersonaName, &item.PersonaRole, &item.Role, &item.Body, &item.Sequence, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan conversation message: %w", err)
		}
		item.RunID, err = domain.ParseRunID(runIDText)
		if err != nil {
			return nil, err
		}
		if personaID != nil {
			parsed, parseErr := domain.ParsePersonaID(*personaID)
			if parseErr != nil {
				return nil, parseErr
			}
			item.PersonaID = &parsed
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) MessagesSnapshot(ctx context.Context, conversationID domain.ConversationID, runID domain.RunID) ([]Message, int64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, fmt.Errorf("begin message snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	messages, err := queryConversationMessages(ctx, tx, conversationID)
	if err != nil {
		return nil, 0, err
	}
	var cursor int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(id), 0) FROM boardroom_events WHERE run_id = $1`, runID.String()).Scan(&cursor); err != nil {
		return nil, 0, fmt.Errorf("load event cursor: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, fmt.Errorf("commit message snapshot: %w", err)
	}
	return messages, cursor, nil
}

func (s *Store) SetRunStatus(ctx context.Context, runID domain.RunID, status domain.RunStatus, runError string) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE boardroom_runs
		SET status = $2,
		    error = NULLIF($3, ''),
		    started_at = CASE WHEN $2 = 'running' THEN COALESCE(started_at, now()) ELSE started_at END,
		    completed_at = CASE WHEN $2 IN ('completed', 'failed', 'canceled') THEN now() ELSE completed_at END
		WHERE id = $1
	`, runID.String(), status, runError)
	if err != nil {
		return fmt.Errorf("update run status: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrRunNotFound
	}
	payload, _ := json.Marshal(map[string]any{"status": status, "error": runError})
	if _, err = s.pool.Exec(ctx, `INSERT INTO boardroom_events (run_id, event_type, payload) VALUES ($1, 'run.status', $2)`, runID.String(), payload); err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE conversations SET updated_at = now()
		WHERE id = (SELECT conversation_id FROM boardroom_runs WHERE id = $1)
	`, runID.String())
	return err
}

func (s *Store) AppendAgentMessage(ctx context.Context, runID domain.RunID, persona Persona, result agent.Result) (Message, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Message{}, fmt.Errorf("begin agent message: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT 1 FROM boardroom_runs WHERE id = $1 FOR UPDATE`, runID.String()); err != nil {
		return Message{}, fmt.Errorf("lock run: %w", err)
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(sequence), 0) + 1 FROM boardroom_messages WHERE run_id = $1`, runID.String()).Scan(&sequence); err != nil {
		return Message{}, fmt.Errorf("select message sequence: %w", err)
	}
	metadata, err := json.Marshal(result)
	if err != nil {
		return Message{}, fmt.Errorf("encode provider metadata: %w", err)
	}
	message := Message{RunID: runID, PersonaID: &persona.ID, PersonaName: persona.Name, PersonaRole: persona.Role, Role: domain.MessageAgent, Body: result.Body, Sequence: sequence}
	if err := tx.QueryRow(ctx, `
		INSERT INTO boardroom_messages (run_id, persona_id, role, body, sequence, provider_metadata)
		VALUES ($1, $2, 'agent', $3, $4, $5)
		RETURNING id::text, created_at
	`, runID.String(), persona.ID.String(), result.Body, sequence, metadata).Scan(&message.ID, &message.CreatedAt); err != nil {
		return Message{}, fmt.Errorf("insert agent message: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE boardroom_runs SET turn_count = turn_count + 1 WHERE id = $1`, runID.String()); err != nil {
		return Message{}, fmt.Errorf("increment turn count: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE conversations SET updated_at = now()
		WHERE id = (SELECT conversation_id FROM boardroom_runs WHERE id = $1)
	`, runID.String()); err != nil {
		return Message{}, fmt.Errorf("update conversation activity: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"message_id": message.ID, "sequence": message.Sequence, "persona_name": message.PersonaName,
		"persona_role": message.PersonaRole, "role": message.Role, "body": message.Body, "created_at": message.CreatedAt,
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO boardroom_events (run_id, event_type, payload)
		VALUES ($1, 'message.completed', $2)
	`, runID.String(), payload); err != nil {
		return Message{}, fmt.Errorf("insert message event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Message{}, fmt.Errorf("commit agent message: %w", err)
	}
	return message, nil
}

func (s *Store) EventsAfter(ctx context.Context, runID domain.RunID, afterID int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id::text, event_type, payload, created_at
		FROM boardroom_events
		WHERE run_id = $1 AND id > $2
		ORDER BY id
		LIMIT $3
	`, runID.String(), afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list boardroom events: %w", err)
	}
	defer rows.Close()

	var result []Event
	for rows.Next() {
		var event Event
		var runIDText string
		if err := rows.Scan(&event.ID, &runIDText, &event.Type, &event.Payload, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan boardroom event: %w", err)
		}
		event.RunID, err = domain.ParseRunID(runIDText)
		if err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
