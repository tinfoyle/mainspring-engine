// Package oidcauth owns the explicit external-identity linking and login
// boundary. An asserted email address is never sufficient to attach an OIDC
// identity to an existing Spyglass User.
package oidcauth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidAssertion = errors.New("OIDC assertion is invalid")
	ErrIdentityNotFound = errors.New("OIDC identity is not connected")
	ErrIdentityConflict = errors.New("OIDC identity is already connected")
	ErrLastLoginMethod  = errors.New("OIDC identity cannot be disconnected without a local login")
)

type Assertion struct {
	Issuer, Subject, Email, DisplayName string
	EmailVerified                       bool
}

type Repository interface {
	UserForOIDC(context.Context, string) (identity.User, error)
	Connected(context.Context, ids.UserID, string) (bool, error)
	Connect(context.Context, ids.UserID, string, time.Time) error
	Disconnect(context.Context, ids.UserID, string, time.Time) error
}

type Service struct {
	repository Repository
	sessions   *sessions.Service
	clock      sessions.Clock
}

func New(repository Repository, sessionService *sessions.Service, clock sessions.Clock) (*Service, error) {
	if repository == nil || sessionService == nil || clock == nil {
		return nil, errors.New("OIDC authentication dependencies are required")
	}
	return &Service{repository: repository, sessions: sessionService, clock: clock}, nil
}

func (s *Service) Login(ctx context.Context, assertion Assertion, clientLabel string) (sessions.Issued, error) {
	identifier, err := identifier(assertion)
	if err != nil {
		return sessions.Issued{}, err
	}
	user, err := s.repository.UserForOIDC(ctx, identifier)
	if err != nil {
		return sessions.Issued{}, err
	}
	if user.State != identity.UserActive || user.SecurityVersion == 0 {
		return sessions.Issued{}, ErrIdentityNotFound
	}
	return s.sessions.IssueForClientWithMethod(ctx, user.ID, user.SecurityVersion, clientLabel, sessions.AuthenticationMethodOIDC)
}

func (s *Service) Connected(ctx context.Context, userID ids.UserID, issuer string) (bool, error) {
	issuer = strings.TrimSpace(issuer)
	if userID == "" || issuer == "" || len(issuer) > 500 {
		return false, ErrInvalidAssertion
	}
	return s.repository.Connected(ctx, userID, issuer+"\x1f")
}

func (s *Service) Connect(ctx context.Context, session sessions.Session, assertion Assertion) error {
	if err := strongauth.Require(session, session.UserID, s.clock.Now().UTC()); err != nil {
		return err
	}
	identifier, err := identifier(assertion)
	if err != nil {
		return err
	}
	return s.repository.Connect(ctx, session.UserID, identifier, s.clock.Now().UTC())
}

func (s *Service) Disconnect(ctx context.Context, session sessions.Session, issuer string) error {
	if err := strongauth.Require(session, session.UserID, s.clock.Now().UTC()); err != nil {
		return err
	}
	issuer = strings.TrimSpace(issuer)
	if issuer == "" || len(issuer) > 500 {
		return ErrInvalidAssertion
	}
	return s.repository.Disconnect(ctx, session.UserID, issuer+"\x1f", s.clock.Now().UTC())
}

func identifier(assertion Assertion) (string, error) {
	assertion.Issuer = strings.TrimSpace(assertion.Issuer)
	assertion.Subject = strings.TrimSpace(assertion.Subject)
	assertion.Email = strings.TrimSpace(assertion.Email)
	if assertion.Issuer == "" || assertion.Subject == "" || assertion.Email == "" || !assertion.EmailVerified ||
		len(assertion.Issuer) > 500 || len(assertion.Subject) > 500 || strings.ContainsRune(assertion.Issuer, '\x1f') || strings.ContainsRune(assertion.Subject, '\x1f') {
		return "", ErrInvalidAssertion
	}
	return assertion.Issuer + "\x1f" + assertion.Subject, nil
}
