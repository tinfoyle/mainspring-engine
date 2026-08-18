package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	agentapp "github.com/tinfoyle/spyglass-engine/internal/application/agents"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AgentRepository struct{ cell *database.CellPool }

func NewAgentRepository(cell *database.CellPool) (*AgentRepository, error) {
	if cell == nil {
		return nil, errors.New("agent repository cell pool is required")
	}
	return &AgentRepository{cell: cell}, nil
}

func (r *AgentRepository) CreateBoardroom(ctx context.Context, boardroom agentdomain.Boardroom) (agentdomain.Boardroom, bool, error) {
	var result agentdomain.Boardroom
	created := false
	err := r.cell.WithAccountTx(ctx, boardroom.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_boardrooms
			(account_id,id,name,purpose,state,version,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (account_id,id) DO NOTHING`, boardroom.AccountID, boardroom.ID, boardroom.Name, boardroom.Purpose, boardroom.State, boardroom.Version, boardroom.CreatedAt, boardroom.UpdatedAt)
		if err != nil {
			return err
		}
		created = tag.RowsAffected() == 1
		loaded, err := scanBoardroom(tx.QueryRow(ctx, `SELECT account_id,id,name,purpose,state,version,created_at,updated_at
			FROM spyglass.agent_boardrooms WHERE account_id=$1 AND id=$2`, boardroom.AccountID, boardroom.ID))
		if err != nil {
			return err
		}
		if !created && (loaded.ID != boardroom.ID || loaded.AccountID != boardroom.AccountID || loaded.Name != boardroom.Name ||
			loaded.Purpose != boardroom.Purpose || loaded.State != boardroom.State || loaded.Version != boardroom.Version) {
			return agentapp.ErrConflict
		}
		result = loaded
		return nil
	})
	if err != nil {
		return agentdomain.Boardroom{}, false, classifyAgentError(err)
	}
	return result, created, nil
}

func (r *AgentRepository) ListBoardrooms(ctx context.Context, accountID ids.AccountID, limit int) ([]agentdomain.Boardroom, error) {
	result := make([]agentdomain.Boardroom, 0, limit)
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT account_id,id,name,purpose,state,version,created_at,updated_at
			FROM spyglass.agent_boardrooms WHERE account_id=$1 ORDER BY updated_at DESC,id LIMIT $2`, accountID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanBoardroom(rows)
			if err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	return result, classifyAgentError(err)
}

func (r *AgentRepository) GetBoardroom(ctx context.Context, accountID ids.AccountID, boardroomID ids.BoardroomID) (agentdomain.Boardroom, error) {
	var result agentdomain.Boardroom
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		item, err := scanBoardroom(tx.QueryRow(ctx, `SELECT account_id,id,name,purpose,state,version,created_at,updated_at
			FROM spyglass.agent_boardrooms WHERE account_id=$1 AND id=$2`, accountID, boardroomID))
		if errors.Is(err, pgx.ErrNoRows) {
			return agentapp.ErrNotFound
		}
		result = item
		return err
	})
	return result, classifyAgentError(err)
}

