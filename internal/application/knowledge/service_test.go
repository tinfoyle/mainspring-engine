package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const appKnowledgeAccount ids.AccountID = "10000000-0000-4000-8000-000000000001"
const appKnowledgeUser ids.UserID = "20000000-0000-4000-8000-000000000002"
const appKnowledgeEvidence ids.KnowledgeEvidenceID = "30000000-0000-4000-8000-000000000003"
const appKnowledgeClaim ids.KnowledgeClaimID = "40000000-0000-4000-8000-000000000004"
const appKnowledgeOperation = "50000000-0000-4000-8000-000000000005"

type knowledgeAuthorizer struct {
	account     access.AccountContext
	requirement access.Requirement
}

func (a *knowledgeAuthorizer) Authorize(_ context.Context, _ access.Actor, _ ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	a.requirement = requirement
	return a.account, nil
}

type knowledgeClock struct{ now time.Time }

func (clock knowledgeClock) Now() time.Time { return clock.now }

type knowledgeRepository struct {
	evidence knowledgedomain.Evidence
	claim    knowledgedomain.Claim
	decision knowledgedomain.DecideClaimCommand
	factID   ids.KnowledgeFactID
	page     FactPage
}

func (repository *knowledgeRepository) RegisterEvidence(_ context.Context, value knowledgedomain.Evidence, _ Mutation) (knowledgedomain.Evidence, error) {
	repository.evidence = value
	return value, nil
}
func (repository *knowledgeRepository) ProposeClaim(_ context.Context, value knowledgedomain.Claim, _ Mutation) (knowledgedomain.Claim, error) {
	repository.claim = value
	return value, nil
}
func (repository *knowledgeRepository) GetClaim(context.Context, ids.AccountID, ids.KnowledgeClaimID) (knowledgedomain.Claim, error) {
	return repository.claim, nil
}
func (repository *knowledgeRepository) DecideClaim(_ context.Context, _ ids.AccountID, _ ids.KnowledgeClaimID, factID ids.KnowledgeFactID, command knowledgedomain.DecideClaimCommand, _ Mutation) (knowledgedomain.Claim, *knowledgedomain.Fact, error) {
	repository.factID, repository.decision = factID, command
	return repository.claim, nil, nil
}
func (repository *knowledgeRepository) ListFacts(context.Context, ids.AccountID, FactListQuery) (FactPage, error) {
	return repository.page, nil
}

func knowledgeFixture(t *testing.T) (*Service, *knowledgeAuthorizer, *knowledgeRepository, knowledgeClock) {
	t.Helper()
	authorizer := &knowledgeAuthorizer{account: access.AccountContext{Role: accounts.RoleOwner}}
	repository := &knowledgeRepository{}
	clock := knowledgeClock{now: time.Date(2026, 8, 21, 22, 0, 0, 0, time.UTC)}
	service, err := New(authorizer, repository, clock)
	if err != nil {
		t.Fatal(err)
	}
	return service, authorizer, repository, clock
}

func TestRegisterEvidenceBindsActorPackageAndExactDigest(t *testing.T) {
	service, authorizer, repository, clock := knowledgeFixture(t)
	digest := sha256.Sum256([]byte("Northstar LLC"))
	value, err := service.RegisterEvidence(context.Background(), RegisterEvidenceCommand{Actor: access.Actor{UserID: appKnowledgeUser}, AccountID: appKnowledgeAccount, EvidenceID: appKnowledgeEvidence, Kind: knowledgedomain.SourceOwnerStatement, SourceReference: "membership:" + string(appKnowledgeUser), SourceRevision: "1", ContentSHA256: digest, CapturedAt: clock.now, CorrelationID: appKnowledgeOperation})
	if err != nil || value.ContentSHA256 != digest || repository.evidence.CreatedBy.ID != string(appKnowledgeUser) || !authorizer.requirement.Mutation || authorizer.requirement.Package != "knowledge" {
		t.Fatalf("evidence=%+v requirement=%+v err=%v", value, authorizer.requirement, err)
	}
}

func TestWorkloadCanProposeButCannotDecide(t *testing.T) {
	service, _, repository, _ := knowledgeFixture(t)
	claim, err := service.ProposeClaim(context.Background(), ProposeClaimCommand{Actor: access.Actor{WorkloadID: "agent:analyst"}, AccountID: appKnowledgeAccount, ClaimID: appKnowledgeClaim, Scope: knowledgedomain.Scope{Kind: knowledgedomain.ScopeAccount}, Key: "organization.legal_name", CanonicalValue: json.RawMessage(`"Northstar LLC"`), Confidence: 800, Sensitivity: knowledgedomain.SensitivityInternal, Citations: []knowledgedomain.Citation{{EvidenceID: appKnowledgeEvidence, EvidenceKind: knowledgedomain.SourceOwnerStatement, Relation: knowledgedomain.EvidenceSupports, Locator: "registration"}}, CorrelationID: appKnowledgeOperation})
	if err != nil || claim.ProposedBy.Kind != knowledgedomain.ActorWorkload || repository.claim.ID != appKnowledgeClaim {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	_, _, err = service.DecideClaim(context.Background(), DecideClaimCommand{Actor: access.Actor{WorkloadID: "agent:analyst"}, AccountID: appKnowledgeAccount, ClaimID: appKnowledgeClaim, Accept: true, Reason: "Reviewed", ExpectedVersion: 1, CorrelationID: appKnowledgeOperation})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("workload decision err=%v", err)
	}
}

