package toolrouterhttp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/platform/toolcontext"
)

type signer struct {
	authority toolcontext.Authority
	binding   routecontext.Binding
}

func (s *signer) Issue(authority toolcontext.Authority, binding routecontext.Binding) (string, error) {
	s.authority, s.binding = authority, binding
	return "signed-tool-context", nil
}

type fixedIDs struct{}

func (fixedIDs) New() string { return "60000000-0000-4000-8000-000000000006" }

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestClientSignsExactAuthorizedCall(t *testing.T) {
	signer := &signer{}
	httpClient := &http.Client{Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		if request.Header.Get(toolcontext.HeaderName) != "signed-tool-context" || string(body) != `{}` {
			t.Fatalf("unexpected dispatch: headers=%v body=%s", request.Header, body)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"active":2}`))}, nil
	})}
	client, err := New(Config{Origin: "http://router.internal", Signer: signer, IDs: fixedIDs{}, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	call := runnercapability.AuthorizedCall{Grant: runnerbroker.CapabilityGrant{
		Identity:  runnerbroker.Identity{InvocationID: "20000000-0000-4000-8000-000000000002", PodUID: "30000000-0000-4000-8000-000000000003"},
		AccountID: "10000000-0000-4000-8000-000000000001", Capability: runnercapability.WorkSummaryCapability,
		ExpiresAt: time.Now().Add(time.Minute),
	}, OperationID: "40000000-0000-4000-8000-000000000004", Input: []byte(`{}`)}
	output, err := client.Execute(context.Background(), call)
	if err != nil || string(output) != `{"active":2}` {
		t.Fatalf("output=%s err=%v", output, err)
	}
	if signer.authority.RequestID != "60000000-0000-4000-8000-000000000006" || signer.authority.AccountID != ids.AccountID("10000000-0000-4000-8000-000000000001") || signer.authority.Capability != runnercapability.WorkSummaryCapability {
		t.Fatalf("unexpected authority: %#v", signer.authority)
	}
	if signer.binding.Method != http.MethodPost || signer.binding.Target != "/internal/v1/tools:invoke" || signer.binding.HeadersSHA256 == "" {
		t.Fatalf("unexpected binding: %#v", signer.binding)
	}
}