func (r *AgentRepository) PublishPersona(ctx context.Context, boardroomID ids.BoardroomID, version agentdomain.PersonaVersion, expectedLatest uint64) (agentapp.PersonaSummary, bool, error) {
	var result agentapp.PersonaSummary
	created := false
	err := r.cell.WithAccountTx(ctx, version.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var boardroomState string
		if err := tx.QueryRow(ctx, `SELECT state FROM spyglass.agent_boardrooms WHERE account_id=$1 AND id=$2 FOR SHARE`, version.AccountID, boardroomID).Scan(&boardroomState); errors.Is(err, pgx.ErrNoRows) {
			return agentapp.ErrNotFound
		} else if err != nil {
			return err
		} else if boardroomState != "active" {
			return agentapp.ErrConstraint
		}
		var state string
		var latest uint64
		var createdAt, updatedAt time.Time
		err := tx.QueryRow(ctx, `SELECT state,latest_version,created_at,updated_at FROM spyglass.agent_personas
			WHERE account_id=$1 AND id=$2 FOR UPDATE`, version.AccountID, version.PersonaID).Scan(&state, &latest, &createdAt, &updatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			if expectedLatest != 0 || version.Version != 1 {
				return agentapp.ErrConflict
			}
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_personas
				(account_id,id,boardroom_id,state,latest_version,created_at,updated_at) VALUES ($1,$2,$3,'active',0,$4,$4)`,
				version.AccountID, version.PersonaID, boardroomID, version.CreatedAt); err != nil {
				return err
			}
			state, latest, createdAt, updatedAt = "active", 0, version.CreatedAt, version.CreatedAt
		} else if err != nil {
			return err
		} else {
			var existingBoardroom ids.BoardroomID
			if err := tx.QueryRow(ctx, `SELECT boardroom_id FROM spyglass.agent_personas WHERE account_id=$1 AND id=$2`, version.AccountID, version.PersonaID).Scan(&existingBoardroom); err != nil {
				return err
			}
			if existingBoardroom != boardroomID || state == "archived" {
				return agentapp.ErrConstraint
			}
		}
		if existing, found, err := loadPersonaVersion(ctx, tx, version.AccountID, version.ID); err != nil {
			return err
		} else if found {
			retryDraft := version.PersonaVersionDraft
			retryDraft.CreatedAt = existing.CreatedAt
			retryVersion, retryErr := agentdomain.NewPersonaVersion(retryDraft)
			if retryErr != nil || existing.ContentDigest != retryVersion.ContentDigest || existing.PersonaID != version.PersonaID {
				return agentapp.ErrConflict
			}
			result = agentapp.PersonaSummary{ID: version.PersonaID, BoardroomID: boardroomID, State: state, LatestVersion: latest, Published: existing, CreatedAt: createdAt, UpdatedAt: updatedAt}
			return nil
		}
		if latest != expectedLatest || version.Version != expectedLatest+1 {
			return agentapp.ErrConflict
		}
		policy, err := json.Marshal(version.Policy)
		if err != nil {
			return agentapp.ErrCorrupt
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_persona_versions
			(account_id,id,persona_id,version,name,role,description,system_instructions,policy,content_digest,created_by,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, version.AccountID, version.ID, version.PersonaID,
			version.Version, version.Name, version.Role, version.Description, version.SystemInstructions, policy, version.ContentDigest[:], version.CreatedBy, version.CreatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE spyglass.agent_personas SET latest_version=$3,updated_at=$4
			WHERE account_id=$1 AND id=$2`, version.AccountID, version.PersonaID, version.Version, version.CreatedAt); err != nil {
			return err
		}
		created = true
		result = agentapp.PersonaSummary{ID: version.PersonaID, BoardroomID: boardroomID, State: state, LatestVersion: version.Version, Published: version, CreatedAt: createdAt, UpdatedAt: version.CreatedAt}
		return nil
	})
	return result, created, classifyAgentError(err)
}

