// Package securityposture derives system-wide identity readiness without
// granting Account authority. Account authorization consumes only the boolean
// owner-readiness policy; transports may present the safe aggregate state.
package securityposture

import (
	"context"
	"errors"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var ErrInvalidRequest = errors.New("security posture request is invalid")

type State struct {
	PasskeyCount            int  `json:"passkey_count"`
	RecoveryCodesConfigured bool `json:"recovery_codes_configured"`
	RecoveryCodesRemaining  int  `json:"recovery_codes_remaining"`
	OwnerReady              bool `json:"owner_ready"`
}

type Store interface {
	Status(context.Context, ids.UserID) (State, error)
}

type Service struct{ store Store }

func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{store: store}, nil
}

func (s *Service) Status(ctx context.Context, userID ids.UserID) (State, error) {
	if userID == "" {
		return State{}, ErrInvalidRequest
	}
	state, err := s.store.Status(ctx, userID)
	if err != nil {
		return State{}, err
	}
	if state.PasskeyCount < 0 || state.RecoveryCodesRemaining < 0 || (!state.RecoveryCodesConfigured && state.RecoveryCodesRemaining != 0) {
		return State{}, ErrInvalidRequest
	}
	state.OwnerReady = state.PasskeyCount > 0 && state.RecoveryCodesConfigured && state.RecoveryCodesRemaining > 0
	return state, nil
}

func (s *Service) Ready(ctx context.Context, userID ids.UserID) (bool, error) {
	state, err := s.Status(ctx, userID)
	return state.OwnerReady, err
}
