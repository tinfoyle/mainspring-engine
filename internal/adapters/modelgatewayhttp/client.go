// Package modelgatewayhttp adapts an authorized runner capability call to the
// private model gateway. Workload mTLS authenticates this client; the adapter
// injects invocation identity so a runner cannot select another invocation.
package modelgatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
)

type Config struct {
	Origin     string
	HTTPClient *http.Client
}

type Client struct {
	endpoint string
	http     *http.Client
}

func New(config Config) (*Client, error) {
	origin := strings.TrimRight(strings.TrimSpace(config.Origin), "/")
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !(parsed.Scheme == "http" && config.HTTPClient != nil)) {
		return nil, errors.New("model gateway client configuration is invalid")
	}
	client := config.HTTPClient
	if client == nil {
		return nil, errors.New("model gateway workload transport is required")
	}
	copyClient := *client
	client = &copyClient
	if client.Timeout == 0 || client.Timeout > modelgateway.MaximumProviderTimeout {
		client.Timeout = modelgateway.MaximumProviderTimeout
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("model gateway redirects are denied") }
	return &Client{endpoint: origin + "/internal/v1/model-turns:invoke", http: client}, nil
}

func (c *Client) Execute(ctx context.Context, call runnercapability.AuthorizedCall) (json.RawMessage, error) {
	if call.Grant.Capability != modelgateway.ModelTurnCapability {
		return nil, codedError("model_capability_invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(call.Input))
	decoder.DisallowUnknownFields()
	var request modelgateway.Request
	if err := decoder.Decode(&request); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return nil, codedError("model_request_invalid")
	}
	request.InvocationID = call.Grant.Identity.InvocationID
	request.OperationID = call.OperationID
	request.SchemaVersion = modelgateway.SchemaVersion
	raw, err := json.Marshal(request)
	if err != nil || len(raw) > modelgateway.MaximumRequestBytes {
		return nil, codedError("model_request_invalid")
	}
	outbound, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, codedError("model_gateway_unavailable")
	}
	outbound.Header.Set("Content-Type", "application/json")
	outbound.Header.Set("Accept", "application/json, application/problem+json")
	response, err := c.http.Do(outbound)
	if err != nil {
		return nil, fmt.Errorf("dispatch model turn: %w", codedError("model_gateway_unavailable"))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, decodeError(response)
	}
	if strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		return nil, codedError("model_gateway_reply_invalid")
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, runnercapability.MaximumOutputBytes+1))
	if err != nil || len(responseBody) > runnercapability.MaximumOutputBytes {
		return nil, codedError("model_gateway_reply_invalid")
	}
	var result modelgateway.Result
	resultDecoder := json.NewDecoder(bytes.NewReader(responseBody))
	resultDecoder.DisallowUnknownFields()
	if err := resultDecoder.Decode(&result); err != nil || !errors.Is(resultDecoder.Decode(&struct{}{}), io.EOF) {
		return nil, codedError("model_gateway_reply_invalid")
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return nil, codedError("model_gateway_reply_invalid")
	}
	return canonical, nil
}

func decodeError(response *http.Response) error {
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&problem)
	switch problem.Code {
	case "model_request_invalid", "model_request_too_large":
		return codedError("model_request_invalid")
	case "model_provider_unavailable", "model_gateway_unavailable":
		return codedError("model_provider_unavailable")
	case "model_provider_failed":
		return codedError("model_provider_failed")
	case "model_provider_reply_invalid":
		return codedError("model_provider_reply_invalid")
	default:
		return codedError("model_gateway_unavailable")
	}
}

type codedError string

func (e codedError) Error() string { return string(e) }
func (e codedError) Code() string  { return string(e) }

var _ runnercapability.Handler = (*Client)(nil)
