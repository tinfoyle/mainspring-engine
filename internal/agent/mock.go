package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

type MockProvider struct{}

func (MockProvider) Invoke(ctx context.Context, invocation Invocation) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	question := "the current request"
	for _, message := range invocation.Conversation {
		if message.Role == domain.MessageUser && strings.TrimSpace(message.Body) != "" {
			question = message.Body
			break
		}
	}
	body := fmt.Sprintf(
		"As %s, I reviewed %q. This is a development-mode response; the boardroom workflow, persona boundary, persistence, and event delivery are operating correctly.",
		invocation.PersonaRole,
		question,
	)
	return Result{Body: body, Provider: "mock", Model: "deterministic-development-provider"}, nil
}

func (MockProvider) Cancel(context.Context, domain.InvocationID) error { return nil }
func (MockProvider) Name() string                                      { return "mock" }
func (MockProvider) Capabilities() Capabilities {
	return Capabilities{StructuredOutput: true}
}
