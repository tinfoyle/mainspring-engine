package boardroom

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

func (s *Store) CreateAgent(ctx context.Context, input AgentInput) (Persona, error) {
	if err := input.NormalizeAndValidate(); err != nil {
		return Persona{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Persona{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	desiredPosition := input.Position
	if _, err := tx.Exec(ctx, `SELECT 1 FROM boardrooms WHERE id=$1 FOR UPDATE`, input.BoardroomID.String()); err != nil {
		return Persona{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(position),0)+1 FROM personas WHERE boardroom_id=$1`, input.BoardroomID.String()).Scan(&input.Position); err != nil {
		return Persona{}, err
	}
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO personas (boardroom_id,name,role,description,system_instructions,position,enabled,provider,model,reasoning_effort,
			temperature,top_p,context_token_limit,max_output_tokens,timeout_seconds,max_tool_calls,max_cost_micros,response_style,citation_policy,action_policy)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20) RETURNING id::text
	`, input.BoardroomID.String(), input.Name, input.Role, input.Description, input.SystemInstructions, input.Position, input.Enabled,
		input.Settings.Provider, input.Settings.Model, input.Settings.ReasoningEffort, input.Settings.Temperature, input.Settings.TopP,
		input.Settings.ContextTokenLimit, input.Settings.MaxOutputTokens, input.Settings.TimeoutSeconds, input.Settings.MaxToolCalls,
		input.Settings.MaxCostMicros, input.Settings.ResponseStyle, input.Settings.CitationPolicy, input.Settings.ActionPolicy).Scan(&id); err != nil {
		return Persona{}, fmt.Errorf("create agent: %w", err)
	}
	if err := replaceAgentGrants(ctx, tx, id, input.Grants); err != nil {
		return Persona{}, err
	}
	parsed, _ := domain.ParsePersonaID(id)
	if err := reorderAgent(ctx, tx, input.BoardroomID, parsed, desiredPosition); err != nil {
		return Persona{}, err
	}
	if err := syncBoardroomTurnLimit(ctx, tx, input.BoardroomID); err != nil {
		return Persona{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Persona{}, err
	}
	return s.GetAgent(ctx, parsed)
}

func (s *Store) UpdateAgent(ctx context.Context, id domain.PersonaID, input AgentInput) (Persona, error) {
	if err := input.NormalizeAndValidate(); err != nil {
		return Persona{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Persona{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var boardroomID string
	if err := tx.QueryRow(ctx, `SELECT boardroom_id::text FROM personas WHERE id=$1 FOR UPDATE`, id.String()).Scan(&boardroomID); errors.Is(err, pgx.ErrNoRows) {
		return Persona{}, ErrPersonaNotFound
	} else if err != nil {
		return Persona{}, err
	}
	if boardroomID != input.BoardroomID.String() {
		return Persona{}, errors.New("an existing agent cannot be moved to another boardroom; duplicate it there instead")
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM boardrooms WHERE id=$1 FOR UPDATE`, input.BoardroomID.String()); err != nil {
		return Persona{}, err
	}
	if err := reorderAgent(ctx, tx, input.BoardroomID, id, input.Position); err != nil {
		return Persona{}, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE personas SET name=$2,role=$3,description=$4,system_instructions=$5,enabled=$6,provider=$7,model=$8,
			reasoning_effort=$9,temperature=$10,top_p=$11,context_token_limit=$12,max_output_tokens=$13,timeout_seconds=$14,
			max_tool_calls=$15,max_cost_micros=$16,response_style=$17,citation_policy=$18,action_policy=$19,updated_at=now()
		WHERE id=$1
	`, id.String(), input.Name, input.Role, input.Description, input.SystemInstructions, input.Enabled,
		input.Settings.Provider, input.Settings.Model, input.Settings.ReasoningEffort, input.Settings.Temperature, input.Settings.TopP,
		input.Settings.ContextTokenLimit, input.Settings.MaxOutputTokens, input.Settings.TimeoutSeconds, input.Settings.MaxToolCalls,
		input.Settings.MaxCostMicros, input.Settings.ResponseStyle, input.Settings.CitationPolicy, input.Settings.ActionPolicy)
	if err != nil || command.RowsAffected() != 1 {
		return Persona{}, fmt.Errorf("update agent: %w", err)
	}
	if err := replaceAgentGrants(ctx, tx, id.String(), input.Grants); err != nil {
		return Persona{}, err
	}
	if err := syncBoardroomTurnLimit(ctx, tx, input.BoardroomID); err != nil {
		return Persona{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Persona{}, err
	}
	return s.GetAgent(ctx, id)
}
