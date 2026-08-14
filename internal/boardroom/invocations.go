package boardroom

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tinfoyle/mainspring-engine/internal/agent"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

var ErrInvalidDelegation = errors.New("invalid manager delegation")

type PlannedPersona struct {
	TurnNumber         int
	PersonaID          domain.PersonaID
	PersonaVersionID   string
	Name               string
	Role               string
	Description        string
	SystemInstructions string
	Grants             []domain.ToolGrant
	OutputSchema       json.RawMessage
	Settings           AgentSettings
}

type RunPlan struct {
	RunID    domain.RunID
	Personas []PlannedPersona
}

type InvocationRecord struct {
	ID            domain.InvocationID
	RunID         domain.RunID
	TurnNumber    int
	Status        string
	AttemptCount  int
	ErrorCategory string
	ErrorDetail   string
	StartedAt     *time.Time
	CompletedAt   *time.Time
}

func (s *Store) PrepareRun(ctx context.Context, runID domain.RunID) (RunPlan, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RunPlan{}, fmt.Errorf("begin run preparation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var boardroomIDText string
	var maxTurns int
	var status domain.RunStatus
	var selectedPayload []byte
	if err := tx.QueryRow(ctx, `
		SELECT boardroom_id::text, COALESCE((configuration_snapshot->>'max_turns')::integer, 1), status,
		       COALESCE(configuration_snapshot->'selected_persona_ids', '[]'::jsonb)
		FROM boardroom_runs WHERE id=$1 FOR UPDATE
	`, runID.String()).Scan(&boardroomIDText, &maxTurns, &status, &selectedPayload); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RunPlan{}, ErrRunNotFound
		}
		return RunPlan{}, fmt.Errorf("lock run for preparation: %w", err)
	}
	if status == domain.RunCompleted || status == domain.RunCanceled {
		return RunPlan{RunID: runID}, nil
	}

	existing, err := queryRunPlan(ctx, tx, runID)
	if err != nil {
		return RunPlan{}, err
	}
	if len(existing.Personas) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE boardroom_runs SET status='running', started_at=COALESCE(started_at, now()), error=NULL WHERE id=$1`, runID.String()); err != nil {
			return RunPlan{}, fmt.Errorf("resume prepared run: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return RunPlan{}, fmt.Errorf("commit prepared run: %w", err)
		}
		return existing, nil
	}

	boardroomID, err := domain.ParseBoardroomID(boardroomIDText)
	if err != nil {
		return RunPlan{}, err
	}
	personas, err := queryPersonas(ctx, tx, boardroomID)
	if err != nil {
		return RunPlan{}, err
	}
	if len(personas) == 0 {
		return RunPlan{}, errors.New("boardroom has no enabled personas")
	}
	orchestration := "manager_led"
	var selectedIDs []string
	if err := json.Unmarshal(selectedPayload, &selectedIDs); err != nil {
		return RunPlan{}, fmt.Errorf("decode selected agents: %w", err)
	}
	if len(selectedIDs) > 0 {
		if len(selectedIDs) > maxTurns {
			return RunPlan{}, fmt.Errorf("selected %d agents but boardroom allows %d turns", len(selectedIDs), maxTurns)
		}
		available := make(map[string]Persona, len(personas))
		for _, persona := range personas {
			available[persona.ID.String()] = persona
		}
		selected := make([]Persona, 0, len(selectedIDs))
		seen := make(map[string]bool, len(selectedIDs))
		for _, id := range selectedIDs {
			if seen[id] {
				continue
			}
			persona, ok := available[id]
			if !ok {
				return RunPlan{}, fmt.Errorf("selected agent %q is not enabled in this boardroom", id)
			}
			seen[id] = true
			persona.Grants = ensureTicketWorkspaceGrant(persona.Grants)
			selected = append(selected, persona)
		}
		personas = selected
		orchestration = "selected_agents"
	} else {
		coordinator := personas[managerPersonaIndex(personas)]
		// A manager-led run starts with only the coordinator. It may append the
		// specialists it explicitly delegates to, followed by its synthesis turn.
		personas = []Persona{coordinator}
	}

	outputSchema := agent.DefaultOutputSchema()
	for index, persona := range personas {
		versionID, err := ensurePersonaVersion(ctx, tx, persona, outputSchema)
		if err != nil {
			return RunPlan{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO boardroom_run_personas (run_id, turn_number, persona_id, persona_version_id)
			VALUES ($1, $2, $3, $4)
		`, runID.String(), index+1, persona.ID.String(), versionID); err != nil {
			return RunPlan{}, fmt.Errorf("snapshot run persona: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE boardroom_runs
		SET status='running', started_at=COALESCE(started_at, now()), error=NULL,
		    configuration_snapshot = configuration_snapshot || jsonb_build_object('agent_contract_version', 2, 'persona_count', $2::integer, 'orchestration', $3::text)
		WHERE id=$1
	`, runID.String(), len(personas), orchestration); err != nil {
		return RunPlan{}, fmt.Errorf("mark run prepared: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RunPlan{}, fmt.Errorf("commit run preparation: %w", err)
	}
	return s.GetRunPlan(ctx, runID)
}

func ensureTicketWorkspaceGrant(grants []domain.ToolGrant) []domain.ToolGrant {
	for _, grant := range grants {
		if grant.Capability == domain.CapabilityTicketCreate {
			return grants
		}
	}
	return append(grants, domain.ToolGrant{Capability: domain.CapabilityTicketCreate, Conditions: map[string]string{"scope": "subtask_only"}})
}

// ScheduleDelegations expands a manager-led run after its initial contribution.
// The request is persisted with that invocation, then every selected specialist
// and a final manager synthesis are snapshotted before they are executed.
func (s *Store) ScheduleDelegations(ctx context.Context, runID domain.RunID) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin delegation scheduling: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var boardroomIDText, orchestration string
	var maxTurns int
	if err := tx.QueryRow(ctx, `
		SELECT boardroom_id::text, COALESCE((configuration_snapshot->>'max_turns')::integer, 1),
		       COALESCE(configuration_snapshot->>'orchestration', 'manager_led')
		FROM boardroom_runs WHERE id=$1 FOR UPDATE
	`, runID.String()).Scan(&boardroomIDText, &maxTurns, &orchestration); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrRunNotFound
		}
		return 0, fmt.Errorf("lock run for delegation scheduling: %w", err)
	}
	plan, err := queryRunPlan(ctx, tx, runID)
	if err != nil {
		return 0, err
	}
	if orchestration == "selected_agents" {
		return len(plan.Personas), tx.Commit(ctx)
	}
	if len(plan.Personas) != 1 {
		return len(plan.Personas), tx.Commit(ctx)
	}

	var payload []byte
	if err := tx.QueryRow(ctx, `SELECT result_payload FROM agent_invocations WHERE run_id=$1 AND turn_number=1`, runID.String()).Scan(&payload); err != nil {
		return 0, fmt.Errorf("load manager delegation result: %w", err)
	}
	var result agent.Result
	if err := json.Unmarshal(payload, &result); err != nil {
		return 0, fmt.Errorf("decode manager delegation result: %w", err)
	}
	requests, err := delegationRequests(result.Structured.Delegations)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidDelegation, err)
	}
	if len(requests) == 0 {
		return 1, tx.Commit(ctx)
	}
	if maxTurns < 3 {
		return 0, fmt.Errorf("%w: boardroom allows %d turns; manager delegation needs at least 3", ErrInvalidDelegation, maxTurns)
	}

	boardroomID, err := domain.ParseBoardroomID(boardroomIDText)
	if err != nil {
		return 0, err
	}
	personas, err := queryPersonas(ctx, tx, boardroomID)
	if err != nil {
		return 0, err
	}
	maximumDelegates := maxTurns - 2
	if len(requests) > maximumDelegates {
		return 0, fmt.Errorf("%w: manager delegated to %d agents but this boardroom allows %d", ErrInvalidDelegation, len(requests), maximumDelegates)
	}

	selected := make([]Persona, 0, len(requests))
	seen := make(map[domain.PersonaID]bool)
	for _, request := range requests {
		persona, found := findDelegatedPersona(personas, request.Agent)
		if !found {
			return 0, fmt.Errorf("%w: manager delegated to unknown agent %q", ErrInvalidDelegation, request.Agent)
		}
		if isManagerPersona(persona.Name, persona.Role) {
			return 0, fmt.Errorf("%w: manager cannot delegate to itself", ErrInvalidDelegation)
		}
		if seen[persona.ID] {
			return 0, fmt.Errorf("%w: manager delegated to %q more than once", ErrInvalidDelegation, request.Agent)
		}
		seen[persona.ID] = true
		selected = append(selected, persona)
	}

	outputSchema := agent.DefaultOutputSchema()
	turnNumber := 2
	for _, persona := range selected {
		versionID, err := ensurePersonaVersion(ctx, tx, persona, outputSchema)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO boardroom_run_personas (run_id, turn_number, persona_id, persona_version_id) VALUES ($1,$2,$3,$4)`, runID.String(), turnNumber, persona.ID.String(), versionID); err != nil {
			return 0, fmt.Errorf("snapshot delegated persona: %w", err)
		}
		turnNumber++
	}
	coordinator := plan.Personas[0]
	if _, err := tx.Exec(ctx, `INSERT INTO boardroom_run_personas (run_id, turn_number, persona_id, persona_version_id) VALUES ($1,$2,$3,$4)`, runID.String(), turnNumber, coordinator.PersonaID.String(), coordinator.PersonaVersionID); err != nil {
		return 0, fmt.Errorf("snapshot manager synthesis: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE boardroom_runs SET configuration_snapshot = configuration_snapshot || jsonb_build_object('delegation_count', $2::integer) WHERE id=$1`, runID.String(), len(selected)); err != nil {
		return 0, fmt.Errorf("record delegation plan: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit delegation plan: %w", err)
	}
	return turnNumber, nil
}

