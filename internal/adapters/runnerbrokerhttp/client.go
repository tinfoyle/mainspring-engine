// Package runnerbrokerhttp is the runner-side client for the broker exchange.
package runnerbrokerhttp

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
	"os"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	maximumTokenBytes    = 16 << 10
	maximumResponseBytes = runnerbroker.MaximumEnvelopeBytes + 64<<10
)

type Config struct {
	BrokerURL, InvocationID, IdentityTokenFile string
	HTTPClient                                 *http.Client
}

type Client struct {
	baseURL, invocationID, tokenFile string
	http                             *http.Client
}

func New(config Config) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(config.BrokerURL), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && config.HTTPClient != nil)) {
		return nil, errors.New("runner broker URL is invalid")
	}
	if ids.Validate(config.InvocationID) != nil || strings.TrimSpace(config.IdentityTokenFile) == "" {
		return nil, errors.New("runner invocation ID and identity token file are required")
	}
	client := config.HTTPClient
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS13}
		client = &http.Client{Transport: transport, Timeout: 15 * time.Second}
	} else {
		copyClient := *client
		client = &copyClient
		if client.Timeout == 0 || client.Timeout > 30*time.Second {
			client.Timeout = 15 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("runner broker redirects are denied") }
	return &Client{baseURL: base, invocationID: config.InvocationID, tokenFile: config.IdentityTokenFile, http: client}, nil
}

func (c *Client) Fetch(ctx context.Context) (runnerbroker.Request, error) {
	request, err := c.newRequest(ctx, http.MethodPost, "request", nil)
	if err != nil {
		return runnerbroker.Request{}, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return runnerbroker.Request{}, fmt.Errorf("fetch runner request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return runnerbroker.Request{}, decodeError(response)
	}
	var value runnerbroker.Request
	if err := decodeJSON(response, &value); err != nil {
		return runnerbroker.Request{}, fmt.Errorf("decode runner request: %w", err)
	}
	return value, nil
}

func (c *Client) Submit(ctx context.Context, result runnerbroker.Result) (bool, error) {
	body, err := json.Marshal(result)
	if err != nil || len(body) > maximumResponseBytes {
		return false, runnerbroker.ErrInvalidExchange
	}
	request, err := c.newRequest(ctx, http.MethodPut, "result", body)
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return false, fmt.Errorf("submit runner result: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return false, decodeError(response)
	}
	var accepted struct {
		Status       string `json:"status"`
		NewlyCreated bool   `json:"newly_created"`
	}
	if err := decodeJSON(response, &accepted); err != nil || accepted.Status != "accepted" || accepted.NewlyCreated != (response.StatusCode == http.StatusCreated) {
		return false, errors.New("runner broker returned an invalid result acknowledgement")
	}
	return accepted.NewlyCreated, nil
}

func (c *Client) Invoke(ctx context.Context, call runnercapability.Call) (runnercapability.Result, error) {
	body, err := json.Marshal(call)
	if err != nil || len(body) > runnercapability.MaximumInputBytes+64<<10 {
		return runnercapability.Result{}, runnercapability.ErrInvalidCall
	}
	request, err := c.newRequest(ctx, http.MethodPost, "capabilities:invoke", body)
	if err != nil {
		return runnercapability.Result{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return runnercapability.Result{}, fmt.Errorf("invoke runner capability: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return runnercapability.Result{}, decodeError(response)
	}
	var result runnercapability.Result
	if err := decodeJSON(response, &result); err != nil || result.SchemaVersion != runnercapability.SchemaVersion {
		return runnercapability.Result{}, errors.New("runner capability gateway returned an invalid result")
	}
	return result, nil
}

func (c *Client) newRequest(ctx context.Context, method, suffix string, body []byte) (*http.Request, error) {
	token, err := readToken(c.tokenFile)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/internal/v1/runner/invocations/"+c.invocationID+"/"+suffix, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json, application/problem+json")
	return request, nil
}

func readToken(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", fmt.Errorf("read runner identity token: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximumTokenBytes+1))
	token := string(raw)
	if err != nil || token == "" || len(token) > maximumTokenBytes || strings.TrimSpace(token) != token || strings.ContainsAny(token, " \t\r\n\x00") {
		return "", errors.New("runner identity token is invalid")
	}
	return token, nil
}

func decodeJSON(response *http.Response, target any) error {
	if strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		return errors.New("runner broker response is not JSON")
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maximumResponseBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return errors.New("runner broker response has trailing data")
	}
	return nil
}

func decodeError(response *http.Response) error {
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&problem)
	switch problem.Code {
	case "runner_identity_denied":
		return runnerbroker.ErrIdentityDenied
	case "runner_request_invalid", "runner_result_invalid", "runner_result_too_large", "runner_result_json_required", "runner_exchange_invalid":
		return runnerbroker.ErrInvalidExchange
	case "runner_exchange_not_ready":
		return runnerbroker.ErrExchangeNotReady
	case "runner_exchange_canceled":
		return runnerbroker.ErrExchangeCanceled
	case "runner_exchange_expired":
		return runnerbroker.ErrExchangeExpired
	case "runner_exchange_conflict":
		return runnerbroker.ErrExchangeConflict
	case "capability_call_invalid", "capability_call_too_large", "capability_json_required":
		return runnercapability.ErrInvalidCall
	case "capability_denied":
		return runnerbroker.ErrCapabilityDenied
	case "capability_unavailable":
		return runnercapability.ErrUnavailable
	case "capability_action_denied":
		return runnercapability.ErrActionDenied
	case "capability_action_unavailable":
		return runnercapability.ErrActionUnavailable
	case "capability_execution_failed":
		return runnercapability.ErrExecutionFailed
	case "capability_audit_unavailable":
		return runnercapability.ErrAuditUnavailable
	default:
		return fmt.Errorf("runner broker request failed with HTTP %d", response.StatusCode)
	}
}
