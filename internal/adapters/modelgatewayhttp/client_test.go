package modelgatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestExecuteInjectsAuthorizedInvocationAndOperation(t *testing.T) {
	var received modelgateway.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(modelgateway.Result{SchemaVersion: 1, Provider: "openai", Model: "gpt-test", ResponseID: "resp_1", StopReason: "completed", Output: json.RawMessage(`{"answer":"ok"}`), Usage: modelgateway.Usage{}})
	}))
	defer server.Close()
	client, err := New(Config{Origin: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	request := modelgateway.Request{InvocationID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", OperationID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Provider: "openai", Model: "gpt-test", Instructions: "Help.", Messages: []modelgateway.Message{{Role: "user", Content: "Status?"}}, OutputFormat: modelgateway.OutputFormat{Name: "result", Schema: json.RawMessage(`{"type":"object"}`)}, MaximumOutTokens: 100}
	raw, _ := json.Marshal(request)
	call := runnercapability.AuthorizedCall{Grant: runnerbroker.CapabilityGrant{Identity: runnerbroker.Identity{InvocationID: "10000000-0000-4000-8000-000000000001"}, AccountID: ids.AccountID("30000000-0000-4000-8000-000000000003"), Capability: modelgateway.ModelTurnCapability}, OperationID: "20000000-0000-4000-8000-000000000002", Input: raw}
	if _, err := client.Execute(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	if received.InvocationID != call.Grant.Identity.InvocationID || received.OperationID != call.OperationID || received.SchemaVersion != 1 {
		t.Fatalf("identity not injected %#v", received)
	}
}
