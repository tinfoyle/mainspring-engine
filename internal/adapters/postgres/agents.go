package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"
	"unicode/utf8"

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

func (r *AgentRepository) ListConversations(ctx context.Context, accountID ids.AccountID, boardroomID ids.BoardroomID, query agentapp.ConversationListQuery) (agentapp.ConversationPage, error) {
	result := agentapp.ConversationPage{Items: make([]agentapp.Conversation, 0, query.Limit)}
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var cursorID any
		if query.AfterUpdatedAt != nil {
			cursorID = query.AfterID
		}
		rows, err := tx.Query(ctx, `SELECT account_id,id,boardroom_id,subject,state,next_message_sequence-1,created_by,created_at,updated_at
			FROM spyglass.agent_conversations
			WHERE account_id=$1 AND boardroom_id=$2
			  AND ($3::timestamptz IS NULL OR (updated_at,id)<($3,$4::uuid))
			ORDER BY updated_at DESC,id DESC LIMIT $5`, accountID, boardroomID, query.AfterUpdatedAt, cursorID, query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanConversation(rows)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(result.Items) > query.Limit {
			last := result.Items[query.Limit-1]
			result.Items = result.Items[:query.Limit]
			result.NextCursor = &agentapp.ConversationCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
		}
		return nil
	})
	return result, classifyAgentError(err)
}

func (r *AgentRepository) GetConversation(ctx context.Context, accountID ids.AccountID, conversationID ids.ConversationID) (agentapp.Conversation, error) {
	var result agentapp.Conversation
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		item, err := scanConversation(tx.QueryRow(ctx, `SELECT account_id,id,boardroom_id,subject,state,next_message_sequence-1,created_by,created_at,updated_at
			FROM spyglass.agent_conversations WHERE account_id=$1 AND id=$2`, accountID, conversationID))
		if errors.Is(err, pgx.ErrNoRows) {
			return agentapp.ErrNotFound
		}
		result = item
		return err
	})
	return result, classifyAgentError(err)
}

func (r *AgentRepository) ListMessages(ctx context.Context, accountID ids.AccountID, conversationID ids.ConversationID, query agentapp.MessageListQuery) (agentapp.MessagePage, error) {
	result := agentapp.MessagePage{Items: make([]agentapp.Message, 0, query.Limit)}
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT true FROM spyglass.agent_conversations WHERE account_id=$1 AND id=$2`, accountID, conversationID).Scan(&exists); errors.Is(err, pgx.ErrNoRows) {
			return agentapp.ErrNotFound
		} else if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,conversation_id,sequence,role,body,created_by,run_id,invocation_id,persona_version_id,structured_result,created_at
			FROM (
				SELECT u.id,u.conversation_id,u.sequence,'user'::text AS role,u.body,u.created_by::text,
				       NULL::text AS run_id,NULL::text AS invocation_id,NULL::text AS persona_version_id,NULL::jsonb AS structured_result,u.created_at
				FROM spyglass.agent_user_messages u WHERE u.account_id=$1 AND u.conversation_id=$2 AND u.sequence>$3
				UNION ALL
				SELECT m.id,m.conversation_id,m.sequence,m.role,m.body,NULL::text,m.run_id::text,m.invocation_id::text,
				       m.persona_version_id::text,m.structured_result,m.created_at
				FROM spyglass.agent_messages m WHERE m.account_id=$1 AND m.conversation_id=$2 AND m.sequence>$3
			) entries ORDER BY sequence LIMIT $4`, accountID, conversationID, query.AfterSequence, query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		var previous uint64
		for rows.Next() {
			item, err := scanAgentMessage(rows)
			if err != nil {
				return err
			}
			if previous != 0 && item.Sequence <= previous {
				return agentapp.ErrCorrupt
			}
			previous = item.Sequence
			result.Items = append(result.Items, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(result.Items) > query.Limit {
			next := result.Items[query.Limit-1].Sequence
			result.Items = result.Items[:query.Limit]
			result.NextAfterSequence = &next
		}
		return nil
	})
	return result, classifyAgentError(err)
}

