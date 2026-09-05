package scheduling

import (
	"context"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"time"
)

type HistoryItem struct {
	ID             string    `json:"id"`
	OccurredAt     time.Time `json:"occurred_at"`
	Outcome        string    `json:"outcome"`
	RunID          string    `json:"run_id"`
	ConversationID string    `json:"conversation_id"`
	BoardroomID    string    `json:"boardroom_id"`
	RunState       string    `json:"run_state"`
	EmailState     string    `json:"email_state"`
	EmailError     string    `json:"email_error"`
}
type HistoryPage struct {
	Items []HistoryItem `json:"items"`
}
type HistoryStore interface {
	History(context.Context, ids.AccountID, ids.ScheduleID) (HistoryPage, error)
}

func (s *Service) History(ctx context.Context, q GetQuery) (HistoryPage, error) {
	if _, err := s.Get(ctx, q); err != nil {
		return HistoryPage{}, err
	}
	store, ok := s.store.(HistoryStore)
	if !ok {
		return HistoryPage{}, ErrRepository
	}
	return store.History(ctx, q.AccountID, q.ScheduleID)
}
