// Package agentusage defines the private cross-boundary contract that freezes
// and settles customer AI Token usage for one Agent invocation.
package agentusage

import (
	"context"

	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type ReserveCommand struct {
	CellID       ids.CellID
	AccountID    ids.AccountID
	UserID       ids.UserID
	InvocationID string
	Complexity   catalog.AIComplexity
}

type Admission struct {
	ReservationID ids.AITokenReservationID
	RequestID     string
	Rate          catalog.AIComplexityRate
	State         aitokens.ReservationState
}

func (admission Admission) ModelTargets() []string {
	return append([]string{admission.Rate.InternalModel}, admission.Rate.InternalFallbackModels...)
}

type Usage struct {
	ProviderStarted   bool  `json:"provider_started"`
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	ToolInvocations   int64 `json:"tool_invocations"`
}

type CloseCommand struct {
	CellID       ids.CellID
	AccountID    ids.AccountID
	InvocationID string
	Usage        Usage
}

type Broker interface {
	ReserveAgentTokens(context.Context, ReserveCommand) (Admission, error)
	CloseAgentTokens(context.Context, CloseCommand) error
}
