package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
)

var validExecutionCode = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)

type AIExecutionTarget struct {
	Provider        string   `json:"provider"`
	Model           string   `json:"model"`
	FallbackModels  []string `json:"fallback_models"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
}

// ApplyAIExecutionPolicies overlays private operational targets onto an
// immutable customer rate publication. Token prices and versions remain
// Catalog-owned; provider configuration is injected per environment.
func ApplyAIExecutionPolicies(publication PublishedCatalog, raw string) (PublishedCatalog, error) {
	targets, err := parseAIExecutionPolicies(raw)
	if err != nil {
		return PublishedCatalog{}, err
	}
	publication.AIComplexityRates = append([]AIComplexityRate(nil), publication.AIComplexityRates...)
	seen := make(map[AIComplexity]struct{}, len(publication.AIComplexityRates))
	for index := range publication.AIComplexityRates {
		rate := &publication.AIComplexityRates[index]
		target, exists := targets[rate.Complexity]
		if !exists {
			return PublishedCatalog{}, errors.New("AI execution target is missing from the rate publication")
		}
		rate.InternalProvider = target.Provider
		rate.InternalModel = target.Model
		rate.InternalFallbackModels = append([]string(nil), target.FallbackModels...)
		rate.InternalReasoningEffort = target.ReasoningEffort
		seen[rate.Complexity] = struct{}{}
	}
	if len(seen) != len(AIComplexities) {
		return PublishedCatalog{}, errors.New("AI complexity publication must contain every execution level")
	}
	if err := publication.ValidateGoverned(); err != nil {
		return PublishedCatalog{}, err
	}
	return publication, nil
}

func parseAIExecutionPolicies(raw string) (map[AIComplexity]AIExecutionTarget, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var source map[AIComplexity]AIExecutionTarget
	if strings.TrimSpace(raw) == "" || decoder.Decode(&source) != nil {
		return nil, errors.New("AI execution policies are invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("AI execution policies must contain exactly one JSON object")
	}
	if len(source) != len(AIComplexities) {
		return nil, errors.New("AI execution policies must define every complexity exactly once")
	}
	result := make(map[AIComplexity]AIExecutionTarget, len(source))
	for complexity, target := range source {
		if !slices.Contains(AIComplexities, complexity) {
			return nil, errors.New("AI execution policy has an unknown complexity")
		}
		target.Provider = strings.TrimSpace(target.Provider)
		target.Model = strings.TrimSpace(target.Model)
		target.ReasoningEffort = strings.TrimSpace(target.ReasoningEffort)
		target.FallbackModels = append([]string(nil), target.FallbackModels...)
		for index := range target.FallbackModels {
			target.FallbackModels[index] = strings.TrimSpace(target.FallbackModels[index])
		}
		models := append([]string{target.Model}, target.FallbackModels...)
		if !validExecutionCode.MatchString(target.Provider) || len(target.FallbackModels) > 2 || (target.ReasoningEffort != "" && !validExecutionCode.MatchString(target.ReasoningEffort)) {
			return nil, errors.New("AI execution target is invalid")
		}
		for index, model := range models {
			if !validExecutionCode.MatchString(model) || slices.Contains(models[:index], model) {
				return nil, errors.New("AI execution target models are invalid")
			}
		}
		result[complexity] = target
	}
	return result, nil
}
