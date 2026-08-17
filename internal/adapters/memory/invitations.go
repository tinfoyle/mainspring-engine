package memory

import (
	"context"
	"sync"

	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
)

type InvitationSink struct {
	mu       sync.Mutex
	messages []invitations.Message
}

func (s *InvitationSink) SendInvitation(_ context.Context, message invitations.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	return nil
}
func (s *InvitationSink) Latest() (invitations.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return invitations.Message{}, false
	}
	return s.messages[len(s.messages)-1], true
}
