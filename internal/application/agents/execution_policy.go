package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"

	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
)

var validExecutionCode = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)

// ExecutionTarget is private operational configuration. It is frozen into an
// immutable Persona version but is never accepted from or returned to a
// customer API client.
type ExecutionTarget struct {
	Provider        string   `json:"provider"`
	Model           string   `json:"model"`
	FallbackModels  []string `json:"fallback_models"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
}

type ExecutionPolicyResolver interface {
	Resolve(agentdomain.PersonaComplexity) (ExecutionTarget, error)
}

type StaticExecutionPolicyResolver struct {
	targets map[agentdomain.PersonaComplexity]ExecutionTarget
}

// ParseExecutionPolicyResolver parses the complete five-level private model
// map. Startup fails closed if a level is missing or any target is malformed.
func ParseExecutionPolicyResolver(raw string) (*StaticExecutionPolicyResolver, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var source map[agentdomain.PersonaComplexity]ExecutionTarget
	if strings.TrimSpace(raw) == "" || decoder.Decode(&source) != nil {
		return nil, errors.New("Agent execution policies are invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("Agent execution policies must contain exactly one JSON object")
	}
	if len(source) != len(agentdomain.PersonaComplexities) {
		return nil, errors.New("Agent execution policies must define every complexity exactly once")
	}
	targets := make(map[agentdomain.PersonaComplexity]ExecutionTarget, len(source))
	for complexity, target := range source {
		if !slices.Contains(agentdomain.PersonaComplexities, complexity) {
			return nil, errors.New("Agent execution policy has an unknown complexity")
		}
		canonical, err := canonicalExecutionTarget(target)
		if err != nil {
			return nil, err
		}
		targets[complexity] = canonical
	}
	return &StaticExecutionPolicyResolver{targets: targets}, nil
}

func (resolver *StaticExecutionPolicyResolver) Resolve(complexity agentdomain.PersonaComplexity) (ExecutionTarget, error) {
	if resolver == nil {
		return ExecutionTarget{}, ErrInvalidCommand
	}
	target, exists := resolver.targets[complexity]
	if !exists {
		return ExecutionTarget{}, ErrInvalidCommand
	}
	target.FallbackModels = append([]string(nil), target.FallbackModels...)
	return target, nil
}

func canonicalExecutionTarget(target ExecutionTarget) (ExecutionTarget, error) {
	target.Provider = strings.TrimSpace(target.Provider)
	target.Model = strings.TrimSpace(target.Model)
	target.ReasoningEffort = strings.TrimSpace(target.ReasoningEffort)
	target.FallbackModels = append([]string(nil), target.FallbackModels...)
	for index := range target.FallbackModels {
		target.FallbackModels[index] = strings.TrimSpace(target.FallbackModels[index])
	}
	if !validExecutionCode.MatchString(target.Provider) || !validExecutionCode.MatchString(target.Model) || len(target.FallbackModels) > agentdomain.MaximumFallbackModels || (target.ReasoningEffort != "" && !validExecutionCode.MatchString(target.ReasoningEffort)) {
		return ExecutionTarget{}, errors.New("Agent execution target is invalid")
	}
	models := append([]string{target.Model}, target.FallbackModels...)
	for index, model := range models {
		if !validExecutionCode.MatchString(model) || slices.Contains(models[:index], model) {
			return ExecutionTarget{}, errors.New("Agent execution target models are invalid")
		}
	}
	return target, nil
}