func delegationRequests(requests []agent.Delegation) ([]agent.Delegation, error) {
	delegations := make([]agent.Delegation, 0, len(requests))
	for _, delegation := range requests {
		delegation.Agent = strings.TrimSpace(delegation.Agent)
		delegation.Request = strings.TrimSpace(delegation.Request)
		if delegation.Agent == "" || delegation.Request == "" {
			return nil, errors.New("manager delegation requires agent and request")
		}
		delegations = append(delegations, delegation)
	}
	return delegations, nil
}

func visibleDelegationSummary(requests []agent.Delegation) (string, error) {
	delegations, err := delegationRequests(requests)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(delegations))
	for _, delegation := range delegations {
		parts = append(parts, fmt.Sprintf("Delegating to %s: %s", delegation.Agent, delegation.Request))
	}
	return strings.Join(parts, "\n"), nil
}

func managerPersonaIndex(personas []Persona) int {
	for index, persona := range personas {
		if isManagerPersona(persona.Name, persona.Role) {
			return index
		}
	}
	return 0
}

func isManagerPersona(name, role string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "Main Manager") || strings.EqualFold(strings.TrimSpace(role), "Main Manager")
}

func findDelegatedPersona(personas []Persona, target string) (Persona, bool) {
	target = strings.TrimSpace(target)
	for _, persona := range personas {
		name := strings.TrimSpace(persona.Name)
		role := strings.TrimSpace(persona.Role)
		if strings.EqualFold(name, target) || strings.EqualFold(role, target) ||
			strings.EqualFold(name+" — "+role, target) || strings.EqualFold(name+" - "+role, target) ||
			strings.EqualFold(name+" ("+role+")", target) {
			return persona, true
		}
	}
	return Persona{}, false
}

