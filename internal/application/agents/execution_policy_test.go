package agents

import (
	"testing"

	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
)

const testExecutionPolicies = `{"simple":{"provider":"openai","model":"gpt-simple","fallback_models":[],"reasoning_effort":"low"},"efficient":{"provider":"openai","model":"gpt-efficient","fallback_models":[],"reasoning_effort":"low"},"balanced":{"provider":"openai","model":"gpt-balanced","fallback_models":["gpt-fallback"],"reasoning_effort":"medium"},"thorough":{"provider":"openai","model":"gpt-thorough","fallback_models":[],"reasoning_effort":"high"},"advanced":{"provider":"openai","model":"gpt-advanced","fallback_models":[],"reasoning_effort":"high"}}`

func TestExecutionPolicyResolverRequiresCompletePrivateMapAndCopiesTargets(t *testing.T) {
	resolver, err := ParseExecutionPolicyResolver(testExecutionPolicies)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolver.Resolve(agentdomain.PersonaComplexityBalanced)
	if err != nil || target.Provider != "openai" || target.Model != "gpt-balanced" || len(target.FallbackModels) != 1 {
		t.Fatalf("target=%+v err=%v", target, err)
	}
	target.FallbackModels[0] = "mutated"
	again, _ := resolver.Resolve(agentdomain.PersonaComplexityBalanced)
	if again.FallbackModels[0] != "gpt-fallback" {
		t.Fatal("resolver retained a caller-owned fallback slice")
	}
}

func TestExecutionPolicyResolverFailsClosed(t *testing.T) {
	for _, raw := range []string{
		``,
		`{"balanced":{"provider":"openai","model":"gpt-balanced","fallback_models":[]}}`,
		`{"simple":{"provider":"openai","model":"gpt-simple","fallback_models":[]},"efficient":{"provider":"openai","model":"gpt-efficient","fallback_models":[]},"balanced":{"provider":"openai","model":"gpt-balanced","fallback_models":[],"secret":"leak"},"thorough":{"provider":"openai","model":"gpt-thorough","fallback_models":[]},"advanced":{"provider":"openai","model":"gpt-advanced","fallback_models":[]}}`,
		`{"simple":{"provider":"openai","model":"same","fallback_models":["same"]},"efficient":{"provider":"openai","model":"gpt-efficient","fallback_models":[]},"balanced":{"provider":"openai","model":"gpt-balanced","fallback_models":[]},"thorough":{"provider":"openai","model":"gpt-thorough","fallback_models":[]},"advanced":{"provider":"openai","model":"gpt-advanced","fallback_models":[]}}`,
	} {
		if _, err := ParseExecutionPolicyResolver(raw); err == nil {
			t.Fatalf("accepted invalid execution policies: %s", raw)
		}
	}
}
