package memory

import (
	"context"
	"sync"

	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
)

type VerificationSink struct {
	mu       sync.Mutex
	messages []registration.VerificationMessage
}

func (s *VerificationSink) SendVerification(_ context.Context, message registration.VerificationMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	return nil
}

func (s *VerificationSink) Latest() (registration.VerificationMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return registration.VerificationMessage{}, false
	}
	return s.messages[len(s.messages)-1], true
}
