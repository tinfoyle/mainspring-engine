package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
	toolbroker "github.com/tinfoyle/mainspring-engine/internal/tools"
)

// RegisterTools installs read-only mailbox operations into the capability-
// enforcing broker. Sending is intentionally excluded: agents may only propose
// email.send actions, which the approval service binds to the action ledger.
func RegisterTools(broker *toolbroker.Broker, service *Service) error {
	if broker == nil || service == nil {
		return errors.New("email broker and service are required")
	}
	return broker.Register(domain.CapabilityEmailRead, service.readTool)
}

func (s *Service) readTool(ctx context.Context, call toolbroker.AuthorizedCall) (json.RawMessage, error) {
	var input struct {
		UID   uint32 `json:"uid"`
		Limit int    `json:"limit"`
	}
	if len(call.Input) > 0 {
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return nil, fmt.Errorf("decode email read input: %w", err)
		}
	}
	var value any
	var err error
	if input.UID > 0 {
		value, err = s.Message(ctx, input.UID)
	} else {
		value, err = s.Inbox(ctx, input.Limit)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func (s *Service) sendTool(ctx context.Context, call toolbroker.AuthorizedCall) (json.RawMessage, error) {
	var input struct {
		IdempotencyKey string   `json:"idempotency_key"`
		To             []string `json:"to"`
		CC             []string `json:"cc"`
		Subject        string   `json:"subject"`
		Body           string   `json:"body"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return nil, fmt.Errorf("decode email send input: %w", err)
	}
	runID, err := domain.ParseRunID(call.Claims.RunID)
	if err != nil {
		return nil, err
	}
	result, err := s.Send(ctx, input.IdempotencyKey, OutgoingMessage{To: input.To, CC: input.CC, Subject: input.Subject, Body: input.Body}, "persona", call.Claims.PersonaID, &runID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
