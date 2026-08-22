// Package knowledge orchestrates Account-authorized Knowledge evidence,
// claims, decisions, and redacted fact queries.
package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultLimit = 50
	MaximumLimit = 100
)

var (
	ErrInvalid    = errors.New("knowledge request is invalid")
	ErrNotFound   = errors.New("knowledge record was not found")
	ErrConflict   = errors.New("knowledge state conflicts with the request")
	ErrConstraint = errors.New("knowledge request violates a constraint")
	ErrRepository = errors.New("knowledge repository is unavailable")
)

type Mutation struct {
	Actor         knowledgedomain.Actor
	CorrelationID string
	ReasonCode    string
	At            time.Time
}

type FactSummary struct {
	ID             ids.KnowledgeFactID
	CurrentClaimID ids.KnowledgeClaimID
	Scope          knowledgedomain.Scope
	Key            string
	Sensitivity    knowledgedomain.Sensitivity
	State          knowledgedomain.FactState
	Revision       uint64
	AcceptedAt     time.Time
	UpdatedAt      time.Time
}

type FactCursor struct {
	UpdatedAt time.Time
	ID        ids.KnowledgeFactID
}

type FactListQuery struct {
	Scope          *knowledgedomain.Scope
	KeyPrefix      string
	AfterUpdatedAt *time.Time
	AfterID        ids.KnowledgeFactID
	Limit          int
}

type FactPage struct {
	Items      []FactSummary
	NextCursor *FactCursor
}

type ClaimSummary struct {
	ID          ids.KnowledgeClaimID
	Scope       knowledgedomain.Scope
	Key         string
	Confidence  uint16
	Sensitivity knowledgedomain.Sensitivity
	State       knowledgedomain.ClaimState
	ProposedBy  knowledgedomain.Actor
	Version     uint64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ClaimCursor struct {
	UpdatedAt time.Time
	ID        ids.KnowledgeClaimID
}

type ClaimListQuery struct {
	State          knowledgedomain.ClaimState
	Scope          *knowledgedomain.Scope
	KeyPrefix      string
	AfterUpdatedAt *time.Time
	AfterID        ids.KnowledgeClaimID
	Limit          int
}

type ClaimPage struct {
	Items      []ClaimSummary
	NextCursor *ClaimCursor
}

type Repository interface {
	RegisterEvidence(context.Context, knowledgedomain.Evidence, Mutation) (knowledgedomain.Evidence, error)
	ProposeClaim(context.Context, knowledgedomain.Claim, Mutation) (knowledgedomain.Claim, error)
	GetClaim(context.Context, ids.AccountID, ids.KnowledgeClaimID) (knowledgedomain.Claim, error)
	ListClaims(context.Context, ids.AccountID, ClaimListQuery) (ClaimPage, error)
	DecideClaim(context.Context, ids.AccountID, ids.KnowledgeClaimID, ids.KnowledgeFactID, knowledgedomain.DecideClaimCommand, Mutation) (knowledgedomain.Claim, *knowledgedomain.Fact, error)
	ListFacts(context.Context, ids.AccountID, FactListQuery) (FactPage, error)
}

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	authorizer Authorizer
	repository Repository
	clock      Clock
}

func New(authorizer Authorizer, repository Repository, clock Clock) (*Service, error) {
	if authorizer == nil || repository == nil || clock == nil {
		return nil, errors.New("knowledge dependencies are required")
	}
	return &Service{authorizer: authorizer, repository: repository, clock: clock}, nil
}

type RegisterEvidenceCommand struct {
	Actor                           access.Actor
	AccountID                       ids.AccountID
	EvidenceID                      ids.KnowledgeEvidenceID
	Kind                            knowledgedomain.SourceKind
	SourceReference, SourceRevision string
	ContentSHA256                   [sha256.Size]byte
	CapturedAt                      time.Time
	CorrelationID                   string
}

func (s *Service) RegisterEvidence(ctx context.Context, command RegisterEvidenceCommand) (knowledgedomain.Evidence, error) {
	actor, ok := domainActor(command.Actor)
	if !ok || ids.Validate(string(command.AccountID)) != nil || ids.Validate(string(command.EvidenceID)) != nil || ids.Validate(command.CorrelationID) != nil {
		return knowledgedomain.Evidence{}, ErrInvalid
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, true)
	if err != nil {
		return knowledgedomain.Evidence{}, err
	}
	if actor.Kind == knowledgedomain.ActorUser && !canContribute(accountContext.Role) {
		return knowledgedomain.Evidence{}, &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageKnowledge}
	}
	evidence, err := knowledgedomain.NewEvidence(knowledgedomain.Evidence{ID: command.EvidenceID, AccountID: command.AccountID, Kind: command.Kind, SourceReference: command.SourceReference, SourceRevision: command.SourceRevision, ContentSHA256: command.ContentSHA256, CapturedAt: command.CapturedAt, CreatedBy: actor, CreatedAt: s.clock.Now()})
	if err != nil {
		return knowledgedomain.Evidence{}, ErrInvalid
	}
	return s.repository.RegisterEvidence(ctx, evidence, Mutation{Actor: actor, CorrelationID: command.CorrelationID, ReasonCode: "source_registered", At: evidence.CreatedAt})
}

