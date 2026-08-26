package admissionhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/agentusage"
	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestAgentTokenBrokerFreezesPrivateRateAndSendsNoBrowserAuthority(t *testing.T) {
	invocationID := admissionOperation
	calls := 0
	client, err := New("https://admission.test", false, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(request.Body)
		if request.URL.Path != "/internal/v1/agents/ai-tokens:reserve" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" ||
			!strings.Contains(string(body), `"cell_id":"cell-us-east-01"`) || !strings.Contains(string(body), `"complexity":"balanced"`) {
			t.Fatalf("path=%q authorization=%q cookie=%q body=%s", request.URL.Path, request.Header.Get("Authorization"), request.Header.Get("Cookie"), body)
		}
		response := `{"reservation_id":"50000000-0000-4000-8000-000000000005","request_id":"` + invocationID + `","state":"active","rate":{"code":"balanced_v2","version":2,"complexity":"balanced","input_per_thousand":4,"cached_input_per_thousand":1,"output_per_thousand":16,"tool_invocation":25,"minimum_charge":20,"maximum_reservation":2500,"estimated_minimum":20,"estimated_maximum":1000,"internal_provider":"kimi","internal_model":"kimi-balanced","internal_fallback_models":["glm-balanced"],"internal_reasoning_effort":"medium","internal_adapter_version":3,"internal_model_policy_version":4}}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	admission, err := client.ReserveAgentTokens(context.Background(), agentusage.ReserveCommand{CellID: "cell-us-east-01", AccountID: admissionAccount, UserID: admissionUser, InvocationID: invocationID, Complexity: catalog.AIComplexityBalanced})
	if err != nil || calls != 1 || admission.State != aitokens.ReservationActive || admission.Rate.InternalProvider != "kimi" || len(admission.ModelTargets()) != 2 || admission.ModelTargets()[1] != "glm-balanced" {
		t.Fatalf("admission=%+v calls=%d err=%v", admission, calls, err)
	}
}

func TestAgentTokenBrokerSettlesTrustedUsageAndRejectsMalformedCell(t *testing.T) {
	calls := 0
	client, _ := New("https://admission.test", false, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(request.Body)
		if request.URL.Path != "/internal/v1/agents/ai-tokens:close" || !strings.Contains(string(body), `"provider_started":true`) ||
			!strings.Contains(string(body), `"cached_input_tokens":20`) || !strings.Contains(string(body), `"tool_invocations":2`) {
			t.Fatalf("path=%q body=%s", request.URL.Path, body)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"request_id":"` + admissionOperation + `","state":"closed"}`))}, nil
	}))
	command := agentusage.CloseCommand{CellID: "cell-us-east-01", AccountID: admissionAccount, InvocationID: admissionOperation,
		Usage: agentusage.Usage{ProviderStarted: true, InputTokens: 100, CachedInputTokens: 20, OutputTokens: 30, ToolInvocations: 2}}
	if err := client.CloseAgentTokens(context.Background(), command); err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	command.CellID = ids.CellID("not a cell")
	if err := client.CloseAgentTokens(context.Background(), command); !errors.Is(err, aitokens.ErrInvalidReservation) || calls != 1 {
		t.Fatalf("invalid cell calls=%d err=%v", calls, err)
	}
}
