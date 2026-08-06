package boardroom

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/agent"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	toolbroker "github.com/tinfoyle/mainspring-engine/internal/tools"
)

type Service struct {
	logger   *slog.Logger
	store    *Store
	provider agent.Provider
	tenantID domain.TenantID
	timeout  time.Duration
	issuer   *toolbroker.TokenIssuer
}

func NewService(logger *slog.Logger, store *Store, provider agent.Provider, tenantID domain.TenantID, timeout time.Duration, issuer *toolbroker.TokenIssuer) *Service {
	return &Service{logger: logger, store: store, provider: provider, tenantID: tenantID, timeout: timeout, issuer: issuer}
}

func (s *Service) CreateScheduledRun(ctx context.Context, boardroomID domain.BoardroomID, workflowID, title, prompt, scheduleID string) (Run, error) {
	return s.store.CreateScheduledRun(ctx, boardroomID, workflowID, title, prompt, scheduleID)
}

// ExecuteRun is application-owned orchestration for the local MVP path. Temporal
// invokes the same bounded-turn behavior through Activities in production mode.
func (s *Service) ExecuteRun(ctx context.Context, runID domain.RunID) error {
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == domain.RunCompleted || run.Status == domain.RunCanceled {
		return nil
	}
	if err := s.store.SetRunStatus(ctx, runID, domain.RunRunning, ""); err != nil {
		return err
	}

	personas, err := s.store.Personas(ctx, run.BoardroomID)
	if err != nil {
		return s.fail(ctx, runID, err)
	}
	if len(personas) == 0 {
		return s.fail(ctx, runID, fmt.Errorf("boardroom has no enabled personas"))
	}

	turnLimit := run.MaxTurns
	if turnLimit > len(personas) {
		turnLimit = len(personas)
	}
	existing, err := s.store.Messages(ctx, runID)
	if err != nil {
		return s.fail(ctx, runID, err)
	}
	completedTurns := 0
	for _, message := range existing {
		if message.Role == "agent" {
			completedTurns++
		}
	}
	if completedTurns >= turnLimit {
		return s.store.SetRunStatus(ctx, runID, domain.RunCompleted, "")
	}

	for index := completedTurns; index < turnLimit; index++ {
		persona := personas[index]
		messages, err := s.store.ConversationMessages(ctx, run.ConversationID)
		if err != nil {
			return s.fail(ctx, runID, err)
		}
		conversation := make([]agent.ConversationMessage, 0, len(messages))
		for _, message := range messages {
			conversation = append(conversation, agent.ConversationMessage{
				Role: message.Role, PersonaName: message.PersonaName, Body: message.Body,
			})
		}

		invocation := agent.Invocation{
			ID:                 domain.NewInvocationID(),
			TenantID:           s.tenantID,
			BoardroomID:        run.BoardroomID,
			RunID:              runID,
			PersonaID:          persona.ID,
			PersonaName:        persona.Name,
			PersonaRole:        persona.Role,
			SystemInstructions: persona.SystemInstructions,
			Conversation:       conversation,
			ToolGrants:         persona.Grants,
			Timeout:            s.timeout,
		}
		if s.issuer != nil {
			expiresAt := time.Now().Add(s.timeout)
			if s.timeout <= 0 {
				expiresAt = time.Now().Add(5 * time.Minute)
			}
			invocation.CapabilityToken, err = s.issuer.Mint(domain.InvocationContext{
				TenantID: s.tenantID, BoardroomID: run.BoardroomID, RunID: runID, PersonaID: persona.ID,
				InvocationID: invocation.ID, Grants: persona.Grants, ExpiresAt: expiresAt,
			})
			if err != nil {
				return s.fail(ctx, runID, fmt.Errorf("mint capability token for %s: %w", persona.Name, err))
			}
		}
		result, err := s.provider.Invoke(ctx, invocation)
		if err != nil {
			return s.fail(ctx, runID, fmt.Errorf("invoke %s: %w", persona.Name, err))
		}
		if _, err := s.store.AppendAgentMessage(ctx, runID, persona, result); err != nil {
			return s.fail(ctx, runID, err)
		}
	}

	return s.store.SetRunStatus(ctx, runID, domain.RunCompleted, "")
}

func (s *Service) fail(ctx context.Context, runID domain.RunID, runErr error) error {
	if err := s.store.SetRunStatus(ctx, runID, domain.RunFailed, runErr.Error()); err != nil {
		s.logger.Error("record run failure", "run_id", runID.String(), "error", err)
	}
	return runErr
}
