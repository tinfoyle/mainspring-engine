package scheduling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

var ErrNotFound = errors.New("schedule not found")

type Spec struct {
	Kind           string `json:"kind"`
	EverySeconds   int64  `json:"every_seconds,omitempty"`
	CronExpression string `json:"cron_expression,omitempty"`
	TimeZone       string `json:"time_zone"`
}

type Schedule struct {
	ID                 string
	BoardroomID        domain.BoardroomID
	BoardroomName      string
	Name               string
	Prompt             string
	TemporalScheduleID string
	Spec               Spec
	Paused             bool
	State              string
	LastError          string
	CreatedAt          time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Create(ctx context.Context, item Schedule) (Schedule, error) {
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	spec, err := json.Marshal(item.Spec)
	if err != nil {
		return Schedule{}, fmt.Errorf("encode schedule spec: %w", err)
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO schedules (id, boardroom_id, name, prompt, temporal_schedule_id, spec, state)
		VALUES ($1, $2, $3, $4, $5, $6, 'creating')
		RETURNING created_at
	`, item.ID, item.BoardroomID.String(), item.Name, item.Prompt, item.TemporalScheduleID, spec).Scan(&item.CreatedAt)
	if err != nil {
		return Schedule{}, fmt.Errorf("create schedule record: %w", err)
	}
	item.State = "creating"
	return item, nil
}

func (s *Store) MarkActive(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE schedules SET state = 'active', last_error = NULL, updated_at = now() WHERE id = $1`, id)
	return err
}

func (s *Store) MarkFailed(ctx context.Context, id string, scheduleErr error) error {
	_, err := s.pool.Exec(ctx, `UPDATE schedules SET state = 'failed', last_error = $2, updated_at = now() WHERE id = $1`, id, scheduleErr.Error())
	return err
}

func (s *Store) SetPaused(ctx context.Context, id string, paused bool) error {
	command, err := s.pool.Exec(ctx, `UPDATE schedules SET paused = $2, updated_at = now() WHERE id = $1`, id, paused)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	command, err := s.pool.Exec(ctx, `DELETE FROM schedules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Get(ctx context.Context, id string) (Schedule, error) {
	var item Schedule
	var boardroomID string
	var spec []byte
	err := s.pool.QueryRow(ctx, `
		SELECT s.id::text, s.boardroom_id::text, b.name, s.name, s.prompt, s.temporal_schedule_id,
		       s.spec, s.paused, s.state, COALESCE(s.last_error, ''), s.created_at
		FROM schedules s JOIN boardrooms b ON b.id = s.boardroom_id
		WHERE s.id = $1
	`, id).Scan(&item.ID, &boardroomID, &item.BoardroomName, &item.Name, &item.Prompt, &item.TemporalScheduleID,
		&spec, &item.Paused, &item.State, &item.LastError, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, ErrNotFound
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("get schedule: %w", err)
	}
	item.BoardroomID, err = domain.ParseBoardroomID(boardroomID)
	if err != nil {
		return Schedule{}, err
	}
	if err := json.Unmarshal(spec, &item.Spec); err != nil {
		return Schedule{}, fmt.Errorf("decode schedule spec: %w", err)
	}
	return item, nil
}

func (s *Store) List(ctx context.Context) ([]Schedule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.id::text, s.boardroom_id::text, b.name, s.name, s.prompt, s.temporal_schedule_id,
		       s.spec, s.paused, s.state, COALESCE(s.last_error, ''), s.created_at
		FROM schedules s JOIN boardrooms b ON b.id = s.boardroom_id
		ORDER BY s.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()
	var result []Schedule
	for rows.Next() {
		var item Schedule
		var boardroomID string
		var spec []byte
		if err := rows.Scan(&item.ID, &boardroomID, &item.BoardroomName, &item.Name, &item.Prompt, &item.TemporalScheduleID,
			&spec, &item.Paused, &item.State, &item.LastError, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan schedule: %w", err)
		}
		item.BoardroomID, err = domain.ParseBoardroomID(boardroomID)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(spec, &item.Spec); err != nil {
			return nil, fmt.Errorf("decode schedule spec: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