func (r *AgentRepository) StartRun(ctx context.Context, draft agentapp.StartRunDraft) (agentapp.Run, bool, error) {
	var result agentapp.Run
	created := false
	err := r.cell.WithAccountTx(ctx, draft.AccountID, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(ctx context.Context, tx pgx.Tx) error {
		if existing, found, err := loadAgentRun(ctx, tx, draft.AccountID, draft.RunID); err != nil {
			return err
		} else if found {
			if existing.Plan.BoardroomID != draft.BoardroomID || existing.Plan.ConversationID != draft.ConversationID || existing.Prompt != draft.Prompt || existing.Plan.CreatedBy != draft.Actor.UserID ||
				existing.Plan.EntitlementVersion != draft.EntitlementVersion || len(existing.Plan.Turns) != len(draft.PersonaIDs) || (draft.CreateConversation && existing.Subject != draft.Subject) || !contextSelectionMatches(existing.Context, draft.Context) {
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
		contextPayload, contextDigest, contextReferences, err := buildAgentContext(ctx, tx, draft.AccountID, draft.Context, draft.CanReadRestricted)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_runs
			(account_id,id,boardroom_id,conversation_id,state,entitlement_version,policy_version,plan_digest,turn_count,created_by,created_at,context_payload,context_digest,context_item_count)
			VALUES ($1,$2,$3,$4,'planned',$5,$6,$7,$8,$9,$10,$11,$12,$13)`, draft.AccountID, draft.RunID, draft.BoardroomID,
			draft.ConversationID, draft.EntitlementVersion, boardroomVersion, plan.Digest[:], len(turns), draft.Actor.UserID, draft.CreatedAt,
			contextPayload, contextDigest[:], len(contextReferences)); err != nil {
			return err
		}
		invocationIDs := make([]ids.AgentInvocationID, len(turns))
		for index, turn := range turns {
			invocationID, err := insertAgentInvocation(ctx, tx, draft.AccountID, draft.RunID, draft.ConversationID, int64(contextSequence), index+1, turn, versions[index], draft.CreatedAt, draft.RequestExpiresAt)
			if err != nil {
				return err
			}
			invocationIDs[index] = invocationID
		}
		created = true
		result = agentapp.Run{Plan: plan, State: "planned", Subject: draft.Subject, Prompt: draft.Prompt, UserMessageID: draft.UserMessageID, InvocationIDs: invocationIDs, Context: contextReferences, ContextDigest: contextDigest}
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

func (r *AgentRepository) ResolveRun(ctx context.Context, draft agentapp.ResolveRunDraft) (agentapp.RunResolution, bool, error) {
	var result agentapp.RunResolution
	created := false
	err := r.cell.WithAccountTx(ctx, draft.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if existing, found, err := loadRunResolution(ctx, tx, draft.AccountID, draft.ResolutionID); err != nil {
			return err
		} else if found {
			if existing.RunID != draft.RunID || existing.Action != draft.Action || existing.Note != draft.Note || existing.ActorID != draft.Actor.UserID || existing.RetryRunID != draft.RetryRunID {
				return agentapp.ErrConflict
			}
			result = existing
			return nil
		}
		var sourceState string
		if err := tx.QueryRow(ctx, `SELECT state FROM spyglass.agent_runs WHERE account_id=$1 AND id=$2 FOR UPDATE`, draft.AccountID, draft.RunID).Scan(&sourceState); errors.Is(err, pgx.ErrNoRows) {
			return agentapp.ErrNotFound
		} else if err != nil {
			return err
		}
		if sourceState != "failed" && sourceState != "partially_failed" {
			return agentapp.ErrConstraint
		}
		result = agentapp.RunResolution{ID: draft.ResolutionID, RunID: draft.RunID, Action: draft.Action, Note: draft.Note, ActorID: draft.Actor.UserID, RetryRunID: draft.RetryRunID, CreatedAt: draft.CreatedAt}
		if draft.Action == agentapp.RunResolutionRetryFailed {
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
			source, found, err := loadAgentRun(ctx, tx, draft.AccountID, draft.RunID)
			if err != nil {
				return err
			}
			if !found || source.State != sourceState {
				return agentapp.ErrCorrupt
			}
			var boardroomState, conversationState string
			if err := tx.QueryRow(ctx, `SELECT b.state,c.state FROM spyglass.agent_boardrooms b JOIN spyglass.agent_conversations c
				ON c.account_id=b.account_id AND c.boardroom_id=b.id WHERE b.account_id=$1 AND b.id=$2 AND c.id=$3 FOR SHARE OF b,c`,
				draft.AccountID, source.Plan.BoardroomID, source.Plan.ConversationID).Scan(&boardroomState, &conversationState); err != nil {
				return err
			}
			if boardroomState != "active" || conversationState != "open" {
				return agentapp.ErrConstraint
			}
			versions := make([]agentdomain.PersonaVersion, 0, len(source.Invocations))
			turns := make([]agentdomain.PlannedTurn, 0, len(source.Invocations))
			var contextSequence int64
			for _, invocation := range source.Invocations {
				retryableFailure := invocation.Status == "failed"
				retryableSuccessor := invocation.Status == "canceled" && invocation.FailureCode == "prior_turn_failed"
				if !retryableFailure && !retryableSuccessor {
					continue
				}
				if invocation.Turn == 0 || int(invocation.Turn) > len(source.Plan.Turns) {
					return agentapp.ErrCorrupt
				}
				original := source.Plan.Turns[invocation.Turn-1]
				if original.PersonaVersionID != invocation.PersonaVersionID {
					return agentapp.ErrCorrupt
				}
				var invocationContext int64
				if err := tx.QueryRow(ctx, `SELECT context_sequence FROM spyglass.agent_invocation_execution_plans
					WHERE account_id=$1 AND invocation_id=$2`, draft.AccountID, invocation.ID).Scan(&invocationContext); err != nil {
					return err
				}
				if contextSequence == 0 {
					contextSequence = invocationContext
				} else if retryableFailure && contextSequence != invocationContext {
					return agentapp.ErrCorrupt
				}
				version, found, err := loadPersonaVersion(ctx, tx, draft.AccountID, invocation.PersonaVersionID)
				if err != nil {
					return err
				}
				if !found || version.PersonaID != original.PersonaID || version.ContentDigest != original.PersonaDigest {
					return agentapp.ErrCorrupt
				}
				versions = append(versions, version)
				turns = append(turns, agentdomain.PlannedTurn{Turn: uint32(len(turns) + 1), PersonaID: original.PersonaID, PersonaVersionID: original.PersonaVersionID, PersonaDigest: original.PersonaDigest})
			}
			if len(turns) == 0 || contextSequence < 1 {
				return agentapp.ErrCorrupt
			}
			plan, err := agentdomain.NewRunPlan(agentdomain.RunPlan{RunID: draft.RetryRunID, AccountID: draft.AccountID, BoardroomID: source.Plan.BoardroomID,
				ConversationID: source.Plan.ConversationID, EntitlementVersion: draft.EntitlementVersion, PolicyVersion: source.Plan.PolicyVersion,
				Turns: turns, CreatedBy: draft.Actor.UserID, CreatedAt: draft.CreatedAt})
			if err != nil {
				return agentapp.ErrCorrupt
			}
			var retryContextPayload, retryContextDigest []byte
			var retryContextCount int
			if err := tx.QueryRow(ctx, `SELECT context_payload,context_digest,context_item_count FROM spyglass.agent_runs
				WHERE account_id=$1 AND id=$2 FOR SHARE`, draft.AccountID, draft.RunID).Scan(&retryContextPayload, &retryContextDigest, &retryContextCount); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_runs
				(account_id,id,boardroom_id,conversation_id,state,entitlement_version,policy_version,plan_digest,turn_count,created_by,created_at,context_payload,context_digest,context_item_count)
				VALUES ($1,$2,$3,$4,'planned',$5,$6,$7,$8,$9,$10,$11,$12,$13)`, draft.AccountID, draft.RetryRunID, plan.BoardroomID,
				plan.ConversationID, plan.EntitlementVersion, plan.PolicyVersion, plan.Digest[:], len(turns), draft.Actor.UserID, draft.CreatedAt,
				retryContextPayload, retryContextDigest, retryContextCount); err != nil {
				return err
			}
			for index, turn := range turns {
				if _, err := insertAgentInvocation(ctx, tx, draft.AccountID, draft.RetryRunID, plan.ConversationID, contextSequence, index+1, turn, versions[index], draft.CreatedAt, draft.RequestExpiresAt); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_run_resolutions
			(account_id,id,run_id,action,note,actor_user_id,retry_run_id,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			draft.AccountID, draft.ResolutionID, draft.RunID, draft.Action, draft.Note, draft.Actor.UserID, nullableRunID(draft.RetryRunID), draft.CreatedAt); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		var limit *accessLimitError
		if errors.As(err, &limit) {
			return agentapp.RunResolution{}, false, &agentapp.ConcurrentRunLimitError{Current: limit.current, Maximum: limit.maximum}
		}
		return agentapp.RunResolution{}, false, classifyAgentError(err)
	}
	return result, created, nil
}

func insertAgentInvocation(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, runID ids.RunID, conversationID ids.ConversationID, contextSequence int64, turnNumber int, turn agentdomain.PlannedTurn, version agentdomain.PersonaVersion, createdAt, requestExpiresAt time.Time) (ids.AgentInvocationID, error) {
	invocationRaw, err := ids.Derive(string(runID), fmt.Sprintf("turn/%d/invocation", turnNumber))
	if err != nil {
		return "", agentapp.ErrCorrupt
	}
	invocationID := ids.AgentInvocationID(invocationRaw)
	if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_run_plan_turns
		(account_id,run_id,turn,persona_id,persona_version_id,persona_digest) VALUES ($1,$2,$3,$4,$5,$6)`,
		accountID, runID, turnNumber, turn.PersonaID, turn.PersonaVersionID, turn.PersonaDigest[:]); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_invocations
		(account_id,id,run_id,turn,persona_version_id,status,expected_provider,requested_model,queued_at)
		VALUES ($1,$2,$3,$4,$5,'queued',$6,$7,$8)`, accountID, invocationID, runID, turnNumber,
		turn.PersonaVersionID, version.Policy.Provider, version.Policy.Model, createdAt); err != nil {
		return "", err
	}
	modelIDs, toolIDs := make([]string, version.Policy.MaximumToolSteps+1), make([]string, version.Policy.MaximumToolSteps)
	for operation := range modelIDs {
		modelIDs[operation], err = ids.Derive(invocationRaw, fmt.Sprintf("model/%d", operation+1))
		if err != nil {
			return "", agentapp.ErrCorrupt
		}
	}
	for operation := range toolIDs {
		toolIDs[operation], err = ids.Derive(invocationRaw, fmt.Sprintf("tool/%d", operation+1))
		if err != nil {
			return "", agentapp.ErrCorrupt
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO spyglass.agent_invocation_execution_plans
		(account_id,invocation_id,conversation_id,context_sequence,profile,model_operation_ids,tool_operation_ids,request_expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6::uuid[],$7::uuid[],$8,$9)`, accountID, invocationID, conversationID,
		contextSequence, profileFor(version.Policy), modelIDs, toolIDs, requestExpiresAt, createdAt); err != nil {
		return "", err
	}
	return invocationID, nil
}

type frozenContextEnvelope struct {
	SchemaVersion int                 `json:"schema_version"`
	Items         []frozenContextItem `json:"items"`
}

type frozenContextItem struct {
	Kind    string          `json:"kind"`
	ID      string          `json:"id"`
	Version uint64          `json:"version"`
	Digest  string          `json:"digest"`
	Content json.RawMessage `json:"content"`
}

func buildAgentContext(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, selection agentapp.ContextSelection, canReadRestricted bool) ([]byte, [sha256.Size]byte, []agentapp.ContextReference, error) {
	envelope := frozenContextEnvelope{SchemaVersion: 1, Items: make([]frozenContextItem, 0, len(selection.WorkItemIDs)+len(selection.KnowledgeFactIDs)+len(selection.KnowledgeDocumentIDs)+len(selection.BaselineAssessmentIDs))}
	add := func(kind, id string, version uint64, content any) error {
		raw, err := json.Marshal(content)
		if err != nil || version == 0 {
			return agentapp.ErrCorrupt
		}
		digest := sha256.Sum256(raw)
		envelope.Items = append(envelope.Items, frozenContextItem{Kind: kind, ID: id, Version: version, Digest: fmt.Sprintf("%x", digest), Content: raw})
		return nil
	}
	for _, id := range selection.WorkItemIDs {
		var number, version int64
		var kind, title, description, state, priority, responsibility string
		var dueAt *time.Time
		err := tx.QueryRow(ctx, `SELECT number,version,kind,title,description,state,priority,responsibility,due_at
			FROM spyglass.work_items WHERE account_id=$1 AND id=$2 FOR SHARE`, accountID, id).Scan(&number, &version, &kind, &title, &description, &state, &priority, &responsibility, &dueAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, [sha256.Size]byte{}, nil, agentapp.ErrNotFound
		}
		if err != nil {
			return nil, [sha256.Size]byte{}, nil, err
		}
		if number < 1 || version < 1 {
			return nil, [sha256.Size]byte{}, nil, agentapp.ErrCorrupt
		}
		content := struct {
			Number         int64      `json:"number"`
			Kind           string     `json:"kind"`
			Title          string     `json:"title"`
			Description    string     `json:"description"`
			State          string     `json:"state"`
			Priority       string     `json:"priority"`
			Responsibility string     `json:"responsibility"`
			DueAt          *time.Time `json:"due_at,omitempty"`
		}{number, kind, title, description, state, priority, responsibility, dueAt}
		if err := add("work_item", string(id), uint64(version), content); err != nil {
			return nil, [sha256.Size]byte{}, nil, err
		}
	}
	for _, id := range selection.KnowledgeFactIDs {
		var revision int64
		var scope, key, state, sensitivity string
		var confidence int16
		var canonical []byte
		err := tx.QueryRow(ctx, `SELECT f.revision,f.scope_kind,f.fact_key,f.state,c.canonical_value,c.confidence,c.sensitivity
			FROM spyglass.knowledge_facts f JOIN spyglass.knowledge_claims c ON c.account_id=f.account_id AND c.id=f.current_claim_id
			WHERE f.account_id=$1 AND f.id=$2 AND f.state='active' AND c.state='accepted' AND (c.sensitivity<>'restricted' OR $3) FOR SHARE OF f,c`, accountID, id, canReadRestricted).Scan(&revision, &scope, &key, &state, &canonical, &confidence, &sensitivity)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, [sha256.Size]byte{}, nil, agentapp.ErrNotFound
		}
		if err != nil {
			return nil, [sha256.Size]byte{}, nil, err
		}
		if revision < 1 || !json.Valid(canonical) {
			return nil, [sha256.Size]byte{}, nil, agentapp.ErrCorrupt
		}
		content := struct {
			Scope       string          `json:"scope"`
			Key         string          `json:"key"`
			State       string          `json:"state"`
			Value       json.RawMessage `json:"value"`
			Confidence  int16           `json:"confidence"`
			Sensitivity string          `json:"sensitivity"`
		}{scope, key, state, canonical, confidence, sensitivity}
		if err := add("knowledge_fact", string(id), uint64(revision), content); err != nil {
			return nil, [sha256.Size]byte{}, nil, err
		}
	}
	for _, id := range selection.KnowledgeDocumentIDs {
		var revision, chunkCount int64
		var revisionID, title, sensitivity, filename, mediaType, indexGeneration string
		var textDigest []byte
		err := tx.QueryRow(ctx, `SELECT d.current_revision,r.id,d.title,d.sensitivity,r.filename,r.verified_media_type,
			r.text_sha256,r.index_generation,r.chunk_count
			FROM spyglass.knowledge_documents d JOIN spyglass.knowledge_document_revisions r
			ON r.account_id=d.account_id AND r.id=d.current_revision_id
			WHERE d.account_id=$1 AND d.id=$2 AND d.state='ready' AND r.state='ready'
			AND (d.sensitivity<>'restricted' OR $3) FOR SHARE OF d,r`, accountID, id, canReadRestricted).Scan(
			&revision, &revisionID, &title, &sensitivity, &filename, &mediaType, &textDigest, &indexGeneration, &chunkCount)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, [sha256.Size]byte{}, nil, agentapp.ErrNotFound
		}
		if err != nil {
			return nil, [sha256.Size]byte{}, nil, err
		}
		if revision < 1 || ids.Validate(revisionID) != nil || len(textDigest) != sha256.Size || chunkCount < 1 || chunkCount > 16384 {
			return nil, [sha256.Size]byte{}, nil, agentapp.ErrCorrupt
		}
		type documentChunk struct {
			Index       int64  `json:"index"`
			StartByte   int64  `json:"start_byte"`
			EndByte     int64  `json:"end_byte"`
			Content     string `json:"content"`
			ContentHash string `json:"content_sha256"`
		}
		chunks := make([]documentChunk, 0, chunkCount)
		rows, err := tx.Query(ctx, `SELECT chunk_index,start_byte,end_byte,content,content_sha256
			FROM spyglass.knowledge_document_chunks WHERE account_id=$1 AND revision_id=$2 AND index_generation=$3 ORDER BY chunk_index`, accountID, revisionID, indexGeneration)
		if err != nil {
			return nil, [sha256.Size]byte{}, nil, err
		}
		for rows.Next() {
			var chunk documentChunk
			var contentDigest []byte
			if err := rows.Scan(&chunk.Index, &chunk.StartByte, &chunk.EndByte, &chunk.Content, &contentDigest); err != nil {
				rows.Close()
				return nil, [sha256.Size]byte{}, nil, err
			}
			computed := sha256.Sum256([]byte(chunk.Content))
			if chunk.Index != int64(len(chunks)) || chunk.StartByte < 0 || chunk.EndByte <= chunk.StartByte || len(contentDigest) != sha256.Size || !bytes.Equal(contentDigest, computed[:]) {
				rows.Close()
				return nil, [sha256.Size]byte{}, nil, agentapp.ErrCorrupt
			}
			chunk.ContentHash = hex.EncodeToString(contentDigest)
			chunks = append(chunks, chunk)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, [sha256.Size]byte{}, nil, err
		}
		if int64(len(chunks)) != chunkCount {
			return nil, [sha256.Size]byte{}, nil, agentapp.ErrCorrupt
		}
		content := struct {
			Title           string          `json:"title"`
			Sensitivity     string          `json:"sensitivity"`
			RevisionID      string          `json:"revision_id"`
			Filename        string          `json:"filename"`
			MediaType       string          `json:"media_type"`
			TextSHA256      string          `json:"text_sha256"`
			IndexGeneration string          `json:"index_generation"`
			Chunks          []documentChunk `json:"chunks"`
		}{title, sensitivity, revisionID, filename, mediaType, hex.EncodeToString(textDigest), indexGeneration, chunks}
		if err := add("knowledge_document", string(id), uint64(revision), content); err != nil {
			return nil, [sha256.Size]byte{}, nil, err
		}
	}
	for _, id := range selection.BaselineAssessmentIDs {
		var version int64
		var catalogVersion, scopePolicyVersion, state string
		err := tx.QueryRow(ctx, `SELECT version,catalog_version,scope_policy_version,state FROM spyglass.baseline_assessments
			WHERE account_id=$1 AND id=$2 FOR SHARE`, accountID, id).Scan(&version, &catalogVersion, &scopePolicyVersion, &state)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, [sha256.Size]byte{}, nil, agentapp.ErrNotFound
		}
		if err != nil {
			return nil, [sha256.Size]byte{}, nil, err
		}
		if version < 1 {
			return nil, [sha256.Size]byte{}, nil, agentapp.ErrCorrupt
		}
		type requirement struct {
			Code        string `json:"code"`
			Title       string `json:"title"`
			Disposition string `json:"disposition"`
			Reason      string `json:"reason,omitempty"`
		}
		requirements := make([]requirement, 0)
		rows, err := tx.Query(ctx, `SELECT requirement_code,title,disposition,reason FROM spyglass.baseline_requirements
			WHERE account_id=$1 AND assessment_id=$2 ORDER BY requirement_code,id`, accountID, id)
		if err != nil {
			return nil, [sha256.Size]byte{}, nil, err
		}
		for rows.Next() {
			var item requirement
			if err := rows.Scan(&item.Code, &item.Title, &item.Disposition, &item.Reason); err != nil {
				rows.Close()
				return nil, [sha256.Size]byte{}, nil, err
			}
			requirements = append(requirements, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, [sha256.Size]byte{}, nil, err
		}
		content := struct {
			CatalogVersion     string        `json:"catalog_version"`
			ScopePolicyVersion string        `json:"scope_policy_version"`
			State              string        `json:"state"`
			Requirements       []requirement `json:"requirements"`
		}{catalogVersion, scopePolicyVersion, state, requirements}
		if err := add("baseline_assessment", string(id), uint64(version), content); err != nil {
			return nil, [sha256.Size]byte{}, nil, err
		}
	}
	raw, err := json.Marshal(envelope)
	if err != nil || len(raw) > agentapp.MaximumContextBytes {
		return nil, [sha256.Size]byte{}, nil, agentapp.ErrConstraint
	}
	digest := sha256.Sum256(raw)
	references := make([]agentapp.ContextReference, len(envelope.Items))
	for index, item := range envelope.Items {
		decoded, err := hex.DecodeString(item.Digest)
		if err != nil || len(decoded) != sha256.Size {
			return nil, [sha256.Size]byte{}, nil, agentapp.ErrCorrupt
		}
		references[index] = agentapp.ContextReference{Kind: item.Kind, ID: item.ID, Version: item.Version}
		copy(references[index].Digest[:], decoded)
	}
	return raw, digest, references, nil
}

func contextSelectionMatches(references []agentapp.ContextReference, selection agentapp.ContextSelection) bool {
	expected := make([]string, 0, len(selection.WorkItemIDs)+len(selection.KnowledgeFactIDs)+len(selection.KnowledgeDocumentIDs)+len(selection.BaselineAssessmentIDs))
	for _, id := range selection.WorkItemIDs {
		expected = append(expected, "work_item:"+string(id))
	}
	for _, id := range selection.KnowledgeFactIDs {
		expected = append(expected, "knowledge_fact:"+string(id))
	}
	for _, id := range selection.KnowledgeDocumentIDs {
		expected = append(expected, "knowledge_document:"+string(id))
	}
	for _, id := range selection.BaselineAssessmentIDs {
		expected = append(expected, "baseline_assessment:"+string(id))
	}
	if len(expected) != len(references) {
		return false
	}
	for index, reference := range references {
		if expected[index] != reference.Kind+":"+reference.ID {
			return false
		}
	}
	return true
}

func decodeAgentContext(raw, storedDigest []byte, expectedCount int) ([]agentapp.ContextReference, [sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if len(raw) < 1 || len(raw) > agentapp.MaximumContextBytes || len(storedDigest) != sha256.Size || expectedCount < 0 || expectedCount > agentapp.MaximumContextItems {
		return nil, digest, agentapp.ErrCorrupt
	}
	digest = sha256.Sum256(raw)
	if !bytes.Equal(digest[:], storedDigest) {
		return nil, [sha256.Size]byte{}, agentapp.ErrCorrupt
	}
	var envelope frozenContextEnvelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || envelope.SchemaVersion != 1 || len(envelope.Items) != expectedCount {
		return nil, [sha256.Size]byte{}, agentapp.ErrCorrupt
	}
	references := make([]agentapp.ContextReference, len(envelope.Items))
	for index, item := range envelope.Items {
		decoded, err := hex.DecodeString(item.Digest)
		contentDigest := sha256.Sum256(item.Content)
		if err != nil || len(decoded) != sha256.Size || !bytes.Equal(decoded, contentDigest[:]) || ids.Validate(item.ID) != nil || item.Version == 0 ||
			!slices.Contains([]string{"work_item", "knowledge_fact", "knowledge_document", "baseline_assessment"}, item.Kind) || !json.Valid(item.Content) {
			return nil, [sha256.Size]byte{}, agentapp.ErrCorrupt
		}
		references[index] = agentapp.ContextReference{Kind: item.Kind, ID: item.ID, Version: item.Version, Digest: contentDigest}
	}
	return references, digest, nil
}

func nullableRunID(value ids.RunID) any {
	if value == "" {
		return nil
	}
	return value
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

func scanConversation(row interface{ Scan(...any) error }) (agentapp.Conversation, error) {
	var item agentapp.Conversation
	var count int64
	if err := row.Scan(&item.AccountID, &item.ID, &item.BoardroomID, &item.Subject, &item.State, &count, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return agentapp.Conversation{}, err
	}
	if ids.Validate(string(item.AccountID)) != nil || ids.Validate(string(item.ID)) != nil || ids.Validate(string(item.BoardroomID)) != nil || ids.Validate(string(item.CreatedBy)) != nil ||
		utf8.RuneCountInString(item.Subject) < 2 || utf8.RuneCountInString(item.Subject) > 240 || (item.State != "open" && item.State != "closed") || count < 0 || item.CreatedAt.IsZero() || item.UpdatedAt.Before(item.CreatedAt) {
		return agentapp.Conversation{}, agentapp.ErrCorrupt
	}
	item.MessageCount = uint64(count)
	return item, nil
}

func scanAgentMessage(row interface{ Scan(...any) error }) (agentapp.Message, error) {
	var item agentapp.Message
	var sequence int64
	var createdBy, runID, invocationID, personaVersionID *string
	var rawResult []byte
	if err := row.Scan(&item.ID, &item.ConversationID, &sequence, &item.Role, &item.Body, &createdBy, &runID, &invocationID, &personaVersionID, &rawResult, &item.CreatedAt); err != nil {
		return agentapp.Message{}, err
	}
	if ids.Validate(string(item.ID)) != nil || ids.Validate(string(item.ConversationID)) != nil || sequence < 1 || utf8.RuneCountInString(item.Body) < 1 || utf8.RuneCountInString(item.Body) > 65536 || item.CreatedAt.IsZero() {
		return agentapp.Message{}, agentapp.ErrCorrupt
	}
	item.Sequence = uint64(sequence)
	switch item.Role {
	case agentapp.MessageRoleUser:
		if createdBy == nil || ids.Validate(*createdBy) != nil || runID != nil || invocationID != nil || personaVersionID != nil || rawResult != nil {
			return agentapp.Message{}, agentapp.ErrCorrupt
		}
		item.CreatedBy = ids.UserID(*createdBy)
	case agentapp.MessageRolePersona:
		if createdBy != nil || runID == nil || invocationID == nil || personaVersionID == nil || ids.Validate(*runID) != nil || ids.Validate(*invocationID) != nil || ids.Validate(*personaVersionID) != nil || len(rawResult) == 0 {
			return agentapp.Message{}, agentapp.ErrCorrupt
		}
		var decoded agentdomain.ResultEnvelope
		if json.Unmarshal(rawResult, &decoded) != nil {
			return agentapp.Message{}, agentapp.ErrCorrupt
		}
		validated, err := agentdomain.ValidateResult(decoded)
		if err != nil || item.Body != validated.Contribution {
			return agentapp.Message{}, agentapp.ErrCorrupt
		}
		item.RunID, item.InvocationID, item.PersonaVersionID = ids.RunID(*runID), ids.AgentInvocationID(*invocationID), ids.PersonaVersionID(*personaVersionID)
		item.Result = &validated
	default:
		return agentapp.Message{}, agentapp.ErrCorrupt
	}
	return item, nil
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
	var digest, contextPayload, contextDigest []byte
	var contextItemCount int
	var contextSequence int64
	plan.AccountID, plan.RunID = accountID, runID
	err := tx.QueryRow(ctx, `SELECT r.boardroom_id,r.conversation_id,r.state,r.entitlement_version,r.policy_version,r.plan_digest,r.created_by,r.created_at,
		c.subject,e.context_sequence,u.body,r.context_payload,r.context_digest,r.context_item_count
		FROM spyglass.agent_runs r JOIN spyglass.agent_conversations c ON c.account_id=r.account_id AND c.id=r.conversation_id
		JOIN spyglass.agent_invocations i ON i.account_id=r.account_id AND i.run_id=r.id AND i.turn=1
		JOIN spyglass.agent_invocation_execution_plans e ON e.account_id=i.account_id AND e.invocation_id=i.id
		JOIN spyglass.agent_user_messages u ON u.account_id=r.account_id AND u.conversation_id=r.conversation_id AND u.sequence=e.context_sequence
		WHERE r.account_id=$1 AND r.id=$2`, accountID, runID).Scan(&plan.BoardroomID, &plan.ConversationID, &state,
		&plan.EntitlementVersion, &plan.PolicyVersion, &digest, &plan.CreatedBy, &plan.CreatedAt, &subject, &contextSequence, &prompt,
		&contextPayload, &contextDigest, &contextItemCount)
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
	contextReferences, validatedContextDigest, err := decodeAgentContext(contextPayload, contextDigest, contextItemCount)
	if err != nil {
		return agentapp.Run{}, false, err
	}
	rows, err := tx.Query(ctx, `SELECT t.turn,t.persona_id,t.persona_version_id,t.persona_digest,i.id,i.status,i.failure_code,i.started_at,i.completed_at,
		CASE WHEN i.status='succeeded' THEN i.input_tokens END,CASE WHEN i.status='succeeded' THEN i.output_tokens END,
		CASE WHEN i.status='succeeded' THEN i.total_tokens END,CASE WHEN i.status='succeeded' THEN i.cost_micros END
		FROM spyglass.agent_run_plan_turns t JOIN spyglass.agent_invocations i
		ON i.account_id=t.account_id AND i.run_id=t.run_id AND i.turn=t.turn
		WHERE t.account_id=$1 AND t.run_id=$2 ORDER BY t.turn`, accountID, runID)
	if err != nil {
		return agentapp.Run{}, false, err
	}
	defer rows.Close()
	var invocations []ids.AgentInvocationID
	var invocationViews []agentapp.RunInvocation
	for rows.Next() {
		var turn agentdomain.PlannedTurn
		var turnNumber int
		var rawDigest []byte
		var invocation agentapp.RunInvocation
		var failureCode *string
		var inputTokens, outputTokens, totalTokens, costMicros *int64
		if err := rows.Scan(&turnNumber, &turn.PersonaID, &turn.PersonaVersionID, &rawDigest, &invocation.ID, &invocation.Status, &failureCode, &invocation.StartedAt, &invocation.CompletedAt,
			&inputTokens, &outputTokens, &totalTokens, &costMicros); err != nil || len(rawDigest) != len(turn.PersonaDigest) {
			return agentapp.Run{}, false, agentapp.ErrCorrupt
		}
		if turnNumber < 1 || !slices.Contains([]string{"queued", "running", "succeeded", "failed", "canceled"}, invocation.Status) ||
			((invocation.Status == "failed" || invocation.Status == "canceled") != (failureCode != nil)) ||
			((invocation.Status == "queued") && (invocation.StartedAt != nil || invocation.CompletedAt != nil)) ||
			((invocation.Status == "running") && (invocation.StartedAt == nil || invocation.CompletedAt != nil)) ||
			(slices.Contains([]string{"succeeded", "failed", "canceled"}, invocation.Status) && invocation.CompletedAt == nil) ||
			(invocation.Status == "succeeded") != (inputTokens != nil && outputTokens != nil && totalTokens != nil && costMicros != nil) {
			return agentapp.Run{}, false, agentapp.ErrCorrupt
		}
		if invocation.Status == "succeeded" {
			if *inputTokens < 0 || *outputTokens < 0 || *totalTokens != *inputTokens+*outputTokens || *costMicros < 0 {
				return agentapp.Run{}, false, agentapp.ErrCorrupt
			}
			invocation.Usage = &agentapp.RunUsage{InputTokens: *inputTokens, OutputTokens: *outputTokens, TotalTokens: *totalTokens, CostMicros: *costMicros}
		}
		if failureCode != nil {
			invocation.FailureCode = *failureCode
		}
		turn.Turn = uint32(turnNumber)
		invocation.Turn = uint32(turnNumber)
		invocation.PersonaVersionID = turn.PersonaVersionID
		copy(turn.PersonaDigest[:], rawDigest)
		plan.Turns = append(plan.Turns, turn)
		invocations = append(invocations, invocation.ID)
		invocationViews = append(invocationViews, invocation)
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
	resolutionRows, err := tx.Query(ctx, `SELECT id,run_id,action,note,actor_user_id,retry_run_id,created_at
		FROM spyglass.agent_run_resolutions WHERE account_id=$1 AND run_id=$2 ORDER BY created_at,id`, accountID, runID)
	if err != nil {
		return agentapp.Run{}, false, err
	}
	defer resolutionRows.Close()
	var resolutions []agentapp.RunResolution
	for resolutionRows.Next() {
		resolution, err := scanRunResolution(resolutionRows)
		if err != nil {
			return agentapp.Run{}, false, err
		}
		resolutions = append(resolutions, resolution)
	}
	if err := resolutionRows.Err(); err != nil {
		return agentapp.Run{}, false, err
	}
	if len(resolutions) > 1 {
		return agentapp.Run{}, false, agentapp.ErrCorrupt
	}
	return agentapp.Run{Plan: validated, State: state, Subject: subject, Prompt: prompt, UserMessageID: messageID, InvocationIDs: invocations, Invocations: invocationViews, Resolutions: resolutions, Context: contextReferences, ContextDigest: validatedContextDigest}, true, nil
}

func loadRunResolution(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, resolutionID ids.RunResolutionID) (agentapp.RunResolution, bool, error) {
	resolution, err := scanRunResolution(tx.QueryRow(ctx, `SELECT id,run_id,action,note,actor_user_id,retry_run_id,created_at
		FROM spyglass.agent_run_resolutions WHERE account_id=$1 AND id=$2`, accountID, resolutionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return agentapp.RunResolution{}, false, nil
	}
	return resolution, err == nil, err
}

func scanRunResolution(row interface{ Scan(...any) error }) (agentapp.RunResolution, error) {
	var item agentapp.RunResolution
	var retryRunID *string
	if err := row.Scan(&item.ID, &item.RunID, &item.Action, &item.Note, &item.ActorID, &retryRunID, &item.CreatedAt); err != nil {
		return agentapp.RunResolution{}, err
	}
	if ids.Validate(string(item.ID)) != nil || ids.Validate(string(item.RunID)) != nil || ids.Validate(string(item.ActorID)) != nil || item.CreatedAt.IsZero() ||
		utf8.RuneCountInString(item.Note) < 3 || utf8.RuneCountInString(item.Note) > 1000 ||
		(item.Action != agentapp.RunResolutionRetryFailed && item.Action != agentapp.RunResolutionAcceptFailed) ||
		(item.Action == agentapp.RunResolutionRetryFailed) != (retryRunID != nil) {
		return agentapp.RunResolution{}, agentapp.ErrCorrupt
	}
	if retryRunID != nil {
		if ids.Validate(*retryRunID) != nil || *retryRunID == string(item.RunID) {
			return agentapp.RunResolution{}, agentapp.ErrCorrupt
		}
		item.RetryRunID = ids.RunID(*retryRunID)
	}
	return item, nil
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
