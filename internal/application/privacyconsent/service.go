package privacyconsent

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrNotFound           = errors.New("privacy consent decision was not found")
	ErrSubjectOwned       = errors.New("privacy consent subject belongs to another user")
	ErrPurposeUnavailable = errors.New("privacy consent purpose is not available")
)

type Clock interface{ Now() time.Time }

type Repository interface {
	Append(context.Context, privacy.Decision) error
	Current(context.Context, ids.ConsentSubjectID, privacy.Surface) (privacy.Decision, error)
	History(context.Context, ids.ConsentSubjectID, int) ([]privacy.Decision, error)
	Erase(context.Context, ids.ConsentSubjectID) error
	Link(context.Context, ids.ConsentSubjectID, ids.UserID, time.Time) error
}

type Service struct {
	repository    Repository
	ids           ids.Generator
	clock         Clock
	policyVersion uint64
}

func New(repository Repository, generator ids.Generator, clock Clock, policyVersion uint64) (*Service, error) {
	if repository == nil || generator == nil || clock == nil || policyVersion == 0 {
		return nil, errors.New("privacy consent dependencies and policy version are required")
	}
	return &Service{repository: repository, ids: generator, clock: clock, policyVersion: policyVersion}, nil
}

type SetCommand struct {
	SubjectID ids.ConsentSubjectID
	Surface   privacy.Surface
	Analytics bool
	Marketing bool
}

func (s *Service) Set(ctx context.Context, command SetCommand) (privacy.Decision, error) {
	// Marketing is reserved in the consent schema, but the launch registry has no
	// marketing processor, event, cookie or destination. Do not manufacture a
	// consent receipt for a purpose the customer cannot meaningfully authorize.
	if command.Marketing {
		return privacy.Decision{}, ErrPurposeUnavailable
	}
	subjectID := command.SubjectID
	if subjectID == "" {
		subjectID = ids.ConsentSubjectID(s.ids.New())
	}
	decision, err := privacy.NewDecision(ids.ConsentDecisionID(s.ids.New()), subjectID, s.policyVersion, command.Surface, command.Analytics, command.Marketing, s.clock.Now())
	if err != nil {
		return privacy.Decision{}, err
	}
	if err := s.repository.Append(ctx, decision); err != nil {
		return privacy.Decision{}, err
	}
	return decision, nil
}

func (s *Service) Current(ctx context.Context, subjectID ids.ConsentSubjectID, surface privacy.Surface) (privacy.Decision, error) {
	if ids.Validate(string(subjectID)) != nil || (surface != privacy.SurfacePublic && surface != privacy.SurfacePrivate) {
		return privacy.Decision{}, privacy.ErrInvalidDecision
	}
	return s.repository.Current(ctx, subjectID, surface)
}

func (s *Service) PolicyVersion() uint64 { return s.policyVersion }

func (s *Service) History(ctx context.Context, subjectID ids.ConsentSubjectID) ([]privacy.Decision, error) {
	if ids.Validate(string(subjectID)) != nil {
		return nil, privacy.ErrInvalidDecision
	}
	return s.repository.History(ctx, subjectID, 1000)
}

func (s *Service) Erase(ctx context.Context, subjectID ids.ConsentSubjectID) error {
	if ids.Validate(string(subjectID)) != nil {
		return privacy.ErrInvalidDecision
	}
	return s.repository.Erase(ctx, subjectID)
}

func (s *Service) Link(ctx context.Context, subjectID ids.ConsentSubjectID, userID ids.UserID) error {
	if ids.Validate(string(subjectID)) != nil || ids.Validate(string(userID)) != nil {
		return privacy.ErrInvalidDecision
	}
	return s.repository.Link(ctx, subjectID, userID, s.clock.Now().UTC())
}