func TestDecisionUsesMembershipRoleAndStableDerivedFactIdentity(t *testing.T) {
	service, authorizer, repository, clock := knowledgeFixture(t)
	repository.claim = knowledgedomain.Claim{ClaimDraft: knowledgedomain.ClaimDraft{ID: appKnowledgeClaim}}
	_, _, err := service.DecideClaim(context.Background(), DecideClaimCommand{Actor: access.Actor{UserID: appKnowledgeUser}, AccountID: appKnowledgeAccount, ClaimID: appKnowledgeClaim, Accept: true, Reason: "Registration inspected", ExpectedVersion: 1, CorrelationID: appKnowledgeOperation})
	want, _ := ids.Derive(appKnowledgeOperation, "knowledge-fact")
	if err != nil || repository.factID != ids.KnowledgeFactID(want) || repository.decision.Role != accounts.RoleOwner || !repository.decision.At.Equal(clock.now) || !authorizer.requirement.Mutation {
		t.Fatalf("fact=%s decision=%+v requirement=%+v err=%v", repository.factID, repository.decision, authorizer.requirement, err)
	}
}

func TestRestrictedClaimRequiresManagingRole(t *testing.T) {
	service, authorizer, repository, clock := knowledgeFixture(t)
	authorizer.account.Role = accounts.RoleMember
	claim, err := knowledgedomain.NewClaim(knowledgedomain.ClaimDraft{ID: appKnowledgeClaim, AccountID: appKnowledgeAccount, Scope: knowledgedomain.Scope{Kind: knowledgedomain.ScopeAccount}, Key: "security.recovery", CanonicalValue: json.RawMessage(`"restricted"`), Confidence: 1000, Sensitivity: knowledgedomain.SensitivityRestricted, Citations: []knowledgedomain.Citation{{EvidenceID: appKnowledgeEvidence, EvidenceKind: knowledgedomain.SourceOwnerStatement, Relation: knowledgedomain.EvidenceSupports, Locator: "statement"}}, ProposedBy: knowledgedomain.Actor{Kind: knowledgedomain.ActorUser, ID: string(appKnowledgeUser)}}, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	repository.claim = claim
	if _, err := service.GetClaim(context.Background(), access.Actor{UserID: appKnowledgeUser}, appKnowledgeAccount, appKnowledgeClaim); !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("restricted read err=%v", err)
	}
}

func TestViewerCannotContributeAndCursorMustBeComplete(t *testing.T) {
	service, authorizer, _, clock := knowledgeFixture(t)
	authorizer.account.Role = accounts.RoleViewer
	digest := sha256.Sum256([]byte("statement"))
	_, err := service.RegisterEvidence(context.Background(), RegisterEvidenceCommand{Actor: access.Actor{UserID: appKnowledgeUser}, AccountID: appKnowledgeAccount, EvidenceID: appKnowledgeEvidence, Kind: knowledgedomain.SourceOwnerStatement, SourceReference: "statement", SourceRevision: "1", ContentSHA256: digest, CapturedAt: clock.now, CorrelationID: appKnowledgeOperation})
	if !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("viewer contribution err=%v", err)
	}
	_, err = service.ListFacts(context.Background(), access.Actor{UserID: appKnowledgeUser}, appKnowledgeAccount, FactListQuery{AfterID: "60000000-0000-4000-8000-000000000006"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("partial cursor err=%v", err)
	}
}

func TestListFactsOmitsSensitivitiesAboveMembershipRole(t *testing.T) {
	service, authorizer, repository, _ := knowledgeFixture(t)
	authorizer.account.Role = accounts.RoleMember
	repository.page = FactPage{
		Items: []FactSummary{
			{ID: "60000000-0000-4000-8000-000000000006", Sensitivity: knowledgedomain.SensitivityInternal},
			{ID: "70000000-0000-4000-8000-000000000007", Sensitivity: knowledgedomain.SensitivityRestricted},
		},
		NextCursor: &FactCursor{ID: "70000000-0000-4000-8000-000000000007", UpdatedAt: time.Date(2026, 8, 21, 21, 0, 0, 0, time.UTC)},
	}
	page, err := service.ListFacts(context.Background(), access.Actor{UserID: appKnowledgeUser}, appKnowledgeAccount, FactListQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Sensitivity != knowledgedomain.SensitivityInternal || page.NextCursor == nil {
		t.Fatalf("filtered page=%+v err=%v", page, err)
	}
}
