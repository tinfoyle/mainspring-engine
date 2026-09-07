// Package openairesponses adapts the OpenAI-compatible Responses protocol to Spyglass's
// provider-neutral model gateway. Provider credentials terminate here and are
// never serialized into runner work.
package openairesponses

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
)

const defaultOrigin = "https://api.openai.com"

type Config struct {
	APIKey     string
	Origin     string
	HTTPClient *http.Client
	// StructuredOutputViaTool is used by Kimi: text.format suppresses function
	// selection, so a private terminal function carries the final JSON instead.
	StructuredOutputViaTool bool
}

type Client struct {
	apiKey                  string
	endpoint                string
	http                    *http.Client
	structuredOutputViaTool bool
}

func New(config Config) (*Client, error) {
	key := strings.TrimSpace(config.APIKey)
	origin := strings.TrimRight(strings.TrimSpace(config.Origin), "/")
	if origin == "" {
		origin = defaultOrigin
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !(parsed.Scheme == "http" && config.HTTPClient != nil)) || key == "" || len(key) > 4096 || strings.ContainsAny(key, "\r\n\x00") {
		return nil, errors.New("OpenAI Responses configuration is invalid")
	}
	client := config.HTTPClient
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		client = &http.Client{Transport: transport, Timeout: modelgateway.MaximumProviderTimeout}
	} else {
		copyClient := *client
		client = &copyClient
		if client.Timeout == 0 || client.Timeout > modelgateway.MaximumProviderTimeout {
			client.Timeout = modelgateway.MaximumProviderTimeout
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("OpenAI redirects are denied") }
	return &Client{apiKey: key, endpoint: origin + "/v1/responses", http: client, structuredOutputViaTool: config.StructuredOutputViaTool}, nil
}

func (c *Client) Invoke(ctx context.Context, request modelgateway.Request) (modelgateway.Result, error) {
	payload, err := buildRequest(request)
	if err == nil && c.structuredOutputViaTool {
		err = useStructuredOutputTool(&payload, request)
	}
	if err != nil {
		return modelgateway.Result{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > modelgateway.MaximumRequestBytes {
		return modelgateway.Result{}, modelgateway.ErrInvalidRequest
	}
	outbound, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return modelgateway.Result{}, modelgateway.ErrProviderUnavailable
	}
	outbound.Header.Set("Authorization", "Bearer "+c.apiKey)
	outbound.Header.Set("Content-Type", "application/json")
	outbound.Header.Set("Accept", "application/json")
	response, err := c.http.Do(outbound)
	if err != nil {
		return modelgateway.Result{}, fmt.Errorf("%w: OpenAI request failed", modelgateway.ErrProviderUnavailable)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, modelgateway.MaximumResponseBytes+1))
	if err != nil || len(raw) > modelgateway.MaximumResponseBytes {
		return modelgateway.Result{}, modelgateway.ErrInvalidProviderReply
	}
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return modelgateway.Result{}, modelgateway.ErrProviderUnavailable
		}
		return modelgateway.Result{}, modelgateway.ErrProviderFailed
	}
	result, err := decodeResponse(request.Provider, raw)
	if err != nil || !c.structuredOutputViaTool {
		return result, err
	}
	if len(result.ToolCalls) == 1 && result.ToolCalls[0].Name == finalResultTool {
		result.Output = append(json.RawMessage(nil), result.ToolCalls[0].Arguments...)
		result.ToolCalls, result.Continuation = nil, nil
		result.StopReason = "completed"
		return result, nil
	}
	// A tool-backed final result is mandatory in this protocol mode. Never
	// silently accept prose as a completed turn or expose the private function.
	if len(result.ToolCalls) != 1 {
		return modelgateway.Result{}, modelgateway.ErrInvalidProviderReply
	}
	return result, nil
}

type responseRequest struct {
	Model             string             `json:"model"`
	Instructions      string             `json:"instructions"`
	Input             []any              `json:"input"`
	Tools             []responseTool     `json:"tools,omitempty"`
	ToolChoice        string             `json:"tool_choice,omitempty"`
	ParallelToolCalls bool               `json:"parallel_tool_calls"`
	Text              *responseText      `json:"text,omitempty"`
	Reasoning         *responseReasoning `json:"reasoning,omitempty"`
	MaxOutputTokens   int                `json:"max_output_tokens"`
	Store             bool               `json:"store"`
}

type responseTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type responseText struct {
	Format responseFormat `json:"format"`
}
type responseFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}
type responseReasoning struct {
	Effort string `json:"effort"`
}

