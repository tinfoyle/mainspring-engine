package boardroom

import (
	"context"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

func (s *Store) AgentVersions(ctx context.Context, id domain.PersonaID) ([]AgentVersion, error) {
	rows, err := s.pool.Query(ctx, `SELECT version,name,role,created_at FROM persona_versions WHERE persona_id=$1 ORDER BY version DESC LIMIT 50`, id.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []AgentVersion
	for rows.Next() {
		var item AgentVersion
		if err := rows.Scan(&item.Version, &item.Name, &item.Role, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) DuplicateAgent(ctx context.Context, id domain.PersonaID) (Persona, error) {
	item, err := s.GetAgent(ctx, id)
	if err != nil {
		return Persona{}, err
	}
	return s.CreateAgent(ctx, AgentInput{BoardroomID: item.BoardroomID, Name: item.Name + " copy", Role: item.Role,
		Description: item.Description, SystemInstructions: item.SystemInstructions, Enabled: false, Grants: item.Grants, Settings: item.Settings})
}