func ensurePersonaVersion(ctx context.Context, tx pgx.Tx, persona Persona, outputSchema json.RawMessage) (string, error) {
	// Multiple autonomous tickets may snapshot the same persona concurrently.
	// Serialize version allocation per persona so max(version)+1 remains safe.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('persona-version:' || $1))`, persona.ID.String()); err != nil {
		return "", fmt.Errorf("lock persona version: %w", err)
	}
	grants, err := json.Marshal(persona.Grants)
	if err != nil {
		return "", fmt.Errorf("encode persona grants: %w", err)
	}
	content, err := json.Marshal(struct {
		Name         string          `json:"name"`
		Role         string          `json:"role"`
		Description  string          `json:"description"`
		Instructions string          `json:"instructions"`
		Grants       json.RawMessage `json:"grants"`
		Schema       json.RawMessage `json:"schema"`
		Settings     AgentSettings   `json:"settings"`
	}{persona.Name, persona.Role, persona.Description, persona.SystemInstructions, grants, outputSchema, persona.Settings})
	if err != nil {
		return "", fmt.Errorf("encode persona version: %w", err)
	}
	hash := sha256.Sum256(content)
	var id string
	err = tx.QueryRow(ctx, `SELECT id::text FROM persona_versions WHERE persona_id=$1 AND content_hash=$2`, persona.ID.String(), hash[:]).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("find persona version: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO persona_versions (persona_id, version, content_hash, name, role, description, system_instructions, tool_grants, output_schema, runtime_config)
		VALUES ($1, COALESCE((SELECT max(version)+1 FROM persona_versions WHERE persona_id=$1), 1), $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id::text
	`, persona.ID.String(), hash[:], persona.Name, persona.Role, persona.Description, persona.SystemInstructions, grants, outputSchema, persona.Settings).Scan(&id); err != nil {
		return "", fmt.Errorf("create persona version: %w", err)
	}
	return id, nil
}

func (s *Store) GetRunPlan(ctx context.Context, runID domain.RunID) (RunPlan, error) {
	return queryRunPlan(ctx, s.pool, runID)
}

func queryRunPlan(ctx context.Context, source queryer, runID domain.RunID) (RunPlan, error) {
	rows, err := source.Query(ctx, `
		SELECT rp.turn_number, rp.persona_id::text, rp.persona_version_id::text,
		       pv.name, pv.role, pv.description, pv.system_instructions, pv.tool_grants, pv.output_schema, pv.runtime_config
		FROM boardroom_run_personas rp
		JOIN persona_versions pv ON pv.id=rp.persona_version_id
		WHERE rp.run_id=$1 ORDER BY rp.turn_number
	`, runID.String())
	if err != nil {
		return RunPlan{}, fmt.Errorf("load run plan: %w", err)
	}
	defer rows.Close()
	plan := RunPlan{RunID: runID}
	for rows.Next() {
		var item PlannedPersona
		var personaIDText string
		var grants []byte
		var runtimeConfig []byte
		if err := rows.Scan(&item.TurnNumber, &personaIDText, &item.PersonaVersionID, &item.Name, &item.Role, &item.Description, &item.SystemInstructions, &grants, &item.OutputSchema, &runtimeConfig); err != nil {
			return RunPlan{}, fmt.Errorf("scan run plan: %w", err)
		}
		item.PersonaID, err = domain.ParsePersonaID(personaIDText)
		if err != nil {
			return RunPlan{}, err
		}
		if err := json.Unmarshal(grants, &item.Grants); err != nil {
			return RunPlan{}, fmt.Errorf("decode snapshotted grants: %w", err)
		}
		item.Settings = DefaultAgentSettings()
		if len(runtimeConfig) > 2 {
			if err := json.Unmarshal(runtimeConfig, &item.Settings); err != nil {
				return RunPlan{}, fmt.Errorf("decode snapshotted runtime config: %w", err)
			}
		}
		plan.Personas = append(plan.Personas, item)
	}
	return plan, rows.Err()
}

func (s *Store) StartInvocation(ctx context.Context, runID domain.RunID, turnNumber int, provider string, contextManifest any) (InvocationRecord, PlannedPersona, error) {
	plan, err := s.GetRunPlan(ctx, runID)
	if err != nil {
		return InvocationRecord{}, PlannedPersona{}, err
	}
	if turnNumber < 1 || turnNumber > len(plan.Personas) {
		return InvocationRecord{}, PlannedPersona{}, fmt.Errorf("turn %d is outside the run plan", turnNumber)
	}
	persona := plan.Personas[turnNumber-1]
	manifest, err := json.Marshal(contextManifest)
	if err != nil {
		return InvocationRecord{}, PlannedPersona{}, fmt.Errorf("encode context manifest: %w", err)
	}
	id := domain.NewInvocationID()
	var record InvocationRecord
	var idText, runIDText string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO agent_invocations (id, run_id, turn_number, persona_version_id, status, provider, context_manifest, output_schema, attempt_count, started_at)
		VALUES ($1, $2, $3, $4, 'running', $5, $6, $7, 1, now())
		ON CONFLICT (run_id, turn_number) DO UPDATE SET
			status=CASE WHEN agent_invocations.status='succeeded' THEN 'succeeded' ELSE 'running' END,
			attempt_count=CASE WHEN agent_invocations.status='succeeded' THEN agent_invocations.attempt_count ELSE agent_invocations.attempt_count+1 END,
			error_category=CASE WHEN agent_invocations.status='succeeded' THEN agent_invocations.error_category ELSE NULL END,
			error_detail=CASE WHEN agent_invocations.status='succeeded' THEN agent_invocations.error_detail ELSE NULL END,
			updated_at=now()
		RETURNING id::text, run_id::text, turn_number, status, attempt_count, COALESCE(error_category,''), COALESCE(error_detail,''), started_at, completed_at
	`, id.String(), runID.String(), turnNumber, persona.PersonaVersionID, provider, manifest, persona.OutputSchema).Scan(
		&idText, &runIDText, &record.TurnNumber, &record.Status, &record.AttemptCount, &record.ErrorCategory, &record.ErrorDetail, &record.StartedAt, &record.CompletedAt,
	)
	if err != nil {
		return InvocationRecord{}, PlannedPersona{}, fmt.Errorf("start invocation: %w", err)
	}
	record.ID, err = domain.ParseInvocationID(idText)
	if err != nil {
		return InvocationRecord{}, PlannedPersona{}, err
	}
	record.RunID, err = domain.ParseRunID(runIDText)
	if err != nil {
		return InvocationRecord{}, PlannedPersona{}, err
	}
	if record.Status != "succeeded" {
		payload, _ := json.Marshal(map[string]any{"attempt": record.AttemptCount, "provider": provider})
		if err := s.appendInvocationEvent(ctx, record.ID, "invocation.started", payload); err != nil {
			return InvocationRecord{}, PlannedPersona{}, err
		}
	}
	return record, persona, nil
}