func buildRequest(request modelgateway.Request) (responseRequest, error) {
	input := make([]any, 0, len(request.Messages)+len(request.History)*3+8)
	for _, message := range request.Messages {
		input = append(input, map[string]any{"role": message.Role, "content": message.Content})
	}
	for _, exchange := range request.History {
		var continuation []json.RawMessage
		if err := json.Unmarshal(exchange.Continuation, &continuation); err != nil {
			return responseRequest{}, modelgateway.ErrInvalidRequest
		}
		for _, item := range continuation {
			input = append(input, item)
		}
		input = append(input, map[string]any{"type": "function_call_output", "call_id": exchange.ToolOutput.CallID, "output": string(exchange.ToolOutput.Output)})
	}
	tools := make([]responseTool, len(request.Tools))
	for index, tool := range request.Tools {
		tools[index] = responseTool{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema, Strict: true}
	}
	payload := responseRequest{Model: request.Model, Instructions: request.Instructions, Input: input, Tools: tools, ParallelToolCalls: false, Text: &responseText{Format: responseFormat{Type: "json_schema", Name: request.OutputFormat.Name, Strict: true, Schema: request.OutputFormat.Schema}}, MaxOutputTokens: request.MaximumOutTokens, Store: false}
	if len(tools) != 0 {
		payload.ToolChoice = "auto"
	}
	if request.ReasoningEffort != "" {
		payload.Reasoning = &responseReasoning{Effort: request.ReasoningEffort}
	}
	return payload, nil
}

const finalResultTool = "spyglass_final_result"

func useStructuredOutputTool(payload *responseRequest, request modelgateway.Request) error {
	for _, tool := range request.Tools {
		if tool.Name == finalResultTool {
			return modelgateway.ErrInvalidRequest
		}
	}
	payload.Text = nil
	payload.Tools = append(payload.Tools, responseTool{Type: "function", Name: finalResultTool,
		Description: "Submit your complete final answer after any necessary tool calls. This ends the turn and performs no business action.",
		Parameters:  request.OutputFormat.Schema, Strict: true})
	payload.ToolChoice = "auto"
	payload.Instructions += "\n\nResponse protocol: make exactly one function call per response and wait for its result before making another. Use the available tools as needed, then call spyglass_final_result exactly once with your complete final answer matching its schema. Do not write a separate text answer. This final function only submits your answer; business actions still require their normal platform tools and approvals."
	return nil
}

type responseEnvelope struct {
	ID     string            `json:"id"`
	Model  string            `json:"model"`
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
	Usage  struct {
		InputTokens       int64 `json:"input_tokens"`
		OutputTokens      int64 `json:"output_tokens"`
		TotalTokens       int64 `json:"total_tokens"`
		InputTokenDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
	} `json:"usage"`
}

func decodeResponse(provider string, raw []byte) (modelgateway.Result, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var response responseEnvelope
	if err := decoder.Decode(&response); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || response.Status != "completed" || len(response.Output) == 0 {
		return modelgateway.Result{}, modelgateway.ErrInvalidProviderReply
	}
	result := modelgateway.Result{SchemaVersion: modelgateway.SchemaVersion, Provider: provider, Model: response.Model, ResponseID: response.ID, Usage: modelgateway.Usage{InputTokens: response.Usage.InputTokens, CachedInputTokens: response.Usage.InputTokenDetails.CachedTokens, OutputTokens: response.Usage.OutputTokens, TotalTokens: response.Usage.TotalTokens}}
	for _, rawItem := range response.Output {
		var item struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		}
		if err := json.Unmarshal(rawItem, &item); err != nil {
			return modelgateway.Result{}, modelgateway.ErrInvalidProviderReply
		}
		switch item.Type {
		case "function_call":
			result.ToolCalls = append(result.ToolCalls, modelgateway.ToolCall{CallID: item.CallID, Name: item.Name, Arguments: json.RawMessage(item.Arguments)})
		case "message":
			for _, content := range item.Content {
				switch content.Type {
				case "output_text":
					if len(result.Output) != 0 {
						return modelgateway.Result{}, modelgateway.ErrInvalidProviderReply
					}
					result.Output = json.RawMessage(content.Text)
				case "refusal":
					return modelgateway.Result{}, modelgateway.ErrProviderFailed
				}
			}
		case "reasoning":
			// Reasoning items are retained only in the opaque continuation when
			// the provider asks for a tool. Spyglass never interprets them.
		default:
			return modelgateway.Result{}, modelgateway.ErrInvalidProviderReply
		}
	}
	if len(result.ToolCalls) != 0 {
		if len(result.Output) != 0 {
			return modelgateway.Result{}, modelgateway.ErrInvalidProviderReply
		}
		continuation, err := json.Marshal(response.Output)
		if err != nil {
			return modelgateway.Result{}, modelgateway.ErrInvalidProviderReply
		}
		result.StopReason, result.Continuation = "tool_call", continuation
		return result, nil
	}
	if len(result.Output) == 0 {
		return modelgateway.Result{}, modelgateway.ErrInvalidProviderReply
	}
	result.StopReason = "completed"
	return result, nil
}

var _ modelgateway.Provider = (*Client)(nil)
