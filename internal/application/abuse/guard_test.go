package abuse_test

import (
	"context"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
)

type limiter struct{}

func (limiter) Consume(context.Context, abuse.Scope, [32]byte, time.Time, abuse.Policy) (bool, error) {
	return true, nil
}

func TestGuardRejectsMissingActorAndInvalidPolicy(t *testing.T) {
	guard, err := abuse.NewGuard(limiter{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Allow(context.Background(), abuse.ScopeLogin, [32]byte{}, time.Now(), abuse.LoginPolicy); err == nil {
		t.Fatal("expected missing actor to fail closed")
	}
	if _, err := guard.Allow(context.Background(), abuse.ScopeLogin, [32]byte{1}, time.Now(), abuse.Policy{}); err == nil {
		t.Fatal("expected invalid policy to fail closed")
	}
}
