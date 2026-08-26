package admissionapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/aitokenledger"
	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type testAITokens struct {
	reserve aitokenledger.ReserveCommand
	closeID string
	usage   aitokenledger.Usage
}

func (s *testAITokens) Reserve(_ context.Context, command aitokenledger.ReserveCommand) (aitokens.Reservation, aitokens.Balance, error) {
	s.reserve = command
	return aitokens.Reservation{ID: "51000000-0000-4000-8000-000000000005", AccountID: command.AccountID, RequestID: command.RequestID, State: aitokens.ReservationActive,
		Rate: catalog.AIComplexityRate{Code: "balanced_v2", Version: 2, Complexity: command.Complexity, InputPerThousand: 4, CachedInputPerThousand: 1, OutputPerThousand: 16,
			MinimumCharge: 20, MaximumReservation: 2500, EstimatedMinimum: 20, EstimatedMaximum: 1000, InternalProvider: "kimi", InternalModel: "kimi-balanced", InternalAdapterVersion: 3, InternalModelPolicyVersion: 4}}, aitokens.Balance{}, nil
}

func (s *testAITokens) Close(_ context.Context, _ ids.AccountID, requestID string, usage aitokenledger.Usage) (aitokens.Reservation, aitokens.Balance, error) {
	s.closeID, s.usage = requestID, usage
	return aitokens.Reservation{RequestID: requestID, State: aitokens.ReservationSettled}, aitokens.Balance{}, nil
}

func TestAgentTokenEndpointsReserveAndSettleThroughPrivateBoundary(t *testing.T) {
	cellID := ids.CellID("cell-us-east-01")
	tokens := &testAITokens{}
	server, err := New(&testUsage{}, map[ids.CellID]Verifier{cellID: testVerifier{}}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithAITokens(tokens))
	if err != nil {
		t.Fatal(err)
	}
	reserve := httptest.NewRequest(http.MethodPost, "/internal/v1/agents/ai-tokens:reserve", bytes.NewBufferString(`{"cell_id":"cell-us-east-01","account_id":"`+testAccount+`","user_id":"`+testActor+`","invocation_id":"`+testOperation+`","complexity":"balanced"}`))
	reserve.Header.Set("Content-Type", "application/json")
	reserveResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(reserveResponse, reserve)
	if reserveResponse.Code != http.StatusOK || tokens.reserve.RequestID != testOperation || tokens.reserve.Actor.UserID != testActor || tokens.reserve.Complexity != catalog.AIComplexityBalanced ||
		!bytes.Contains(reserveResponse.Body.Bytes(), []byte(`"internal_model":"kimi-balanced"`)) {
		t.Fatalf("status=%d command=%+v body=%s", reserveResponse.Code, tokens.reserve, reserveResponse.Body.String())
	}

	closeRequest := httptest.NewRequest(http.MethodPost, "/internal/v1/agents/ai-tokens:close", bytes.NewBufferString(`{"cell_id":"cell-us-east-01","account_id":"`+testAccount+`","invocation_id":"`+testOperation+`","usage":{"provider_started":true,"input_tokens":100,"cached_input_tokens":20,"output_tokens":30,"tool_invocations":2}}`))
	closeRequest.Header.Set("Content-Type", "application/json")
	closeResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(closeResponse, closeRequest)
	if closeResponse.Code != http.StatusOK || tokens.closeID != testOperation || !tokens.usage.ProviderStarted || tokens.usage.CachedInputTokens != 20 || tokens.usage.ToolInvocations != 2 {
		t.Fatalf("status=%d close=%s usage=%+v body=%s", closeResponse.Code, tokens.closeID, tokens.usage, closeResponse.Body.String())
	}
}

func TestAgentTokenEndpointRejectsUnknownJSONWithoutDoubleResponse(t *testing.T) {
	cellID := ids.CellID("cell-us-east-01")
	server, err := New(&testUsage{}, map[ids.CellID]Verifier{cellID: testVerifier{}}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithAITokens(&testAITokens{}))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/agents/ai-tokens:reserve", bytes.NewBufferString(`{"unexpected":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || bytes.Count(response.Body.Bytes(), []byte(`"status":400`)) != 1 || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"invalid_ai_token_admission"`)) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