func (s *Store) FailInvocation(ctx context.Context, invocationID domain.InvocationID, category, detail string) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE agent_invocations SET status='failed', error_category=$2, error_detail=$3, completed_at=now(), updated_at=now()
		WHERE id=$1 AND status <> 'succeeded'
	`, invocationID.String(), category, detail); err != nil {
		return fmt.Errorf("fail invocation: %w", err)
	}
	payload, _ := json.Marshal(map[string]string{"category": category, "detail": detail})
	return s.appendInvocationEvent(ctx, invocationID, "invocation.failed", payload)
}

func (s *Store) appendInvocationEvent(ctx context.Context, invocationID domain.InvocationID, eventType string, payload json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_invocation_events (invocation_id, event_sequence, event_type, payload)
		VALUES ($1, (SELECT COALESCE(max(event_sequence),0)+1 FROM agent_invocation_events WHERE invocation_id=$1), $2, $3)
	`, invocationID.String(), eventType, payload)
	if err != nil {
		return fmt.Errorf("append invocation event: %w", err)
	}
	return nil
}

func (s *Store) RecordInvocationEvent(ctx context.Context, invocationID domain.InvocationID, eventType string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode invocation event: %w", err)
	}
	return s.appendInvocationEvent(ctx, invocationID, eventType, encoded)
}

