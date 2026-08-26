package admissionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"github.com/tinfoyle/spyglass-engine/internal/application/agentusage"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type agentTokenReserveRequest struct {
	CellID       ids.CellID           `json:"cell_id"`
	AccountID    ids.AccountID        `json:"account_id"`
	UserID       ids.UserID           `json:"user_id"`
	InvocationID string               `json:"invocation_id"`
	Complexity   catalog.AIComplexity `json:"complexity"`
}

type agentTokenReserveResponse struct {
	ReservationID ids.AITokenReservationID  `json:"reservation_id"`
	RequestID     string                    `json:"request_id"`
	State         aitokens.ReservationState `json:"state"`
	Rate          catalog.AIComplexityRate  `json:"rate"`
}

func (c *Client) ReserveAgentTokens(ctx context.Context, command agentusage.ReserveCommand) (agentusage.Admission, error) {
	if c == nil || !validAgentTokenReserve(command) {
		return agentusage.Admission{}, aitokens.ErrInvalidReservation
	}
	payload, err := json.Marshal(agentTokenReserveRequest{CellID: command.CellID, AccountID: command.AccountID, UserID: command.UserID, InvocationID: command.InvocationID, Complexity: command.Complexity})
	if err != nil {
		return agentusage.Admission{}, err
	}
	body, err := c.agentTokenRequest(ctx, "/internal/v1/agents/ai-tokens:reserve", payload)
	if err != nil {
		return agentusage.Admission{}, err
	}
	var response agentTokenReserveResponse
	if json.Unmarshal(body, &response) != nil || response.RequestID != command.InvocationID || response.State != aitokens.ReservationActive ||
		ids.Validate(string(response.ReservationID)) != nil || response.Rate.Complexity != command.Complexity || !validFrozenRate(response.Rate) {
		return agentusage.Admission{}, errors.New("AI Token admission returned an invalid receipt")
	}
	return agentusage.Admission{ReservationID: response.ReservationID, RequestID: response.RequestID, State: response.State, Rate: response.Rate}, nil
}

type agentTokenCloseRequest struct {
	CellID       ids.CellID       `json:"cell_id"`
	AccountID    ids.AccountID    `json:"account_id"`
	InvocationID string           `json:"invocation_id"`
	Usage        agentusage.Usage `json:"usage"`
}

func (c *Client) CloseAgentTokens(ctx context.Context, command agentusage.CloseCommand) error {
	if c == nil || !validAgentTokenClose(command) {
		return aitokens.ErrInvalidReservation
	}
	payload, err := json.Marshal(agentTokenCloseRequest{CellID: command.CellID, AccountID: command.AccountID, InvocationID: command.InvocationID, Usage: command.Usage})
	if err != nil {
		return err
	}
	body, err := c.agentTokenRequest(ctx, "/internal/v1/agents/ai-tokens:close", payload)
	if err != nil {
		return err
	}
	var response struct {
		RequestID string `json:"request_id"`
		State     string `json:"state"`
	}
	if json.Unmarshal(body, &response) != nil || response.RequestID != command.InvocationID || response.State != "closed" {
		return errors.New("AI Token settlement returned an invalid receipt")
	}
	return nil
}

func (c *Client) agentTokenRequest(ctx context.Context, path string, payload []byte) ([]byte, error) {
	target := *c.origin
	target.Path = path
	request, err := newRequest(ctx, target.String(), payload)
	if err != nil {
		return nil, err
	}
	response, err := c.client.Do(request)
	if err != nil && response == nil && ctx.Err() == nil {
		c.stats.retryAttempts.Add(1)
		request, requestErr := newRequest(ctx, target.String(), payload)
		if requestErr != nil {
			return nil, requestErr
		}
		response, err = c.client.Do(request)
		if err == nil {
			c.stats.retryRecovered.Add(1)
		}
	}
	if err != nil {
		closeResponse(response)
		c.stats.requestsFailed.Add(1)
		return nil, errors.New("AI Token admission is unavailable")
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
	if readErr != nil || int64(len(body)) > maxResponseBody {
		return nil, errors.New("AI Token admission returned an invalid response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, agentTokenError(body)
	}
	return body, nil
}

func agentTokenError(body []byte) error {
	var problem problemResponse
	if json.Unmarshal(body, &problem) != nil {
		return errors.New("AI Token admission failed")
	}
	switch problem.Code {
	case "ai_tokens_insufficient":
		return aitokens.ErrInsufficient
	case "invalid_ai_token_admission", "invalid_ai_token_settlement":
		return aitokens.ErrInvalidReservation
	case "agent_workload_scope_denied":
		return aitokens.ErrInvalidReservation
	case string(access.DenialUnauthenticated), string(access.DenialMembership), string(access.DenialAccountUnavailable), string(access.DenialPackageNotEntitled), string(access.DenialPackageReadOnly), string(access.DenialCorruptContext):
		return &access.DeniedError{Code: access.DenialCode(problem.Code), Package: catalog.PackageAgents}
	default:
		return errors.New("AI Token admission is unavailable")
	}
}

func validAgentTokenReserve(command agentusage.ReserveCommand) bool {
	return routecontext.ValidCellID(command.CellID) && ids.Validate(string(command.AccountID)) == nil && ids.Validate(string(command.UserID)) == nil && ids.Validate(command.InvocationID) == nil && slices.Contains(catalog.AIComplexities, command.Complexity)
}

func validAgentTokenClose(command agentusage.CloseCommand) bool {
	usage := command.Usage
	return routecontext.ValidCellID(command.CellID) && ids.Validate(string(command.AccountID)) == nil && ids.Validate(command.InvocationID) == nil && usage.InputTokens >= 0 && usage.CachedInputTokens >= 0 && usage.OutputTokens >= 0 && usage.ToolInvocations >= 0 && usage.CachedInputTokens <= usage.InputTokens
}

func validFrozenRate(rate catalog.AIComplexityRate) bool {
	return catalog.ValidateAIComplexityRate(rate) == nil
}

var _ agentusage.Broker = (*Client)(nil)