func (r *AgentRepository) ListPersonas(ctx context.Context, accountID ids.AccountID, boardroomID ids.BoardroomID, limit int) ([]agentapp.PersonaSummary, error) {
	result := make([]agentapp.PersonaSummary, 0, limit)
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT p.id,p.boardroom_id,p.state,p.latest_version,p.created_at,p.updated_at,
			v.id,v.persona_id,v.version,v.name,v.role,v.description,v.system_instructions,v.policy,v.content_digest,v.created_by,v.created_at
			FROM spyglass.agent_personas p JOIN spyglass.agent_persona_versions v
			ON v.account_id=p.account_id AND v.persona_id=p.id AND v.version=p.latest_version
			WHERE p.account_id=$1 AND p.boardroom_id=$2 ORDER BY p.created_at,p.id LIMIT $3`, accountID, boardroomID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item agentapp.PersonaSummary
			var rawPolicy []byte
			var digest []byte
			var version agentdomain.PersonaVersion
			version.AccountID = accountID
			if err := rows.Scan(&item.ID, &item.BoardroomID, &item.State, &item.LatestVersion, &item.CreatedAt, &item.UpdatedAt,
				&version.ID, &version.PersonaID, &version.Version, &version.Name, &version.Role, &version.Description,
				&version.SystemInstructions, &rawPolicy, &digest, &version.CreatedBy, &version.CreatedAt); err != nil {
				return err
			}
			if len(digest) != len(version.ContentDigest) || json.Unmarshal(rawPolicy, &version.Policy) != nil {
				return agentapp.ErrCorrupt
			}
			copy(version.ContentDigest[:], digest)
			validated, err := agentdomain.RestorePersonaVersion(version)
			if err != nil {
				return agentapp.ErrCorrupt
			}
			item.Published = validated
			result = append(result, item)
		}
		return rows.Err()
	})
	return result, classifyAgentError(err)
}

func (r *AgentRepository) StartRun(ctx context.Context, draft agentapp.StartRunDraft) (agentapp.Run, bool, error) {
	var result agentapp.Run
	created := false
	err := r.cell.WithAccountTx(ctx, draft.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if existing, found, err := loadAgentRun(ctx, tx, draft.AccountID, draft.RunID); err != nil {
			return err
		} else if found {
			if existing.Plan.BoardroomID != draft.BoardroomID || existing.Plan.ConversationID != draft.ConversationID || existing.Prompt != draft.Prompt || existing.Plan.CreatedBy != draft.Actor.UserID ||
				existing.Plan.EntitlementVersion != draft.EntitlementVersion || len(existing.Plan.Turns) != len(draft.PersonaIDs) || (draft.CreateConversation && existing.Subject != draft.Subject) {
				return agentapp.ErrConflict
			}
			for index, personaID := range draft.PersonaIDs {
				if existing.Plan.Turns[index].PersonaID != personaID {
					return agentapp.ErrConflict
				}
			}
			result = existing
			return nil
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "agent-runs:"+string(draft.AccountID)); err != nil {
			return err
		}
		var active int64
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM spyglass.agent_runs WHERE account_id=$1 AND state IN ('planned','running')`, draft.AccountID).Scan(&active); err != nil {
			return err
		}
		if active >= draft.MaximumConcurrentRun {
			return &accessLimitError{current: active, maximum: draft.MaximumConcurrentRun}
		}
		var boardroomVersion uint64
		var boardroomState string
		if err := tx.QueryRow(ctx, `SELECT version,state FROM spyglass.agent_boardrooms WHERE account_id=$1 AND id=$2 FOR SHARE`, draft.AccountID, draft.BoardroomID).Scan(&boardroomVersion, &boardroomState); errors.Is(err, pgx.ErrNoRows) {
			return agentapp.ErrNotFound
		} else if err != nil {
			return err
		} else if boardroomState != "active" {
			return agentapp.ErrConstraint
		}
		if draft.CreateConversation {
			if len(draft.Subject) < 2 || len(draft.Subject) > 240 {
				return agentapp.ErrConstraint
			}
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_conversations
				(account_id,id,boardroom_id,subject,state,next_message_sequence,created_by,created_at,updated_at)
				VALUES ($1,$2,$3,$4,'open',1,$5,$6,$6)`, draft.AccountID, draft.ConversationID, draft.BoardroomID, draft.Subject, draft.Actor.UserID, draft.CreatedAt); err != nil {
				return err
			}
		} else {
			var conversationBoardroom ids.BoardroomID
			var conversationState string
			if err := tx.QueryRow(ctx, `SELECT boardroom_id,state FROM spyglass.agent_conversations
				WHERE account_id=$1 AND id=$2 FOR UPDATE`, draft.AccountID, draft.ConversationID).Scan(&conversationBoardroom, &conversationState); errors.Is(err, pgx.ErrNoRows) {
				return agentapp.ErrNotFound
			} else if err != nil {
				return err
			} else if conversationBoardroom != draft.BoardroomID || conversationState != "open" {
				return agentapp.ErrConstraint
			}
		}
		var contextSequence int64
		if err := tx.QueryRow(ctx, `UPDATE spyglass.agent_conversations SET next_message_sequence=next_message_sequence+1,updated_at=$3
			WHERE account_id=$1 AND id=$2 RETURNING next_message_sequence-1`, draft.AccountID, draft.ConversationID, draft.CreatedAt).Scan(&contextSequence); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_user_messages
			(account_id,id,conversation_id,sequence,body,created_by,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			draft.AccountID, draft.UserMessageID, draft.ConversationID, contextSequence, draft.Prompt, draft.Actor.UserID, draft.CreatedAt); err != nil {
			return err
		}
		versions := make([]agentdomain.PersonaVersion, len(draft.PersonaIDs))
		turns := make([]agentdomain.PlannedTurn, len(draft.PersonaIDs))
		for index, personaID := range draft.PersonaIDs {
			version, found, err := loadLatestPersonaVersion(ctx, tx, draft.AccountID, draft.BoardroomID, personaID)
			if err != nil {
				return err
			}
			if !found {
				return agentapp.ErrNotFound
			}
			versions[index] = version
			turns[index] = agentdomain.PlannedTurn{Turn: uint32(index + 1), PersonaID: personaID, PersonaVersionID: version.ID, PersonaDigest: version.ContentDigest}
		}
		plan, err := agentdomain.NewRunPlan(agentdomain.RunPlan{RunID: draft.RunID, AccountID: draft.AccountID, BoardroomID: draft.BoardroomID,
			ConversationID: draft.ConversationID, EntitlementVersion: draft.EntitlementVersion, PolicyVersion: boardroomVersion,
			Turns: turns, CreatedBy: draft.Actor.UserID, CreatedAt: draft.CreatedAt})
		if err != nil {
			return agentapp.ErrCorrupt
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_runs
			(account_id,id,boardroom_id,conversation_id,state,entitlement_version,policy_version,plan_digest,turn_count,created_by,created_at)
			VALUES ($1,$2,$3,$4,'planned',$5,$6,$7,$8,$9,$10)`, draft.AccountID, draft.RunID, draft.BoardroomID,
			draft.ConversationID, draft.EntitlementVersion, boardroomVersion, plan.Digest[:], len(turns), draft.Actor.UserID, draft.CreatedAt); err != nil {
			return err
		}
		invocationIDs := make([]ids.AgentInvocationID, len(turns))
		for index, turn := range turns {
			invocationRaw, err := ids.Derive(string(draft.RunID), fmt.Sprintf("turn/%d/invocation", index+1))
			if err != nil {
				return agentapp.ErrCorrupt
			}
			invocationID := ids.AgentInvocationID(invocationRaw)
			invocationIDs[index] = invocationID
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_run_plan_turns
				(account_id,run_id,turn,persona_id,persona_version_id,persona_digest) VALUES ($1,$2,$3,$4,$5,$6)`,
				draft.AccountID, draft.RunID, index+1, turn.PersonaID, turn.PersonaVersionID, turn.PersonaDigest[:]); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_invocations
				(account_id,id,run_id,turn,persona_version_id,status,expected_provider,requested_model,queued_at)
				VALUES ($1,$2,$3,$4,$5,'queued',$6,$7,$8)`, draft.AccountID, invocationID, draft.RunID, index+1,
				turn.PersonaVersionID, versions[index].Policy.Provider, versions[index].Policy.Model, draft.CreatedAt); err != nil {
				return err
			}
			modelIDs, toolIDs := make([]string, versions[index].Policy.MaximumToolSteps+1), make([]string, versions[index].Policy.MaximumToolSteps)
			for operation := range modelIDs {
				modelIDs[operation], err = ids.Derive(invocationRaw, fmt.Sprintf("model/%d", operation+1))
				if err != nil {
					return agentapp.ErrCorrupt
				}
			}
			for operation := range toolIDs {
				toolIDs[operation], err = ids.Derive(invocationRaw, fmt.Sprintf("tool/%d", operation+1))
				if err != nil {
					return agentapp.ErrCorrupt
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_invocation_execution_plans
				(account_id,invocation_id,conversation_id,context_sequence,profile,model_operation_ids,tool_operation_ids,request_expires_at,created_at)
				VALUES ($1,$2,$3,$4,$5,$6::uuid[],$7::uuid[],$8,$9)`, draft.AccountID, invocationID, draft.ConversationID,
				contextSequence, profileFor(versions[index].Policy), modelIDs, toolIDs, draft.RequestExpiresAt, draft.CreatedAt); err != nil {
				return err
			}
		}
		created = true
		result = agentapp.Run{Plan: plan, State: "planned", Subject: draft.Subject, Prompt: draft.Prompt, UserMessageID: draft.UserMessageID, InvocationIDs: invocationIDs}
		return nil
	})
	if err != nil {
		var limit *accessLimitError
		if errors.As(err, &limit) {
			return agentapp.Run{}, false, &agentapp.ConcurrentRunLimitError{Current: limit.current, Maximum: limit.maximum}
		}
		return agentapp.Run{}, false, classifyAgentError(err)
	}
	return result, created, nil
}

func (r *AgentRepository) GetRun(ctx context.Context, accountID ids.AccountID, runID ids.RunID) (agentapp.Run, error) {
	var result agentapp.Run
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		loaded, found, err := loadAgentRun(ctx, tx, accountID, runID)
		if err != nil {
			return err
		}
		if !found {
			return agentapp.ErrNotFound
		}
		result = loaded
		return nil
	})
	return result, classifyAgentError(err)
}

// agentRunLimitError is translated by the application-facing wrapper below;
// keeping SQL adapters free of routed entitlement objects avoids trusting
// caller-provided package metadata inside persistence.
type accessLimitError struct{ current, maximum int64 }

func (e *accessLimitError) Error() string { return "agent concurrent run limit reached" }

func scanBoardroom(row interface{ Scan(...any) error }) (agentdomain.Boardroom, error) {
	var item agentdomain.Boardroom
	if err := row.Scan(&item.AccountID, &item.ID, &item.Name, &item.Purpose, &item.State, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return agentdomain.Boardroom{}, err
	}
	validated, err := agentdomain.RestoreBoardroom(item)
	if err != nil {
		return agentdomain.Boardroom{}, agentapp.ErrCorrupt
	}
	return validated, nil
}

func loadPersonaVersion(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, versionID ids.PersonaVersionID) (agentdomain.PersonaVersion, bool, error) {
	var item agentdomain.PersonaVersion
	var rawPolicy, digest []byte
	item.AccountID = accountID
	err := tx.QueryRow(ctx, `SELECT id,persona_id,version,name,role,description,system_instructions,policy,content_digest,created_by,created_at
		FROM spyglass.agent_persona_versions WHERE account_id=$1 AND id=$2`, accountID, versionID).Scan(
		&item.ID, &item.PersonaID, &item.Version, &item.Name, &item.Role, &item.Description, &item.SystemInstructions,
		&rawPolicy, &digest, &item.CreatedBy, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return agentdomain.PersonaVersion{}, false, nil
	}
	if err != nil {
		return agentdomain.PersonaVersion{}, false, err
	}
	if len(digest) != len(item.ContentDigest) || json.Unmarshal(rawPolicy, &item.Policy) != nil {
		return agentdomain.PersonaVersion{}, false, agentapp.ErrCorrupt
	}
	copy(item.ContentDigest[:], digest)
	validated, err := agentdomain.RestorePersonaVersion(item)
	if err != nil {
		return agentdomain.PersonaVersion{}, false, agentapp.ErrCorrupt
	}
	return validated, true, nil
}

func loadLatestPersonaVersion(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, boardroomID ids.BoardroomID, personaID ids.PersonaID) (agentdomain.PersonaVersion, bool, error) {
	var versionID ids.PersonaVersionID
	var state string
	err := tx.QueryRow(ctx, `SELECT v.id,p.state FROM spyglass.agent_personas p JOIN spyglass.agent_persona_versions v
		ON v.account_id=p.account_id AND v.persona_id=p.id AND v.version=p.latest_version
		WHERE p.account_id=$1 AND p.id=$2 AND p.boardroom_id=$3 FOR SHARE OF p,v`, accountID, personaID, boardroomID).Scan(&versionID, &state)
	if errors.Is(err, pgx.ErrNoRows) || state != "active" {
		return agentdomain.PersonaVersion{}, false, nil
	}
	if err != nil {
		return agentdomain.PersonaVersion{}, false, err
	}
	return loadPersonaVersion(ctx, tx, accountID, versionID)
}

func loadAgentRun(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, runID ids.RunID) (agentapp.Run, bool, error) {
	var plan agentdomain.RunPlan
	var state, subject, prompt string
	var digest []byte
	var contextSequence int64
	plan.AccountID, plan.RunID = accountID, runID
	err := tx.QueryRow(ctx, `SELECT r.boardroom_id,r.conversation_id,r.state,r.entitlement_version,r.policy_version,r.plan_digest,r.created_by,r.created_at,
		c.subject,e.context_sequence,u.body
		FROM spyglass.agent_runs r JOIN spyglass.agent_conversations c ON c.account_id=r.account_id AND c.id=r.conversation_id
		JOIN spyglass.agent_invocations i ON i.account_id=r.account_id AND i.run_id=r.id AND i.turn=1
		JOIN spyglass.agent_invocation_execution_plans e ON e.account_id=i.account_id AND e.invocation_id=i.id
		JOIN spyglass.agent_user_messages u ON u.account_id=r.account_id AND u.conversation_id=r.conversation_id AND u.sequence=e.context_sequence
		WHERE r.account_id=$1 AND r.id=$2`, accountID, runID).Scan(&plan.BoardroomID, &plan.ConversationID, &state,
		&plan.EntitlementVersion, &plan.PolicyVersion, &digest, &plan.CreatedBy, &plan.CreatedAt, &subject, &contextSequence, &prompt)
	if errors.Is(err, pgx.ErrNoRows) {
		return agentapp.Run{}, false, nil
	}
	if err != nil {
		return agentapp.Run{}, false, err
	}
	if len(digest) != len(plan.Digest) {
		return agentapp.Run{}, false, agentapp.ErrCorrupt
	}
	copy(plan.Digest[:], digest)
	rows, err := tx.Query(ctx, `SELECT t.turn,t.persona_id,t.persona_version_id,t.persona_digest,i.id
		FROM spyglass.agent_run_plan_turns t JOIN spyglass.agent_invocations i
		ON i.account_id=t.account_id AND i.run_id=t.run_id AND i.turn=t.turn
		WHERE t.account_id=$1 AND t.run_id=$2 ORDER BY t.turn`, accountID, runID)
	if err != nil {
		return agentapp.Run{}, false, err
	}
	defer rows.Close()
	var invocations []ids.AgentInvocationID
	for rows.Next() {
		var turn agentdomain.PlannedTurn
		var turnNumber int
		var rawDigest []byte
		var invocationID ids.AgentInvocationID
		if err := rows.Scan(&turnNumber, &turn.PersonaID, &turn.PersonaVersionID, &rawDigest, &invocationID); err != nil || len(rawDigest) != len(turn.PersonaDigest) {
			return agentapp.Run{}, false, agentapp.ErrCorrupt
		}
		turn.Turn = uint32(turnNumber)
		copy(turn.PersonaDigest[:], rawDigest)
		plan.Turns = append(plan.Turns, turn)
		invocations = append(invocations, invocationID)
	}
	if err := rows.Err(); err != nil {
		return agentapp.Run{}, false, err
	}
	validated, err := agentdomain.RestoreRunPlan(plan)
	if err != nil {
		return agentapp.Run{}, false, agentapp.ErrCorrupt
	}
	var messageID ids.MessageID
	if err := tx.QueryRow(ctx, `SELECT id FROM spyglass.agent_user_messages
		WHERE account_id=$1 AND conversation_id=$2 AND sequence=$3`, accountID, plan.ConversationID, contextSequence).Scan(&messageID); err != nil {
		return agentapp.Run{}, false, err
	}
	return agentapp.Run{Plan: validated, State: state, Subject: subject, Prompt: prompt, UserMessageID: messageID, InvocationIDs: invocations}, true, nil
}

func profileFor(policy agentdomain.PersonaPolicy) string {
	if policy.MaximumInputTokens > 500_000 || policy.MaximumOutputTokens > 16_384 {
		return "agent-large"
	}
	if policy.MaximumInputTokens > 128_000 || policy.MaximumOutputTokens > 4_096 || policy.MaximumToolSteps > 2 {
		return "agent-medium"
	}
	return "agent-small"
}

func classifyAgentError(err error) error {
	if err == nil || errors.Is(err, agentapp.ErrNotFound) || errors.Is(err, agentapp.ErrConflict) || errors.Is(err, agentapp.ErrConstraint) || errors.Is(err, agentapp.ErrCorrupt) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return agentapp.ErrConflict
		case "23503", "23514", "22023":
			return agentapp.ErrConstraint
		}
	}
	return fmt.Errorf("agent repository unavailable: %w", err)
}

var _ agentapp.Repository = (*AgentRepository)(nil)
