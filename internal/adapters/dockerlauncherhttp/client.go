// Package dockerlauncherhttp adapts the runner-control and runner-identity
// ports to the narrow stage Docker launcher API.
package dockerlauncherhttp

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
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
)

const maximumResponseBytes = 64 << 10

type Config struct {
	Origin     string
	Token      string
	HTTPClient *http.Client
}

type Client struct {
	origin, token string
	http          *http.Client
}

func New(config Config) (*Client, error) {
	origin := strings.TrimRight(strings.TrimSpace(config.Origin), "/")
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && config.HTTPClient != nil)) {
		return nil, errors.New("Docker launcher origin must be an exact HTTPS origin")
	}
	token := strings.TrimSpace(config.Token)
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, " \t\r\n\x00") {
		return nil, errors.New("Docker launcher service token is invalid")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{origin: origin, token: token, http: client}, nil
}

func (c *Client) Ensure(ctx context.Context, invocation runnercontrol.Invocation) (string, error) {
	response, err := c.request(ctx, http.MethodPost, "/internal/v1/runner-launches", invocationRequest(invocation))
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		var result launchResponse
		if err := decode(response.Body, &result); err != nil {
			return "", fmt.Errorf("decode Docker launcher ensure response: %w", err)
		}
		if result.JobName == "" {
			return "", errors.New("Docker launcher returned an empty container identity")
		}
		return result.JobName, nil
	}
	if response.StatusCode == http.StatusServiceUnavailable {
		var result launchResponse
		if err := decode(response.Body, &result); err == nil && result.Code == "launch_uncertain" && result.JobName != "" {
			return result.JobName, runnercontrol.ErrLaunchUncertain
		}
	}
	return "", fmt.Errorf("Docker launcher ensure returned HTTP %d", response.StatusCode)
}

func (c *Client) Cancel(ctx context.Context, invocation runnercontrol.Invocation) error {
	response, err := c.request(ctx, http.MethodPost, invocationPath(invocation.ID)+"/cancel", invocationRequest(invocation))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("Docker launcher cancel returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (c *Client) Inspect(ctx context.Context, invocation runnercontrol.Invocation) (runnercontrol.TerminalStatus, error) {
	response, err := c.request(ctx, http.MethodPost, invocationPath(invocation.ID)+"/inspect", invocationRequest(invocation))
	if err != nil {
		return runnercontrol.TerminalStatus{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return runnercontrol.TerminalStatus{}, fmt.Errorf("Docker launcher inspect returned HTTP %d", response.StatusCode)
	}
	var result runnercontrol.TerminalStatus
	if err := decode(response.Body, &result); err != nil {
		return runnercontrol.TerminalStatus{}, fmt.Errorf("decode Docker launcher inspect response: %w", err)
	}
	return result, nil
}

func (c *Client) Verify(ctx context.Context, token, invocationID string) (runnerbroker.Identity, error) {
	response, err := c.request(ctx, http.MethodPost, invocationPath(invocationID)+"/verify", map[string]string{"token": token})
	if err != nil {
		return runnerbroker.Identity{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusNotFound {
		return runnerbroker.Identity{}, runnerbroker.ErrIdentityDenied
	}
	if response.StatusCode != http.StatusOK {
		return runnerbroker.Identity{}, fmt.Errorf("Docker launcher identity verification returned HTTP %d", response.StatusCode)
	}
	var identity runnerbroker.Identity
	if err := decode(response.Body, &identity); err != nil {
		return runnerbroker.Identity{}, fmt.Errorf("decode Docker launcher identity response: %w", err)
	}
	return identity, nil
}

func (c *Client) request(ctx context.Context, method, path string, value any) (*http.Response, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Docker launcher request: %w", err)
	}
	return response, nil
}

type invocationWire struct {
	ID           string `json:"invocation_id"`
	AccountID    string `json:"account_id"`
	Profile      string `json:"profile"`
	State        string `json:"state"`
	AttemptCount int    `json:"attempt_count"`
	JobName      string `json:"job_name,omitempty"`
}

type launchResponse struct {
	JobName string `json:"job_name"`
	Code    string `json:"code,omitempty"`
	Status  int    `json:"status,omitempty"`
}

func invocationRequest(invocation runnercontrol.Invocation) invocationWire {
	return invocationWire{ID: invocation.ID, AccountID: string(invocation.AccountID), Profile: invocation.Profile, State: invocation.State, AttemptCount: invocation.AttemptCount, JobName: invocation.JobName}
}

func invocationPath(invocationID string) string {
	return "/internal/v1/runner-launches/" + url.PathEscape(invocationID)
}

func decode(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maximumResponseBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("response contains trailing data")
	}
	return nil
}

var _ runnercontrol.Launcher = (*Client)(nil)
var _ runnerbroker.IdentityVerifier = (*Client)(nil)
