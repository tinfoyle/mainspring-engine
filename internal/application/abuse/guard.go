// Package abuse coordinates distributed request budgets for anonymous actors.
package abuse

import (
	"context"
	"errors"
	"time"
)

type Scope string

const (
	ScopeLogin    Scope = "identity_login"
	ScopeRecovery Scope = "identity_recovery"
)

type Policy struct {
	Limit  int
	Window time.Duration
}

var (
	LoginPolicy    = Policy{Limit: 60, Window: 15 * time.Minute}
	RecoveryPolicy = Policy{Limit: 10, Window: time.Hour}
)

type Limiter interface {
	Consume(context.Context, Scope, [32]byte, time.Time, Policy) (bool, error)
}

type Guard struct{ limiter Limiter }

func NewGuard(limiter Limiter) (*Guard, error) {
	if limiter == nil {
		return nil, errors.New("network abuse limiter is required")
	}
	return &Guard{limiter: limiter}, nil
}

func (g *Guard) Allow(ctx context.Context, scope Scope, actor [32]byte, now time.Time, policy Policy) (bool, error) {
	if scope == "" || actor == ([32]byte{}) || policy.Limit <= 0 || policy.Window <= 0 {
		return false, errors.New("valid network abuse scope, actor, and policy are required")
	}
	return g.limiter.Consume(ctx, scope, actor, now.UTC(), policy)
}