type ProposeClaimCommand struct {
	Actor          access.Actor
	AccountID      ids.AccountID
	ClaimID        ids.KnowledgeClaimID
	Scope          knowledgedomain.Scope
	Key            string
	CanonicalValue json.RawMessage
	Confidence     uint16
	Sensitivity    knowledgedomain.Sensitivity
	Citations      []knowledgedomain.Citation
	CorrelationID  string
}

func (s *Service) ProposeClaim(ctx context.Context, command ProposeClaimCommand) (knowledgedomain.Claim, error) {
	actor, ok := domainActor(command.Actor)
	if !ok || ids.Validate(string(command.AccountID)) != nil || ids.Validate(string(command.ClaimID)) != nil || ids.Validate(command.CorrelationID) != nil {
		return knowledgedomain.Claim{}, ErrInvalid
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, true)
	if err != nil {
		return knowledgedomain.Claim{}, err
	}
	if actor.Kind == knowledgedomain.ActorUser && !canContribute(accountContext.Role) {
		return knowledgedomain.Claim{}, &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageKnowledge}
	}
	claim, err := knowledgedomain.NewClaim(knowledgedomain.ClaimDraft{ID: command.ClaimID, AccountID: command.AccountID, Scope: command.Scope, Key: command.Key, CanonicalValue: command.CanonicalValue, Confidence: command.Confidence, Sensitivity: command.Sensitivity, Citations: command.Citations, ProposedBy: actor}, s.clock.Now())
	if err != nil {
		return knowledgedomain.Claim{}, ErrInvalid
	}
	return s.repository.ProposeClaim(ctx, claim, Mutation{Actor: actor, CorrelationID: command.CorrelationID, ReasonCode: "claim_proposed", At: claim.CreatedAt})
}

type DecideClaimCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	ClaimID         ids.KnowledgeClaimID
	Accept          bool
	Reason          string
	ExpectedVersion uint64
	CorrelationID   string
}

func (s *Service) DecideClaim(ctx context.Context, command DecideClaimCommand) (knowledgedomain.Claim, *knowledgedomain.Fact, error) {
	actor, ok := domainActor(command.Actor)
	if !ok || actor.Kind != knowledgedomain.ActorUser || ids.Validate(string(command.AccountID)) != nil || ids.Validate(string(command.ClaimID)) != nil || ids.Validate(command.CorrelationID) != nil || command.ExpectedVersion == 0 {
		return knowledgedomain.Claim{}, nil, ErrInvalid
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, true)
	if err != nil {
		return knowledgedomain.Claim{}, nil, err
	}
	factID, err := ids.Derive(command.CorrelationID, "knowledge-fact")
	if err != nil {
		return knowledgedomain.Claim{}, nil, ErrInvalid
	}
	at := s.clock.Now().UTC()
	domainCommand := knowledgedomain.DecideClaimCommand{Accept: command.Accept, Reason: command.Reason, Actor: actor, Role: accountContext.Role, ExpectedVersion: command.ExpectedVersion, At: at}
	reasonCode := "claim_rejected"
	if command.Accept {
		reasonCode = "claim_accepted"
	}
	return s.repository.DecideClaim(ctx, command.AccountID, command.ClaimID, ids.KnowledgeFactID(factID), domainCommand, Mutation{Actor: actor, CorrelationID: command.CorrelationID, ReasonCode: reasonCode, At: at})
}

func (s *Service) GetClaim(ctx context.Context, actor access.Actor, accountID ids.AccountID, claimID ids.KnowledgeClaimID) (knowledgedomain.Claim, error) {
	if _, ok := domainActor(actor); !ok || ids.Validate(string(accountID)) != nil || ids.Validate(string(claimID)) != nil {
		return knowledgedomain.Claim{}, ErrInvalid
	}
	accountContext, err := s.authorize(ctx, actor, accountID, false)
	if err != nil {
		return knowledgedomain.Claim{}, err
	}
	claim, err := s.repository.GetClaim(ctx, accountID, claimID)
	if err != nil {
		return knowledgedomain.Claim{}, err
	}
	if !canReadSensitivity(accountContext.Role, claim.Sensitivity) {
		return knowledgedomain.Claim{}, &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageKnowledge}
	}
	return claim, nil
}

