package marketing

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	testAccountID        ids.AccountID                = "10000000-0000-4000-8000-000000000001"
	testCampaignID       ids.MarketingCampaignID      = "20000000-0000-4000-8000-000000000002"
	testAssetID          ids.MarketingAssetID         = "30000000-0000-4000-8000-000000000003"
	testAssetRevisionID  ids.MarketingAssetRevisionID = "40000000-0000-4000-8000-000000000004"
	testAssetRevision2ID ids.MarketingAssetRevisionID = "50000000-0000-4000-8000-000000000005"
	testReleaseID        ids.MarketingReleaseID       = "60000000-0000-4000-8000-000000000006"
	testApprovalID       ids.ConsequentialApprovalID  = "70000000-0000-4000-8000-000000000007"
)

var (
	testNow         = time.Date(2026, 8, 23, 1, 0, 0, 0, time.UTC)
	testUser        = Actor{Kind: ActorUser, ID: "80000000-0000-4000-8000-000000000008"}
	manager         = Actor{Kind: ActorUser, ID: "90000000-0000-4000-8000-000000000009"}
	agent           = Actor{Kind: ActorWorkload, ID: "agent-projection-worker"}
	agentProvenance = Provenance{Origin: OriginAgent, RunID: "a0000000-0000-4000-8000-00000000000a", InvocationID: "b0000000-0000-4000-8000-00000000000b"}
)

func TestCampaignRequiresGovernedChannelsAndHumanLifecycle(t *testing.T) {
	campaign := validCampaign(t, testUser, Provenance{Origin: OriginHuman})
	if len(campaign.Channels) != 2 || campaign.Channels[0] != ChannelEmail || campaign.Channels[1] != ChannelWeb {
		t.Fatalf("channels=%v", campaign.Channels)
	}
	if _, err := campaign.Pause(1, agent, accounts.RoleOwner, testNow.Add(time.Hour)); !errors.Is(err, ErrState) {
		t.Fatalf("draft pause error=%v", err)
	}
	bad := CampaignDraftInput{ID: testCampaignID, AccountID: testAccountID, Name: "Launch", Objective: "Announce the release", Audience: "Current customers", Channels: []Channel{"sms"}, CreatedBy: testUser, Provenance: Provenance{Origin: OriginHuman}, CreatedAt: testNow}
	if _, err := NewCampaign(bad, accounts.RoleOwner); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown channel error=%v", err)
	}
}

func TestAgentMayDraftWithExactProvenanceButCannotGovern(t *testing.T) {
	campaign := validCampaign(t, agent, agentProvenance)
	if campaign.Provenance.Origin != OriginAgent {
		t.Fatal("agent provenance was not frozen")
	}
	bad := validCampaignInput(agent, Provenance{Origin: OriginAgent})
	if _, err := NewCampaign(bad, accounts.RoleMember); !errors.Is(err, ErrRole) {
		t.Fatalf("missing invocation provenance error=%v", err)
	}
	if _, err := campaign.Archive(1, agent, accounts.RoleOwner, testNow.Add(time.Hour)); !errors.Is(err, ErrRole) {
		t.Fatalf("workload archive error=%v", err)
	}
}

func TestAssetRevisionsAreImmutableOrderedContentBindings(t *testing.T) {
	digest := sha256.Sum256([]byte("launch copy"))
	first, err := NewAssetRevision(AssetRevisionInput{ID: testAssetRevisionID, AccountID: testAccountID, CampaignID: testCampaignID, AssetID: testAssetID,
		Kind: AssetCopy, Title: "Launch copy", MediaType: "text/plain", ContentReference: "marketing/launch/copy/v1", ContentSHA256: digest, ContentBytes: 11,
		CreatedBy: agent, Provenance: agentProvenance, CreatedAt: testNow}, nil, accounts.RoleMember)
	if err != nil || first.Revision != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	secondInput := AssetRevisionInput{ID: testAssetRevision2ID, AccountID: testAccountID, CampaignID: testCampaignID, AssetID: testAssetID,
		Kind: AssetImage, Title: "Launch image", MediaType: "image/png", ContentReference: "marketing/launch/image/v2", ContentSHA256: digest, ContentBytes: 11,
		AlternativeText: "Product launch illustration", CreatedBy: testUser, Provenance: Provenance{Origin: OriginHuman}, CreatedAt: testNow.Add(time.Hour)}
	second, err := NewAssetRevision(secondInput, &first, accounts.RoleMember)
	if err != nil || second.Revision != 2 || first.Title != "Launch copy" {
		t.Fatalf("first=%+v second=%+v err=%v", first, second, err)
	}
	secondInput.AlternativeText = ""
	if _, err := NewAssetRevision(secondInput, &first, accounts.RoleMember); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing image alternative error=%v", err)
	}
}

