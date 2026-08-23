package marketing

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	testAccountID  ids.AccountID           = "10000000-0000-4000-8000-000000000001"
	testCampaignID ids.MarketingCampaignID = "20000000-0000-4000-8000-000000000002"
	testAssetID    ids.MarketingAssetID    = "30000000-0000-4000-8000-000000000003"
	testRunID      ids.RunID               = "40000000-0000-4000-8000-000000000004"
	testInvocation ids.AgentInvocationID   = "50000000-0000-4000-8000-000000000005"
	testUserID     ids.UserID              = "60000000-0000-4000-8000-000000000006"
)

var testNow = time.Date(2026, 8, 23, 3, 0, 0, 0, time.UTC)

type authorizerStub struct {
	role        accounts.MembershipRole
	requirement access.Requirement
}

func (stub *authorizerStub) Authorize(_ context.Context, _ access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	stub.requirement = requirement
	return access.AccountContext{AccountID: accountID, Role: stub.role}, nil
}

type storeStub struct {
	Store
	createCampaign func(domain.CampaignDraftInput, accounts.MembershipRole, Mutation) (domain.Campaign, bool, error)
	createAsset    func(domain.AssetRevisionInput, accounts.MembershipRole, Mutation) (domain.AssetRevision, bool, error)
	approveRelease func(ids.AccountID, ids.MarketingReleaseID, uint64, ids.ConsequentialApprovalID, domain.Actor, accounts.MembershipRole, Mutation) (domain.ReleasePlan, error)
}

func (stub *storeStub) CreateCampaign(_ context.Context, input domain.CampaignDraftInput, role accounts.MembershipRole, mutation Mutation) (domain.Campaign, bool, error) {
	return stub.createCampaign(input, role, mutation)
}

func (stub *storeStub) CreateAssetRevision(_ context.Context, input domain.AssetRevisionInput, role accounts.MembershipRole, mutation Mutation) (domain.AssetRevision, bool, error) {
	return stub.createAsset(input, role, mutation)
}

func (stub *storeStub) ApproveRelease(_ context.Context, accountID ids.AccountID, releaseID ids.MarketingReleaseID, version uint64, approvalID ids.ConsequentialApprovalID, actor domain.Actor, role accounts.MembershipRole, mutation Mutation) (domain.ReleasePlan, error) {
	return stub.approveRelease(accountID, releaseID, version, approvalID, actor, role, mutation)
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestCreateCampaignAuthorizesMarketingAndDefaultsHumanProvenance(t *testing.T) {
	authorizer := &authorizerStub{role: accounts.RoleMember}
	store := &storeStub{}
	store.createCampaign = func(input domain.CampaignDraftInput, role accounts.MembershipRole, mutation Mutation) (domain.Campaign, bool, error) {
		if role != accounts.RoleMember || input.Provenance.Origin != domain.OriginHuman || input.CreatedBy.Kind != domain.ActorUser ||
			mutation.Kind != "created" || mutation.EventID != string(testCampaignID) {
			t.Fatalf("input=%+v role=%s mutation=%+v", input, role, mutation)
		}
		value, err := domain.NewCampaign(input, role)
		return value, true, err
	}
	service, err := New(authorizer, store, fixedClock{now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	value, created, err := service.CreateCampaign(context.Background(), CreateCampaignCommand{Actor: access.Actor{UserID: testUserID}, AccountID: testAccountID,
		RequestID: string(testCampaignID), Name: "Launch", Objective: "Announce launch", Audience: "Current customers", Channels: []domain.Channel{domain.ChannelEmail}})
	if err != nil || !created || value.State != domain.CampaignDraft || authorizer.requirement.Package != PackageCode || !authorizer.requirement.Mutation {
		t.Fatalf("campaign=%+v created=%t requirement=%+v err=%v", value, created, authorizer.requirement, err)
	}
}

func TestAgentDraftBindsAuthenticatedInvocationAndRejectsSpoof(t *testing.T) {
	authorizer := &authorizerStub{}
	called := false
	store := &storeStub{}
	store.createAsset = func(input domain.AssetRevisionInput, role accounts.MembershipRole, mutation Mutation) (domain.AssetRevision, bool, error) {
		called = true
		if role != "" || input.CreatedBy.Kind != domain.ActorWorkload || input.Provenance.InvocationID != testInvocation || mutation.Actor != input.CreatedBy {
			t.Fatalf("input=%+v role=%s mutation=%+v", input, role, mutation)
		}
		value, err := domain.NewAssetRevision(input, nil, role)
		return value, true, err
	}
	service, _ := New(authorizer, store, fixedClock{now: testNow})
	digest := sha256.Sum256([]byte("launch copy"))
	command := CreateAssetRevisionCommand{Actor: access.Actor{WorkloadID: "runner-invocation:" + string(testInvocation)}, AccountID: testAccountID,
		RequestID: "70000000-0000-4000-8000-000000000007", CampaignID: testCampaignID, AssetID: testAssetID, Kind: domain.AssetCopy,
		Title: "Launch copy", MediaType: "text/plain", ContentReference: "marketing/launch/copy/v1", ContentSHA256: digest, ContentBytes: 11,
		Provenance: domain.Provenance{Origin: domain.OriginAgent, RunID: testRunID, InvocationID: testInvocation}}
	if _, created, err := service.CreateAssetRevision(context.Background(), command); err != nil || !created || !called || len(authorizer.requirement.Roles) != 0 {
		t.Fatalf("created=%t called=%t requirement=%+v err=%v", created, called, authorizer.requirement, err)
	}
	called = false
	command.Provenance.InvocationID = "80000000-0000-4000-8000-000000000008"
	if _, _, err := service.CreateAssetRevision(context.Background(), command); !errors.Is(err, ErrInvalid) || called {
		t.Fatalf("spoof error=%v called=%t", err, called)
	}
}

func TestApprovalRequiresManagerAndPreservesExactAttentionIdentity(t *testing.T) {
	authorizer := &authorizerStub{role: accounts.RoleAdministrator}
	store := &storeStub{}
	releaseID := ids.MarketingReleaseID("90000000-0000-4000-8000-000000000009")
	approvalID := ids.ConsequentialApprovalID("a0000000-0000-4000-8000-00000000000a")
	requestID := "b0000000-0000-4000-8000-00000000000b"
	store.approveRelease = func(accountID ids.AccountID, gotRelease ids.MarketingReleaseID, version uint64, gotApproval ids.ConsequentialApprovalID, actor domain.Actor, role accounts.MembershipRole, mutation Mutation) (domain.ReleasePlan, error) {
		if accountID != testAccountID || gotRelease != releaseID || version != 2 || gotApproval != approvalID || actor.ID != string(testUserID) ||
			role != accounts.RoleAdministrator || mutation.Kind != "approved" || mutation.EventID != requestID {
			t.Fatalf("release=%s approval=%s actor=%+v role=%s mutation=%+v", gotRelease, gotApproval, actor, role, mutation)
		}
		return domain.ReleasePlan{ID: releaseID, State: domain.ReleaseApproved}, nil
	}
	service, _ := New(authorizer, store, fixedClock{now: testNow})
	value, err := service.ApproveRelease(context.Background(), ReleaseTransitionCommand{Actor: access.Actor{UserID: testUserID}, AccountID: testAccountID,
		RequestID: requestID, ReleaseID: releaseID, ExpectedVersion: 2, ApprovalID: approvalID})
	if err != nil || value.State != domain.ReleaseApproved || len(authorizer.requirement.Roles) != 2 {
		t.Fatalf("release=%+v requirement=%+v err=%v", value, authorizer.requirement, err)
	}
}