func (s *Service) ListClaims(ctx context.Context, actor access.Actor, accountID ids.AccountID, query ClaimListQuery) (ClaimPage, error) {
	if _, ok := domainActor(actor); !ok || ids.Validate(string(accountID)) != nil || !validClaimQuery(query) {
		return ClaimPage{}, ErrInvalid
	}
	accountContext, err := s.authorize(ctx, actor, accountID, false)
	if err != nil {
		return ClaimPage{}, err
	}
	if query.Limit == 0 {
		query.Limit = DefaultLimit
	}
	if query.Limit < 1 || query.Limit > MaximumLimit {
		return ClaimPage{}, ErrInvalid
	}
	page, err := s.repository.ListClaims(ctx, accountID, query)
	if err != nil {
		return ClaimPage{}, err
	}
	visible := page.Items[:0]
	for _, item := range page.Items {
		if canReadSensitivity(accountContext.Role, item.Sensitivity) {
			visible = append(visible, item)
		}
	}
	page.Items = visible
	return page, nil
}

func (s *Service) ListFacts(ctx context.Context, actor access.Actor, accountID ids.AccountID, query FactListQuery) (FactPage, error) {
	if _, ok := domainActor(actor); !ok || ids.Validate(string(accountID)) != nil || !validFactQuery(query) {
		return FactPage{}, ErrInvalid
	}
	accountContext, err := s.authorize(ctx, actor, accountID, false)
	if err != nil {
		return FactPage{}, err
	}
	if query.Limit == 0 {
		query.Limit = DefaultLimit
	}
	if query.Limit < 1 || query.Limit > MaximumLimit {
		return FactPage{}, ErrInvalid
	}
	page, err := s.repository.ListFacts(ctx, accountID, query)
	if err != nil {
		return FactPage{}, err
	}
	visible := page.Items[:0]
	for _, item := range page.Items {
		if canReadSensitivity(accountContext.Role, item.Sensitivity) {
			visible = append(visible, item)
		}
	}
	page.Items = visible
	return page, nil
}

func (s *Service) authorize(ctx context.Context, actor access.Actor, accountID ids.AccountID, mutation bool) (access.AccountContext, error) {
	return s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: catalog.PackageKnowledge, Mutation: mutation})
}

func domainActor(actor access.Actor) (knowledgedomain.Actor, bool) {
	if actor.UserID != "" && actor.WorkloadID == "" && ids.Validate(string(actor.UserID)) == nil {
		return knowledgedomain.Actor{Kind: knowledgedomain.ActorUser, ID: string(actor.UserID)}, true
	}
	if actor.UserID == "" && actor.WorkloadID != "" {
		return knowledgedomain.Actor{Kind: knowledgedomain.ActorWorkload, ID: actor.WorkloadID}, true
	}
	return knowledgedomain.Actor{}, false
}

func canReadSensitivity(role accounts.MembershipRole, sensitivity knowledgedomain.Sensitivity) bool {
	if sensitivity == knowledgedomain.SensitivityRestricted {
		return role == accounts.RoleOwner || role == accounts.RoleAdministrator
	}
	if sensitivity == knowledgedomain.SensitivityConfidential {
		return role == accounts.RoleOwner || role == accounts.RoleAdministrator || role == accounts.RoleMember
	}
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator || role == accounts.RoleMember || role == accounts.RoleViewer
}

func canContribute(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator || role == accounts.RoleMember
}

func validFactQuery(query FactListQuery) bool {
	if query.Scope != nil && !query.Scope.Valid() {
		return false
	}
	if query.KeyPrefix != "" && (len(query.KeyPrefix) > 128 || strings.TrimSpace(query.KeyPrefix) != query.KeyPrefix) {
		return false
	}
	if (query.AfterUpdatedAt == nil) != (query.AfterID == "") {
		return false
	}
	return query.AfterUpdatedAt == nil || (!query.AfterUpdatedAt.IsZero() && ids.Validate(string(query.AfterID)) == nil)
}

func validClaimQuery(query ClaimListQuery) bool {
	if query.State != "" && query.State != knowledgedomain.ClaimProposed && query.State != knowledgedomain.ClaimAccepted && query.State != knowledgedomain.ClaimRejected && query.State != knowledgedomain.ClaimSuperseded && query.State != knowledgedomain.ClaimStale {
		return false
	}
	return validFactQuery(FactListQuery{Scope: query.Scope, KeyPrefix: query.KeyPrefix, AfterUpdatedAt: query.AfterUpdatedAt, AfterID: ids.KnowledgeFactID(query.AfterID), Limit: query.Limit})
}
