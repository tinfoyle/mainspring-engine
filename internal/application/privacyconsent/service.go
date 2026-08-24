package privacyconsent

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var ErrNotFound = errors.New("privacy consent decision was not found")

type Clock interface{ Now() time.Time }

type Repository interface {
	Append(context.Context, privacy.Decision) error
	Current(context.Context, ids.ConsentSubjectID, privacy.Surface) (privacy.Decision, error)
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
