package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

type CodexProvider struct {
	binary string
	mu     sync.Mutex
	runs   map[domain.InvocationID]*exec.Cmd
}

func NewCodexProvider(binary string) *CodexProvider {
	return &CodexProvider{binary: binary, runs: make(map[domain.InvocationID]*exec.Cmd)}
}

func (p *CodexProvider) Name() string { return "codex-cli" }

func (p *CodexProvider) Capabilities() Capabilities {
	return Capabilities{
		StructuredOutput: true,
		ToolCalling:      true,
		SessionResume:    true,
		StreamingEvents:  true,
	}
}

func (p *CodexProvider) Invoke(ctx context.Context, invocation Invocation) (Result, error) {
	if invocation.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, invocation.Timeout)
		defer cancel()
	}

	workspace, err := os.MkdirTemp("", "mainspring-agent-*")
	if err != nil {
		return Result{}, fmt.Errorf("create agent workspace: %w", err)
	}
	defer os.RemoveAll(workspace)

	prompt := buildPrompt(invocation)
	command := exec.CommandContext(ctx, p.binary,
		"exec",
		"--json",
		"--ephemeral",
		"--skip-git-repo-check",
		"--sandbox", "read-only",
		"-",
	)
	command.Dir = workspace
	command.Stdin = strings.NewReader(prompt)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("open Codex output: %w", err)
	}

	p.mu.Lock()
	p.runs[invocation.ID] = command
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.runs, invocation.ID)
		p.mu.Unlock()
	}()

	if err := command.Start(); err != nil {
		return Result{}, fmt.Errorf("start Codex: %w", err)
	}

	result := Result{Provider: p.Name(), Metadata: make(map[string]any)}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		typeName, _ := event["type"].(string)
		switch typeName {
		case "thread.started":
			result.ProviderThreadID, _ = event["thread_id"].(string)
		case "item.completed":
			item, _ := event["item"].(map[string]any)
			if itemType, _ := item["type"].(string); itemType == "agent_message" {
				if text, _ := item["text"].(string); strings.TrimSpace(text) != "" {
					result.Body = text
				}
			}
		case "turn.completed":
			usage, _ := event["usage"].(map[string]any)
			result.Usage.InputTokens = jsonInteger(usage["input_tokens"])
			result.Usage.CachedInputTokens = jsonInteger(usage["cached_input_tokens"])
			result.Usage.OutputTokens = jsonInteger(usage["output_tokens"])
		}
	}
	if err := scanner.Err(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return Result{}, fmt.Errorf("read Codex events: %w", err)
	}
	if err := command.Wait(); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("Codex failed: %w: %s", err, truncateText(stderr.String(), 2048))
	}
	if strings.TrimSpace(result.Body) == "" {
		return Result{}, errors.New("Codex completed without an agent message")
	}
	return result, nil
}

func (p *CodexProvider) Cancel(_ context.Context, id domain.InvocationID) error {
	p.mu.Lock()
	command := p.runs[id]
	p.mu.Unlock()
	if command == nil || command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}

func buildPrompt(invocation Invocation) string {
	var builder strings.Builder
	builder.WriteString("You are participating as a bounded persona in a Mainspring boardroom.\n")
	builder.WriteString("The Mainspring application owns turn selection and orchestration. Do not attempt to invoke another agent.\n")
	builder.WriteString("Respond only with your professional contribution to the boardroom discussion.\n\n")
	builder.WriteString("Persona: ")
	builder.WriteString(invocation.PersonaName)
	builder.WriteString(" (" + invocation.PersonaRole + ")\n")
	builder.WriteString("Instructions: ")
	builder.WriteString(invocation.SystemInstructions)
	builder.WriteString("\n\nConversation:\n")
	for _, message := range invocation.Conversation {
		label := string(message.Role)
		if message.PersonaName != "" {
			label = message.PersonaName
		}
		builder.WriteString(label)
		builder.WriteString(": ")
		builder.WriteString(message.Body)
		builder.WriteString("\n")
	}
	return builder.String()
}

func jsonInteger(value any) int64 {
	switch number := value.(type) {
	case float64:
		return int64(number)
	case json.Number:
		result, _ := number.Int64()
		return result
	default:
		return 0
	}
}

func truncateText(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
