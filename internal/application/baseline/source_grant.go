package baseline

import (
	"context"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type GrantSourceCommand struct {
	Actor         access.Actor
	AccountID     ids.AccountID
	AssessmentID  ids.BaselineAssessmentID
	GrantID       ids.BaselineSourceGrantID
	ConnectionID  string
	Kind          domain.SourceKind
	Scope         domain.SourceScope
	CorrelationID string
}

func (s *Service) GrantSource(ctx context.Context, command GrantSourceCommand) (domain.SourceGrant, error) {
	accountContext, actor, err := s.authorizeSources(ctx, command.Actor, command.AccountID, command.AssessmentID, command.CorrelationID, true)
	if err != nil {
		return domain.SourceGrant{}, err
	}
	if s.sources == nil {
		return domain.SourceGrant{}, ErrRepository
	}
	if !canManage(accountContext.Role) {
		return domain.SourceGrant{}, roleDenied()
	}
	assessment, err := s.repository.Get(ctx, command.AccountID, command.AssessmentID)
	if err != nil {
		return domain.SourceGrant{}, err
	}
	if assessment.State != domain.StateActive && assessment.State != domain.StateReady {
		return domain.SourceGrant{}, ErrConstraint
	}
	now := s.clock.Now().UTC()
	grant, err := domain.NewSourceGrant(domain.SourceGrantDraft{ID: command.GrantID, AccountID: command.AccountID, AssessmentID: command.AssessmentID, ConnectionID: command.ConnectionID, Kind: command.Kind, Scope: command.Scope, GrantedBy: actor}, now)
	if err != nil {
		return domain.SourceGrant{}, ErrInvalid
	}
	return s.sources.CreateSourceGrant(ctx, grant, mutation(actor, "source_granted", command.CorrelationID, now))
}

type ListSourceGrantsQuery struct {
	Actor        access.Actor
	AccountID    ids.AccountID
	AssessmentID ids.BaselineAssessmentID
	After        ids.BaselineSourceGrantID
	Limit        uint16
}

func (s *Service) ListSourceGrants(ctx context.Context, query ListSourceGrantsQuery) (SourceGrantPage, error) {
	if s.sources == nil || query.Actor.UserID == "" || query.Actor.WorkloadID != "" || ids.Validate(string(query.Actor.UserID)) != nil || ids.Validate(string(query.AccountID)) != nil || ids.Validate(string(query.AssessmentID)) != nil || (query.After != "" && ids.Validate(string(query.After)) != nil) || query.Limit == 0 || query.Limit > 100 {
		return SourceGrantPage{}, ErrInvalid
	}
	_, err := s.authorizer.Authorize(ctx, query.Actor, query.AccountID, access.Requirement{Package: catalog.PackageIntegrations})
	if err != nil {
		return SourceGrantPage{}, err
	}
	return s.sources.ListSourceGrants(ctx, query.AccountID, query.AssessmentID, query.After, query.Limit)
}

type RevokeSourceCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	AssessmentID    ids.BaselineAssessmentID
	GrantID         ids.BaselineSourceGrantID
	ExpectedVersion uint64
	Reason          string
	CorrelationID   string
}

func (s *Service) RevokeSource(ctx context.Context, command RevokeSourceCommand) (domain.SourceGrant, error) {
	accountContext, actor, err := s.authorizeSources(ctx, command.Actor, command.AccountID, command.AssessmentID, command.CorrelationID, true)
	if err != nil {
		return domain.SourceGrant{}, err
	}
	if s.sources == nil || ids.Validate(string(command.GrantID)) != nil || command.ExpectedVersion == 0 {
		return domain.SourceGrant{}, ErrInvalid
	}
	current, err := s.sources.GetSourceGrant(ctx, command.AccountID, command.GrantID)
	if err != nil {
		return domain.SourceGrant{}, err
	}
	if current.AssessmentID != command.AssessmentID {
		return domain.SourceGrant{}, ErrNotFound
	}
	now := s.clock.Now().UTC()
	updated, err := current.Revoke(actor, accountContext.Role, command.Reason, command.ExpectedVersion, now)
	if err != nil {
		return domain.SourceGrant{}, classifyDomain(err)
	}
	return s.sources.UpdateSourceGrant(ctx, updated, command.ExpectedVersion, mutation(actor, "source_revoked", command.CorrelationID, now))
}

func (s *Service) authorizeSources(ctx context.Context, actor access.Actor, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID, correlationID string, write bool) (access.AccountContext, domain.Actor, error) {
	if !validBase(actor, accountID, assessmentID, correlationID) {
		return access.AccountContext{}, domain.Actor{}, ErrInvalid
	}
	integrationContext, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: catalog.PackageIntegrations, Mutation: write})
	if err != nil {
		return access.AccountContext{}, domain.Actor{}, err
	}
	return integrationContext, domain.Actor{UserID: actor.UserID}, nil
}