// RunToolResults returns only results recorded after successful broker calls.
// Later turns may cite this evidence because it remains scoped to the same
// durable run and retains its authorized invocation provenance.
func (s *Store) RunToolResults(ctx context.Context, runID domain.RunID) ([]json.RawMessage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT event.payload->'result'
		FROM agent_invocation_events event
		JOIN agent_invocations invocation ON invocation.id=event.invocation_id
		WHERE invocation.run_id=$1
		  AND event.event_type='tool.completed'
		  AND event.payload ? 'result'
		ORDER BY invocation.turn_number, event.event_sequence
	`, runID.String())
	if err != nil {
		return nil, fmt.Errorf("load authorized run tool results: %w", err)
	}
	defer rows.Close()
	var results []json.RawMessage
	for rows.Next() {
		var result json.RawMessage
		if err := rows.Scan(&result); err != nil {
			return nil, fmt.Errorf("scan authorized run tool result: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authorized run tool results: %w", err)
	}
	return results, nil
}

// UnknownAnswerSearchQuery returns the focused document query completed by the
// initial manager turn. Final synthesis uses it to preserve the evidence trail
// without making the specialist or manager repeat the same search.
func (s *Store) UnknownAnswerSearchQuery(ctx context.Context, runID domain.RunID) (string, error) {
	var query string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(request.payload->'arguments'->>'query', '')
		FROM agent_invocations ai
		JOIN LATERAL (
			SELECT event.payload
			FROM agent_invocation_events event
			WHERE event.invocation_id=ai.id
			  AND event.event_type='tool.requested'
			  AND event.payload->>'name'='documents.search'
			ORDER BY event.event_sequence DESC
			LIMIT 1
		) request ON true
		WHERE ai.run_id=$1
		  AND ai.turn_number=1
		  AND ai.status='succeeded'
		  AND EXISTS (
			SELECT 1
			FROM agent_invocation_events completed
			WHERE completed.invocation_id=ai.id
			  AND completed.event_type='tool.completed'
			  AND completed.payload->>'name'='documents.search'
		  )
		LIMIT 1
	`, runID.String()).Scan(&query)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load unknown-answer document search: %w", err)
	}
	return strings.TrimSpace(query), nil
}

