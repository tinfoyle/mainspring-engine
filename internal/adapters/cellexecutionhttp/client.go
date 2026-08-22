// Package cellexecutionhttp adapts the private cell app API to the Schedule
// worker without granting that worker an account-data database credential.
package cellexecutionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	schedulingapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
)

const maximumResponseBody = int64(1 << 20)

type Client struct {
	origin *url.URL
	client *http.Client
}

func New(rawOrigin string, allowHTTP bool, transport http.RoundTripper) (*Client, error) {
	origin, err := url.Parse(strings.TrimSpace(rawOrigin))
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" ||
		(origin.Path != "" && origin.Path != "/") || (origin.Scheme != "https" && !(allowHTTP && origin.Scheme == "http")) {
		return nil, errors.New("cell execution origin must be an allowed absolute origin without a path")
	}
	origin.Path = ""
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Client{origin: origin, client: &http.Client{Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("cell execution redirects are not allowed")
		}}}, nil
}

type loadRequest struct {
	Claim schedulingapp.ExecutionClaim `json:"claim"`
}

type loadResponse struct {
	Snapshot schedulingapp.ExecutionSnapshot `json:"snapshot"`
}

type commandRequest struct {
	Command schedulingapp.OccurrenceCommand `json:"command"`
}

type commandResponse struct {
	Reconciled bool `json:"reconciled"`
}

type problemResponse struct {
	Code string `json:"code"`
}

func (client *Client) Load(ctx context.Context, claim schedulingapp.ExecutionClaim) (schedulingapp.ExecutionSnapshot, error) {
	if client == nil || client.origin == nil || !claim.Valid() {
		return schedulingapp.ExecutionSnapshot{}, schedulingapp.ErrExecutionClaimInvalid
	}
	body, status, err := client.do(ctx, "/internal/v1/schedules/executions:load", loadRequest{Claim: claim})
	if err != nil {
		return schedulingapp.ExecutionSnapshot{}, err
	}
	if status < 200 || status >= 300 {
		return schedulingapp.ExecutionSnapshot{}, classify(status, body)
	}
	var response loadResponse
	if json.Unmarshal(body, &response) != nil || !response.Snapshot.ValidFor(claim) {
		return schedulingapp.ExecutionSnapshot{}, schedulingapp.ErrExecutionServiceUnavailable
	}
	return response.Snapshot, nil
}

func (client *Client) Dispatch(ctx context.Context, command schedulingapp.OccurrenceCommand) (bool, error) {
	return client.command(ctx, "/internal/v1/schedules/executions:dispatch", command)
}

func (client *Client) Skip(ctx context.Context, command schedulingapp.OccurrenceCommand) (bool, error) {
	return client.command(ctx, "/internal/v1/schedules/executions:skip", command)
}

func (client *Client) command(ctx context.Context, path string, command schedulingapp.OccurrenceCommand) (bool, error) {
	if client == nil || client.origin == nil || !command.Valid() {
		return false, schedulingapp.ErrExecutionSnapshotInvalid
	}
	body, status, err := client.do(ctx, path, commandRequest{Command: command})
	if err != nil {
		return false, err
	}
	if status < 200 || status >= 300 {
		return false, classify(status, body)
	}
	var response commandResponse
	if json.Unmarshal(body, &response) != nil {
		return false, schedulingapp.ErrExecutionServiceUnavailable
	}
	return response.Reconciled, nil
}

func (client *Client) do(ctx context.Context, path string, value any) ([]byte, int, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, 0, err
	}
	target := *client.origin
	target.Path = path
	request := newRequest(ctx, target.String(), payload)
	response, err := client.client.Do(request)
	if err != nil && response == nil && ctx.Err() == nil {
		response, err = client.client.Do(newRequest(ctx, target.String(), payload))
	}
	if err != nil {
		closeResponse(response)
		return nil, 0, schedulingapp.ErrExecutionServiceUnavailable
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBody+1))
	if err != nil || int64(len(body)) > maximumResponseBody {
		return nil, 0, schedulingapp.ErrExecutionServiceUnavailable
	}
	return body, response.StatusCode, nil
}

func newRequest(ctx context.Context, target string, payload []byte) *http.Request {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	return request
}

func closeResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}

func classify(status int, body []byte) error {
	var problem problemResponse
	if json.Unmarshal(body, &problem) != nil {
		return schedulingapp.ErrExecutionServiceUnavailable
	}
	switch problem.Code {
	case "invalid_schedule_execution":
		return schedulingapp.ErrExecutionSnapshotInvalid
	case "schedule_execution_lease_lost":
		return schedulingapp.ErrExecutionLeaseLost
	case "schedule_execution_conflict":
		return schedulingapp.ErrExecutionConflict
	case "agent_run_capacity":
		return schedulingapp.ErrExecutionCapacity
	case "schedule_workload_scope_denied":
		return schedulingapp.ErrExecutionAuthorization
	}
	if status == http.StatusForbidden || status == http.StatusUnauthorized {
		return schedulingapp.ErrExecutionAuthorization
	}
	return schedulingapp.ErrExecutionServiceUnavailable
}

var _ schedulingapp.OccurrenceExecutor = (*Client)(nil)
