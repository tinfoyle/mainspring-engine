package boardroom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

func syncBoardroomTurnLimit(ctx context.Context, tx pgx.Tx, boardroomID domain.BoardroomID) error {
	var enabled int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM personas WHERE boardroom_id=$1 AND enabled`, boardroomID.String()).Scan(&enabled); err != nil {
		return err
	}
	if enabled == 0 {
		return errors.New("a boardroom must retain at least one active agent")
	}
	if _, err := tx.Exec(ctx, `UPDATE boardrooms SET max_turns=$2, updated_at=now() WHERE id=$1`, boardroomID.String(), enabled); err != nil {
		return fmt.Errorf("synchronize boardroom turn limit: %w", err)
	}
	return nil
}

func reorderAgent(ctx context.Context, tx pgx.Tx, boardroomID domain.BoardroomID, target domain.PersonaID, desired int) error {
	rows, err := tx.Query(ctx, `SELECT id::text FROM personas WHERE boardroom_id=$1 ORDER BY position FOR UPDATE`, boardroomID.String())
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if id != target.String() {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if desired < 1 {
		desired = 1
	}
	if desired > len(ids)+1 {
		desired = len(ids) + 1
	}
	ids = append(ids, "")
	copy(ids[desired:], ids[desired-1:])
	ids[desired-1] = target.String()
	if _, err := tx.Exec(ctx, `UPDATE personas SET position=position+10000 WHERE boardroom_id=$1`, boardroomID.String()); err != nil {
		return err
	}
	for index, id := range ids {
		if _, err := tx.Exec(ctx, `UPDATE personas SET position=$2 WHERE id=$1`, id, index+1); err != nil {
			return err
		}
	}
	return nil
}

func replaceAgentGrants(ctx context.Context, tx pgx.Tx, personaID string, grants []domain.ToolGrant) error {
	if _, err := tx.Exec(ctx, `DELETE FROM persona_tool_grants WHERE persona_id=$1`, personaID); err != nil {
		return err
	}
	for _, grant := range grants {
		conditions, err := json.Marshal(grant.Conditions)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO persona_tool_grants (persona_id,capability,conditions) VALUES ($1,$2,$3)`, personaID, grant.Capability, conditions); err != nil {
			return err
		}
	}
	return nil
}