func (s *Store) CompleteInvocation(ctx context.Context, invocation InvocationRecord, persona PlannedPersona, result agent.Result, estimatedCostMicros int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin invocation completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM agent_invocations WHERE id=$1 FOR UPDATE`, invocation.ID.String()).Scan(&status); err != nil {
		return fmt.Errorf("lock invocation: %w", err)
	}
	if status == "succeeded" {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM boardroom_runs WHERE id=$1 FOR UPDATE`, invocation.RunID.String()); err != nil {
		return fmt.Errorf("lock invocation run: %w", err)
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0)+1 FROM boardroom_messages WHERE run_id=$1`, invocation.RunID.String()).Scan(&sequence); err != nil {
		return fmt.Errorf("select invocation message sequence: %w", err)
	}
	metadata, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode invocation result: %w", err)
	}
	var messageID string
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `
		INSERT INTO boardroom_messages (run_id, persona_id, invocation_id, role, body, sequence, provider_metadata)
		VALUES ($1,$2,$3,'agent',$4,$5,$6)
		RETURNING id::text, created_at
	`, invocation.RunID.String(), persona.PersonaID.String(), invocation.ID.String(), result.Body, sequence, metadata).Scan(&messageID, &createdAt); err != nil {
		return fmt.Errorf("insert invocation message: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_invocations SET status='succeeded', model=NULLIF($2,''), provider_thread_id=NULLIF($3,''),
			result_payload=$4, input_tokens=$5, cached_input_tokens=$6, output_tokens=$7,
			estimated_cost_micros=$8, error_category=NULL, error_detail=NULL, completed_at=now(), updated_at=now()
		WHERE id=$1
	`, invocation.ID.String(), result.Model, result.ProviderThreadID, metadata, result.Usage.InputTokens, result.Usage.CachedInputTokens, result.Usage.OutputTokens, estimatedCostMicros); err != nil {
		return fmt.Errorf("complete invocation: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE boardroom_runs SET turn_count=GREATEST(turn_count,$2) WHERE id=$1`, invocation.RunID.String(), invocation.TurnNumber); err != nil {
		return fmt.Errorf("advance run turn: %w", err)
	}
	invocationPayload, _ := json.Marshal(map[string]any{"provider": result.Provider, "model": result.Model, "usage": result.Usage})
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_invocation_events (invocation_id,event_sequence,event_type,payload)
		VALUES ($1,(SELECT COALESCE(max(event_sequence),0)+1 FROM agent_invocation_events WHERE invocation_id=$1),'invocation.completed',$2)
	`, invocation.ID.String(), invocationPayload); err != nil {
		return fmt.Errorf("record invocation completion: %w", err)
	}
	messagePayload, _ := json.Marshal(map[string]any{
		"message_id": messageID, "sequence": sequence, "persona_name": persona.Name,
		"persona_role": persona.Role, "role": domain.MessageAgent, "body": result.Body, "created_at": createdAt,
	})
	if _, err := tx.Exec(ctx, `INSERT INTO boardroom_events (run_id,event_type,payload) VALUES ($1,'message.completed',$2)`, invocation.RunID.String(), messagePayload); err != nil {
		return fmt.Errorf("record invocation message event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE conversations SET updated_at=now()
		WHERE id=(SELECT conversation_id FROM boardroom_runs WHERE id=$1)
	`, invocation.RunID.String()); err != nil {
		return fmt.Errorf("update invocation conversation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit invocation completion: %w", err)
	}
	return nil
}
