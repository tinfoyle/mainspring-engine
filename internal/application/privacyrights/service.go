package privacyrights

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrNotFound      = errors.New("privacy rights request was not found")
	ErrAlreadyOpen   = errors.New("an equivalent privacy rights request is already open")
	ErrNotCancelable = errors.New("privacy rights request can no longer be canceled")
)

type Clock interface{ Now() time.Time }

type Repository interface {
	Create(context.Context, privacy.RightsRequest) error
	List(context.Context, ids.UserID, int) ([]privacy.RightsRequest, error)
	Cancel(context.Context, ids.PrivacyRightsRequestID, ids.UserID, time.Time) (privacy.RightsRequest, error)
}

type Service struct {
	repository Repository
	ids        ids.Generator
	clock      Clock
}

func New(repository Repository, generator ids.Generator, clock Clock) (*Service, error) {
	if repository == nil || generator == nil || clock == nil {
		return nil, errors.New("privacy rights dependencies are required")
	}
	return &Service{repository: repository, ids: generator, clock: clock}, nil
}

type SubmitCommand struct {
	UserID  ids.UserID
	Session sessions.Session
	Kind    privacy.RightsKind
	Scope   privacy.RightsScope
}

func (s *Service) Submit(ctx context.Context, command SubmitCommand) (privacy.RightsRequest, error) {
	if err := strongauth.Require(command.Session, command.UserID, s.clock.Now()); err != nil {
		return privacy.RightsRequest{}, err
	}
	request, err := privacy.NewRightsRequest(ids.PrivacyRightsRequestID(s.ids.New()), command.UserID, command.Kind, command.Scope, s.clock.Now())
	if err != nil {
		return privacy.RightsRequest{}, err
	}
	if err := s.repository.Create(ctx, request); err != nil {
		return privacy.RightsRequest{}, err
	}
	return request, nil
}

func (s *Service) List(ctx context.Context, userID ids.UserID) ([]privacy.RightsRequest, error) {
	if ids.Validate(string(userID)) != nil {
		return nil, privacy.ErrInvalidRightsRequest
	}
	return s.repository.List(ctx, userID, 100)
}

type CancelCommand struct {
	RequestID ids.PrivacyRightsRequestID
	UserID    ids.UserID
	Session   sessions.Session
}

func (s *Service) Cancel(ctx context.Context, command CancelCommand) (privacy.RightsRequest, error) {
	if ids.Validate(string(command.RequestID)) != nil {
		return privacy.RightsRequest{}, privacy.ErrInvalidRightsRequest
	}
	if err := strongauth.Require(command.Session, command.UserID, s.clock.Now()); err != nil {
		return privacy.RightsRequest{}, err
	}
	return s.repository.Cancel(ctx, command.RequestID, command.UserID, s.clock.Now().UTC())
}
