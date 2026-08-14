package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
	toolbroker "github.com/tinfoyle/mainspring-engine/internal/tools"
)

type Service struct {
	store     *Store
	connector Connector
	ledger    *toolbroker.ActionLedger
}

func NewService(store *Store, connector Connector, ledger *toolbroker.ActionLedger) *Service {
	return &Service{store: store, connector: connector, ledger: ledger}
}

func (s *Service) Configure(ctx context.Context, input SettingsInput, userID string) (Integration, error) {
	integration, err := input.integration()
	if err != nil {
		return Integration{}, err
	}
	if err := s.connector.Test(ctx, integration); err != nil {
		return Integration{}, err
	}
	return s.store.SavePrimary(ctx, integration, userID)
}

func (s *Service) Integration(ctx context.Context) (Integration, error) { return s.store.Primary(ctx) }

func (s *Service) Inbox(ctx context.Context, limit int) ([]InboxMessage, error) {
	integration, err := s.store.Primary(ctx)
	if err != nil {
		return nil, err
	}
	messages, err := s.connector.Inbox(ctx, integration, limit)
	if err != nil {
		s.store.MarkError(ctx, integration.ID, err)
	}
	return messages, err
}

func (s *Service) Message(ctx context.Context, uid uint32) (Message, error) {
	integration, err := s.store.Primary(ctx)
	if err != nil {
		return Message{}, err
	}
	message, err := s.connector.Message(ctx, integration, uid)
	if err != nil {
		s.store.MarkError(ctx, integration.ID, err)
	}
	return message, err
}

func (s *Service) Folders(ctx context.Context) ([]string, error) {
	integration, err := s.store.Primary(ctx)
	if err != nil {
		return nil, err
	}
	connector, ok := s.connector.(EvidenceConnector)
	if !ok {
		return []string{"INBOX"}, nil
	}
	folders, err := connector.Folders(ctx, integration)
	if err != nil {
		s.store.MarkError(ctx, integration.ID, err)
	}
	return folders, err
}

func (s *Service) Evidence(ctx context.Context, scope EvidenceScope) ([]EvidenceMessage, error) {
	integration, err := s.store.Primary(ctx)
	if err != nil {
		return nil, err
	}
	if connector, ok := s.connector.(EvidenceConnector); ok {
		items, err := connector.Evidence(ctx, integration, scope)
		if err != nil {
			s.store.MarkError(ctx, integration.ID, err)
		}
		return items, err
	}
	messages, err := s.connector.Inbox(ctx, integration, scope.MaxItems)
	if err != nil {
		return nil, err
	}
	result := make([]EvidenceMessage, 0, len(messages))
	for _, item := range messages {
		if scope.Since != nil && item.Date.Before(*scope.Since) || scope.Until != nil && item.Date.After(scope.Until.AddDate(0, 0, 1)) {
			continue
		}
		message, err := s.connector.Message(ctx, integration, item.UID)
		if err == nil {
			result = append(result, EvidenceMessage{Message: message, Folder: "INBOX"})
		}
	}
	return result, nil
}

func (s *Service) Send(ctx context.Context, key string, message OutgoingMessage, actorType, actorID string, runID *domain.RunID) (SendResult, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 200 {
		return SendResult{}, errors.New("a valid idempotency key is required")
	}
	if actorType != "user" && actorType != "persona" && actorType != "system" {
		return SendResult{}, errors.New("invalid email actor type")
	}
	integration, err := s.store.Primary(ctx)
	if err != nil {
		return SendResult{}, err
	}
	action, err := s.ledger.Prepare(ctx, runID, key, "email.send", message)
	if err != nil {
		return SendResult{}, err
	}
	if action.Status == "succeeded" {
		var result SendResult
		if err := json.Unmarshal(action.ResponsePayload, &result); err != nil {
			return SendResult{}, fmt.Errorf("decode prior email result: %w", err)
		}
		return result, nil
	}
	if err := s.store.PrepareOutbox(ctx, integration.ID, key, message, actorType, actorID); err != nil {
		return SendResult{}, err
	}
	action, execute, err := s.ledger.BeginExecution(ctx, action.ID, false)
	if err != nil {
		return SendResult{}, err
	}
	if !execute {
		return SendResult{}, fmt.Errorf("email action is already %s", action.Status)
	}
	s.store.MarkOutbox(ctx, key, "sending", "", "", nil)
	result, sendErr := s.connector.Send(ctx, integration, message)
	if sendErr != nil {
		var deliveryErr *DeliveryError
		if errors.As(sendErr, &deliveryErr) && deliveryErr.Unknown {
			_ = s.ledger.MarkUnknown(ctx, action.ID, sendErr)
			s.store.MarkOutbox(ctx, key, "unknown", "", sendErr.Error(), nil)
			return SendResult{}, fmt.Errorf("email delivery outcome is unknown and needs review: %w", sendErr)
		}
		_ = s.ledger.MarkFailed(ctx, action.ID, sendErr)
		s.store.MarkOutbox(ctx, key, "failed", "", sendErr.Error(), nil)
		return SendResult{}, sendErr
	}
	if err := s.ledger.MarkSucceeded(ctx, action.ID, result, result.MessageID); err != nil {
		return SendResult{}, err
	}
	s.store.MarkOutbox(ctx, key, "sent", result.MessageID, "", &result.SentAt)
	return result, nil
}
