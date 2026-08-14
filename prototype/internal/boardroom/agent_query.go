package boardroom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

func (s *Store) ListAgents(ctx context.Context) ([]Persona, error) {
	rows, err := s.pool.Query(ctx, agentSelect+` ORDER BY b.name, p.position`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	var result []Persona
	for rows.Next() {
		item, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) GetAgent(ctx context.Context, id domain.PersonaID) (Persona, error) {
	item, err := scanAgent(s.pool.QueryRow(ctx, agentSelect+` WHERE p.id=$1`, id.String()))
	if errors.Is(err, pgx.ErrNoRows) {
		return Persona{}, ErrPersonaNotFound
	}
	return item, err
}

const agentSelect = `
	SELECT p.id::text,p.boardroom_id::text,p.name,p.role,p.description,p.system_instructions,p.position,p.enabled,
	       p.provider,p.model,p.reasoning_effort,p.temperature,p.top_p,p.context_token_limit,p.max_output_tokens,
	       p.timeout_seconds,p.max_tool_calls,p.max_cost_micros,p.response_style,p.citation_policy,p.action_policy,
	       p.created_at,p.updated_at,
	       COALESCE((SELECT jsonb_agg(jsonb_build_object('capability',g.capability,'conditions',g.conditions) ORDER BY g.capability)
	                 FROM persona_tool_grants g WHERE g.persona_id=p.id),'[]'::jsonb)
	FROM personas p JOIN boardrooms b ON b.id=p.boardroom_id`

type scanner interface{ Scan(...any) error }

func scanAgent(row scanner) (Persona, error) {
	var item Persona
	var id, boardroomID string
	var grants []byte
	err := row.Scan(&id, &boardroomID, &item.Name, &item.Role, &item.Description, &item.SystemInstructions, &item.Position, &item.Enabled,
		&item.Settings.Provider, &item.Settings.Model, &item.Settings.ReasoningEffort, &item.Settings.Temperature, &item.Settings.TopP,
		&item.Settings.ContextTokenLimit, &item.Settings.MaxOutputTokens, &item.Settings.TimeoutSeconds, &item.Settings.MaxToolCalls,
		&item.Settings.MaxCostMicros, &item.Settings.ResponseStyle, &item.Settings.CitationPolicy, &item.Settings.ActionPolicy,
		&item.CreatedAt, &item.UpdatedAt, &grants)
	if err != nil {
		return Persona{}, err
	}
	item.ID, err = domain.ParsePersonaID(id)
	if err != nil {
		return Persona{}, err
	}
	item.BoardroomID, err = domain.ParseBoardroomID(boardroomID)
	if err != nil {
		return Persona{}, err
	}
	if err := json.Unmarshal(grants, &item.Grants); err != nil {
		return Persona{}, err
	}
	return item, nil
}