func TestReleaseFreezesExactSnapshotAndRequiresHumanManagerApproval(t *testing.T) {
	campaign := validCampaign(t, testUser, Provenance{Origin: OriginHuman})
	release, err := NewReleasePlan(ReleasePlanInput{ID: testReleaseID, AccountID: testAccountID, CampaignID: testCampaignID, CampaignVersion: campaign.Version,
		Name: "Initial launch", Channels: []Channel{ChannelWeb, ChannelEmail}, AssetRevisionIDs: []ids.MarketingAssetRevisionID{testAssetRevisionID},
		CreatedBy: agent, Provenance: agentProvenance, CreatedAt: testNow}, accounts.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := release.Submit(1, campaign.Version, agent, accounts.RoleMember, testNow.Add(time.Hour)); !errors.Is(err, ErrRole) {
		t.Fatalf("agent submit error=%v", err)
	}
	submitted, err := release.Submit(1, campaign.Version, testUser, accounts.RoleMember, testNow.Add(time.Hour))
	if err != nil || submitted.State != ReleaseSubmitted {
		t.Fatalf("submitted=%+v err=%v", submitted, err)
	}
	if _, err := submitted.Approve(2, testApprovalID, testUser, accounts.RoleMember, testNow.Add(2*time.Hour)); !errors.Is(err, ErrRole) {
		t.Fatalf("member approval error=%v", err)
	}
	approved, err := submitted.Approve(2, testApprovalID, manager, accounts.RoleAdministrator, testNow.Add(2*time.Hour))
	if err != nil || approved.State != ReleaseApproved || approved.ApprovalID != testApprovalID {
		t.Fatalf("approved=%+v err=%v", approved, err)
	}
	revised, err := campaign.Revise(CampaignRevision{Name: campaign.Name, Objective: campaign.Objective, Audience: campaign.Audience, Channels: campaign.Channels,
		ExpectedVersion: 1, Actor: testUser, Role: accounts.RoleMember, At: testNow.Add(150 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := revised.Activate(approved, 2, manager, accounts.RoleOwner, testNow.Add(3*time.Hour)); !errors.Is(err, ErrApproval) {
		t.Fatalf("changed campaign binding error=%v", err)
	}
	active, err := campaign.Activate(approved, 1, manager, accounts.RoleOwner, testNow.Add(3*time.Hour))
	if err != nil || active.State != CampaignActive || active.ActiveReleaseID != testReleaseID {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	if _, err := campaign.Activate(approved, 2, manager, accounts.RoleOwner, testNow.Add(3*time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale campaign error=%v", err)
	}
	paused, err := active.Pause(2, manager, accounts.RoleAdministrator, testNow.Add(4*time.Hour))
	if err != nil || paused.State != CampaignPaused {
		t.Fatalf("paused=%+v err=%v", paused, err)
	}
}

func validCampaign(t *testing.T, actor Actor, provenance Provenance) Campaign {
	t.Helper()
	value, err := NewCampaign(validCampaignInput(actor, provenance), accounts.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func validCampaignInput(actor Actor, provenance Provenance) CampaignDraftInput {
	return CampaignDraftInput{ID: testCampaignID, AccountID: testAccountID, Name: "Product launch", Objective: "Announce the new product", Audience: "Current customers",
		Channels: []Channel{ChannelWeb, ChannelEmail}, CreatedBy: actor, Provenance: provenance, CreatedAt: testNow}
}
