package securityposture_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/securityposture"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type store struct {
	state securityposture.State
	err   error
}

func (s store) Status(context.Context, ids.UserID) (securityposture.State, error) {
	return s.state, s.err
}

func TestOwnerReadinessRequiresPasskeyAndUnusedRecoveryCode(t *testing.T) {
	userID := ids.UserID("10000000-0000-4000-8000-000000000001")
	for name, value := range map[string]struct {
		state securityposture.State
		ready bool
	}{
		"nothing enrolled": {state: securityposture.State{}},
		"passkey only":     {state: securityposture.State{PasskeyCount: 1}},
		"codes only":       {state: securityposture.State{RecoveryCodesConfigured: true, RecoveryCodesRemaining: 10}},
		"codes exhausted":  {state: securityposture.State{PasskeyCount: 1, RecoveryCodesConfigured: true}},
		"fully ready":      {state: securityposture.State{PasskeyCount: 1, RecoveryCodesConfigured: true, RecoveryCodesRemaining: 1}, ready: true},
	} {
		t.Run(name, func(t *testing.T) {
			service, _ := securityposture.NewService(store{state: value.state})
			state, err := service.Status(context.Background(), userID)
			if err != nil || state.OwnerReady != value.ready {
				t.Fatalf("state=%+v err=%v", state, err)
			}
		})
	}
}

func TestSecurityPostureRejectsInvalidOrUnavailableState(t *testing.T) {
	service, _ := securityposture.NewService(store{state: securityposture.State{RecoveryCodesRemaining: 1}})
	if _, err := service.Status(context.Background(), ""); !errors.Is(err, securityposture.ErrInvalidRequest) {
		t.Fatalf("empty user error=%v", err)
	}
	if _, err := service.Status(context.Background(), "user"); !errors.Is(err, securityposture.ErrInvalidRequest) {
		t.Fatalf("inconsistent state error=%v", err)
	}
}
