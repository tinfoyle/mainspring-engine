package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

type RemoteProvider struct {
	baseURL string
	inner   string
	client  *http.Client
}

type RemoteInvocation struct {
	Provider   string     `json:"provider"`
	Invocation Invocation `json:"invocation"`
}

type RemoteResult struct {
	Result    Result          `json:"result"`
	Error     string          `json:"error,omitempty"`
	Category  FailureCategory `json:"category,omitempty"`
	Retryable bool            `json:"retryable,omitempty"`
}

func NewRemoteProvider(baseURL, inner string, timeout time.Duration) (*RemoteProvider, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || inner == "" {
		return nil, fmt.Errorf("runner URL and provider are required")
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &RemoteProvider{baseURL: baseURL, inner: inner, client: &http.Client{Timeout: timeout + time.Minute}}, nil
}

func (p *RemoteProvider) Name() string { return "runner:" + p.inner }
func (p *RemoteProvider) Capabilities() Capabilities {
	return Capabilities{StructuredOutput: true, ToolCalling: true, StreamingEvents: false, SessionResume: false}
}

func (p *RemoteProvider) Invoke(ctx context.Context, invocation Invocation) (Result, error) {
	selected := p.inner
	if invocation.Provider != "" && invocation.Provider != "inherit" {
		selected = invocation.Provider
	}
	body, err := json.Marshal(RemoteInvocation{Provider: selected, Invocation: invocation})
	if err != nil {
		return Result{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/invocations", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return Result{}, &InvocationError{Category: FailureUnavailable, Retryable: true, Err: err}
	}
	defer response.Body.Close()
	var output RemoteResult
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&output); err != nil {
		return Result{}, &InvocationError{Category: FailureUnavailable, Retryable: true, Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || output.Error != "" {
		category := output.Category
		if category == "" {
			category = FailureUnavailable
		}
		return Result{}, &InvocationError{Category: category, Retryable: output.Retryable, Err: fmt.Errorf("runner invocation failed: %s", output.Error)}
	}
	return output.Result, nil
}

func (p *RemoteProvider) Cancel(ctx context.Context, invocationID domain.InvocationID) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, p.baseURL+"/v1/invocations/"+invocationID.String(), nil)
	if err != nil {
		return err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusNoContent {
		return nil
	}
	return fmt.Errorf("runner cancellation returned %s", response.Status)
}
