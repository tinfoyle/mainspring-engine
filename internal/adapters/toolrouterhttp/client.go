// Package toolrouterhttp adapts an authorized runner capability call to the
// private app-router tool dispatch boundary.
package toolrouterhttp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/platform/toolcontext"
)

type TokenSigner interface {
	Issue(toolcontext.Authority, routecontext.Binding) (string, error)
}

type Config struct {
	Origin     string
	Signer     TokenSigner
	IDs        ids.Generator
	HTTPClient *http.Client
}

type Client struct {
	endpoint string
	signer   TokenSigner
	ids      ids.Generator
	http     *http.Client
}

func New(config Config) (*Client, error) {
	origin := strings.TrimRight(strings.TrimSpace(config.Origin), "/")
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && config.HTTPClient != nil)) || config.Signer == nil || config.IDs == nil {
		return nil, errors.New("tool router configuration is invalid")
	}
	client := config.HTTPClient
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS13}
		client = &http.Client{Transport: transport, Timeout: 45 * time.Second}
	} else {
		copyClient := *client
		client = &copyClient
		if client.Timeout == 0 || client.Timeout > 30*time.Second {
			client.Timeout = 45 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("tool router redirects are denied") }
	return &Client{endpoint: origin + "/internal/v1/tools:invoke", signer: config.Signer, ids: config.IDs, http: client}, nil
}

func (c *Client) Execute(ctx context.Context, call runnercapability.AuthorizedCall) (json.RawMessage, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(call.Input))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, application/problem+json")
	binding, err := routecontext.BindRequest(request, call.Input)
	if err != nil {
		return nil, runnercapability.ErrInvalidCall
	}
	token, err := c.signer.Issue(toolcontext.Authority{
		RequestID: c.ids.New(), AccountID: call.Grant.AccountID, InvocationID: call.Grant.Identity.InvocationID,
		PodUID: call.Grant.Identity.PodUID, OperationID: call.OperationID, Capability: call.Grant.Capability,
	}, binding)
	if err != nil {
		return nil, err
	}
	request.Header.Set(toolcontext.HeaderName, token)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("dispatch runner tool: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, decodeError(response)
	}
	if strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		return nil, codedError("tool_response_invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, runnercapability.MaximumOutputBytes+1))
	if err != nil || len(raw) > runnercapability.MaximumOutputBytes || !json.Valid(raw) {
		return nil, codedError("tool_response_invalid")
	}
	return raw, nil
}

func decodeError(response *http.Response) error {
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&problem)
	switch problem.Code {
	case "tool_context_replay", "tool_context_invalid", "tool_context_required":
		return codedError("tool_identity_denied")
	case "tool_authorization_denied":
		return codedError("tool_authorization_denied")
	case "capability_unavailable":
		return codedError("capability_unavailable")
	case "tool_input_invalid", "tool_dispatch_invalid", "tool_dispatch_too_large":
		return codedError("tool_input_invalid")
	case "tool_boundary_unavailable", "tool_routing_unavailable", "tool_cell_unavailable", "tool_execution_failed", "tool_response_invalid":
		return codedError(problem.Code)
	default:
		return codedError("tool_dispatch_failed")
	}
}

type codedError string

func (e codedError) Error() string { return string(e) }
func (e codedError) Code() string  { return string(e) }

var _ runnercapability.Handler = (*Client)(nil)
