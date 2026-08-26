package catalog

import (
	"testing"
	"time"
)

const completeExecutionPolicies = `{
  "simple":{"provider":"zai","model":"glm-simple","fallback_models":[]},
  "efficient":{"provider":"kimi","model":"kimi-efficient","fallback_models":["glm-efficient"]},
  "balanced":{"provider":"openai","model":"gpt-balanced","fallback_models":["kimi-balanced"],"reasoning_effort":"medium"},
  "thorough":{"provider":"openai","model":"gpt-thorough","fallback_models":[],"reasoning_effort":"high"},
  "advanced":{"provider":"openai","model":"gpt-advanced","fallback_models":["kimi-advanced","glm-advanced"],"reasoning_effort":"high"}
}`

func TestApplyAIExecutionPoliciesPreservesCommercialRateAndOverlaysPrivateTarget(t *testing.T) {
	publication := Default(time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC))
	before := publication.AIComplexityRates[2]
	result, err := ApplyAIExecutionPolicies(publication, completeExecutionPolicies)
	if err != nil {
		t.Fatal(err)
	}
	rate := result.AIComplexityRates[2]
	if rate.Code != before.Code || rate.Version != before.Version || rate.MaximumReservation != before.MaximumReservation || rate.InputPerThousand != before.InputPerThousand ||
		rate.InternalProvider != "openai" || rate.InternalModel != "gpt-balanced" || len(rate.InternalFallbackModels) != 1 || rate.InternalFallbackModels[0] != "kimi-balanced" || rate.InternalReasoningEffort != "medium" {
		t.Fatalf("overlaid rate=%+v before=%+v", rate, before)
	}
	if publication.AIComplexityRates[2].InternalModel != "balanced" {
		t.Fatal("private policy overlay mutated the caller's publication")
	}
}

func TestApplyAIExecutionPoliciesRequiresExactCompletePrivateMap(t *testing.T) {
	publication := Default(time.Now().UTC())
	for name, raw := range map[string]string{
		"missing level":   `{"simple":{"provider":"zai","model":"glm-simple","fallback_models":[]}}`,
		"unknown field":   `{"simple":{"provider":"zai","model":"glm-simple","fallback_models":[],"secret":"bad"},"efficient":{"provider":"zai","model":"glm-efficient","fallback_models":[]},"balanced":{"provider":"zai","model":"glm-balanced","fallback_models":[]},"thorough":{"provider":"zai","model":"glm-thorough","fallback_models":[]},"advanced":{"provider":"zai","model":"glm-advanced","fallback_models":[]}}`,
		"duplicate model": `{"simple":{"provider":"zai","model":"same","fallback_models":["same"]},"efficient":{"provider":"zai","model":"glm-efficient","fallback_models":[]},"balanced":{"provider":"zai","model":"glm-balanced","fallback_models":[]},"thorough":{"provider":"zai","model":"glm-thorough","fallback_models":[]},"advanced":{"provider":"zai","model":"glm-advanced","fallback_models":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ApplyAIExecutionPolicies(publication, raw); err == nil {
				t.Fatal("expected private execution policy to fail closed")
			}
		})
	}
}
