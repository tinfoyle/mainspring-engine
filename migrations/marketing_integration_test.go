package migrations_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestMarketingSchemaEnforcesIsolationImmutableRevisionsAndGovernance(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	owner := openPool(t, ctx, databaseURL, nil)
	defer owner.Close()
	if _, err := migrations.Apply(ctx, owner, migrations.Cell); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC)
	accountA, accountB := "e1000000-0000-4000-8000-000000000001", "e2000000-0000-4000-8000-000000000002"
	userID := "e3000000-0000-4000-8000-000000000003"
	campaignA, campaignB := "e4000000-0000-4000-8000-000000000004", "e5000000-0000-4000-8000-000000000005"
	assetID, revisionID := "e6000000-0000-4000-8000-000000000006", "e7000000-0000-4000-8000-000000000007"
	releaseID, eventID := "e8000000-0000-4000-8000-000000000008", "e9000000-0000-4000-8000-000000000009"
	correlationID := "ea000000-0000-4000-8000-00000000000a"
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at)
		VALUES ($1,1,'active',$4),($2,1,'active',$4);
		INSERT INTO spyglass.marketing_campaigns
		(account_id,id,name,objective,audience,state,version,created_by_kind,created_by_id,origin,created_at,updated_at)
		VALUES ($1,$5,'Launch','Announce the release','Current customers','draft',1,'user',$3,'human',$4,$4),
		       ($2,$6,'Other launch','Announce another release','Other customers','draft',1,'user',$3,'human',$4,$4)`,
		pgx.QueryExecModeSimpleProtocol, accountA, accountB, userID, now, campaignA, campaignB); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.marketing_campaign_channels(account_id,campaign_id,channel)
		VALUES ($1,$2,'email'),($1,$2,'web');
		INSERT INTO spyglass.marketing_assets(account_id,campaign_id,id,created_at) VALUES ($1,$2,$3,$4);
		INSERT INTO spyglass.marketing_asset_revisions
		(account_id,id,campaign_id,asset_id,revision,kind,title,media_type,content_reference,content_sha256,content_bytes,alternative_text,created_by_kind,created_by_id,origin,created_at)
		VALUES ($1,$5,$2,$3,1,'image','Launch image','image/png','marketing/launch/image/v1',decode(repeat('31',32),'hex'),1024,'Product launch illustration','user',$6,'human',$4)`,
		pgx.QueryExecModeSimpleProtocol, accountA, campaignA, assetID, now, revisionID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.marketing_asset_revisions
		(account_id,id,campaign_id,asset_id,revision,kind,title,media_type,content_reference,content_sha256,content_bytes,alternative_text,created_by_kind,created_by_id,origin,created_at)
		VALUES ($1,'eb000000-0000-4000-8000-00000000000b',$2,$3,3,'copy','Skipped revision','text/plain','marketing/launch/copy/v3',decode(repeat('32',32),'hex'),10,'','user',$4,'human',$5)`,
		accountA, campaignA, assetID, userID, now.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "not sequential") {
		t.Fatalf("nonsequential revision=%v", err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.marketing_asset_revisions SET title='Changed' WHERE account_id=$1 AND id=$2`, accountA, revisionID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("revision mutation=%v", err)
	}

	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.marketing_release_plans
		(account_id,id,campaign_id,campaign_version,name,state,version,created_by_kind,created_by_id,origin,created_at,updated_at)
		VALUES ($1,$2,$3,1,'Initial release','draft',1,'user',$4,'human',$5,$5);
		INSERT INTO spyglass.marketing_release_channels(account_id,campaign_id,release_id,channel)
		VALUES ($1,$3,$2,'email'),($1,$3,$2,'web');
		INSERT INTO spyglass.marketing_release_assets(account_id,campaign_id,release_id,asset_revision_id)
		VALUES ($1,$3,$2,$6)`, pgx.QueryExecModeSimpleProtocol, accountA, releaseID, campaignA, userID, now, revisionID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.marketing_release_plans SET name='Rewritten',version=2,updated_at=$3 WHERE account_id=$1 AND id=$2`, accountA, releaseID, now.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "snapshot is immutable") {
		t.Fatalf("release snapshot mutation=%v", err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.marketing_release_plans SET state='submitted',submitted_by_user_id=$3,version=2,updated_at=$4 WHERE account_id=$1 AND id=$2`, accountA, releaseID, userID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.marketing_release_channels SET channel='email' WHERE account_id=$1 AND release_id=$2 AND channel='web'`, accountA, releaseID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("release channel mutation=%v", err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.marketing_events
		(account_id,id,aggregate_kind,aggregate_id,event_type,from_version,to_version,actor_kind,actor_id,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$2,'release',$3,'submitted',1,2,'user',$4,$5,'{"state":"submitted","asset_count":1}',$6)`, accountA, eventID, releaseID, userID, correlationID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.marketing_events
		(account_id,id,aggregate_kind,aggregate_id,event_type,from_version,to_version,actor_kind,actor_id,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,'ec000000-0000-4000-8000-00000000000c','campaign',$2,'created',0,1,'user',$3,$4,'{"objective":"leak"}',$5)`, accountA, campaignA, userID, correlationID, now); err == nil {
		t.Fatal("content-bearing Marketing event was accepted")
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.marketing_events SET redacted_payload='{}' WHERE account_id=$1 AND id=$2`, accountA, eventID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("event mutation=%v", err)
	}

	role := "spyglass_marketing_reader_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA spyglass TO `+role+`;
		GRANT SELECT,INSERT,UPDATE,DELETE ON spyglass.marketing_campaigns TO `+role); err != nil {
		t.Fatal(err)
	}
	reader := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+role)
		return err
	})
	defer func() {
		reader.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+role+`; DROP ROLE IF EXISTS `+role)
	}()
	if _, err := reader.Exec(ctx, `SELECT set_config('app.account_id',$1,false)`, accountA); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := reader.QueryRow(ctx, `SELECT count(*) FROM spyglass.marketing_campaigns`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Account A campaigns=%d err=%v", count, err)
	}
	if _, err := reader.Exec(ctx, `INSERT INTO spyglass.marketing_campaigns
		(account_id,id,name,objective,audience,state,version,created_by_kind,created_by_id,origin,created_at,updated_at)
		VALUES ($1,'ed000000-0000-4000-8000-00000000000d','Cross account','Cross Account objective','Other customers','draft',1,'user',$2,'human',$3,$3)`, accountB, userID, now); err == nil {
		t.Fatal("cross-Account Marketing insert bypassed RLS")
	}
}

func TestMarketingRepositoryReplaysRestoresAndIsolatesDraftLifecycle(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	owner := openPool(t, ctx, databaseURL, nil)
	defer owner.Close()
	if _, err := migrations.Apply(ctx, owner, migrations.Cell); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 23, 4, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("f1000000-0000-4000-8000-000000000001")
	otherAccountID := ids.AccountID("f2000000-0000-4000-8000-000000000002")
	userID := "f3000000-0000-4000-8000-000000000003"
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$3),($2,1,'active',$3)`, accountID, otherAccountID, now); err != nil {
		t.Fatal(err)
	}
	boardroomID := "f3100000-0000-4000-8000-000000000003"
	personaID := "f3200000-0000-4000-8000-000000000003"
	personaVersionID := "f3300000-0000-4000-8000-000000000003"
	conversationID := "f3400000-0000-4000-8000-000000000003"
	runID := "f3500000-0000-4000-8000-000000000003"
	invocationID := "f3600000-0000-4000-8000-000000000003"
	approvalID := ids.ConsequentialApprovalID("f3700000-0000-4000-8000-000000000003")
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.agent_boardrooms(account_id,id,name,purpose,state,version,created_at,updated_at)
		VALUES ($1,$3,'Marketing room','Verify Marketing approval provenance','active',1,$2,$2);
		INSERT INTO spyglass.agent_personas(account_id,id,boardroom_id,state,latest_version,created_at,updated_at)
		VALUES ($1,$4,$3,'active',1,$2,$2);
		INSERT INTO spyglass.agent_persona_versions(account_id,id,persona_id,version,name,role,description,system_instructions,policy,content_digest,created_by,created_at)
		VALUES ($1,$5,$4,1,'Marketing Agent','Marketing','Marketing approval fixture','Prepare a governed Marketing release proposal.','{}',decode(repeat('41',32),'hex'),$6,$2);
		INSERT INTO spyglass.agent_conversations(account_id,id,boardroom_id,subject,state,next_message_sequence,created_by,created_at,updated_at)
		VALUES ($1,$7,$3,'Marketing release approval','open',1,$6,$2,$2);
		INSERT INTO spyglass.agent_runs(account_id,id,boardroom_id,conversation_id,state,entitlement_version,policy_version,plan_digest,turn_count,created_by,created_at,started_at,completed_at)
		VALUES ($1,$8,$3,$7,'succeeded',1,1,decode(repeat('42',32),'hex'),1,$6,$2,$2,$2);
		INSERT INTO spyglass.agent_run_plan_turns(account_id,run_id,turn,persona_id,persona_version_id,persona_digest)
		VALUES ($1,$8,1,$4,$5,decode(repeat('41',32),'hex'));
		INSERT INTO spyglass.agent_invocations(account_id,id,run_id,turn,persona_version_id,status,expected_provider,requested_model,response_model,provider_response_id,runner_result_digest,result_digest,result_payload,input_tokens,output_tokens,total_tokens,queued_at,started_at,completed_at)
		VALUES ($1,$9,$8,1,$5,'succeeded','openai','gpt-test','gpt-test','resp-marketing',decode(repeat('43',32),'hex'),decode(repeat('44',32),'hex'),'{}',1,1,2,$2,$2,$2);
		INSERT INTO spyglass.attention_consequential_approvals
		(account_id,id,operation_id,invocation_id,capability,canonical_payload,input_sha256,hash_version,evidence_sha256,proposer_kind,proposer_id,policy_version,require_independent_review,expires_at,state,decision,decision_reason,decided_by_user_id,decided_at,version,created_at,updated_at)
		VALUES ($1,$10,'f3800000-0000-4000-8000-000000000003',$9,'marketing.release.activate',convert_to('{}','UTF8'),decode(repeat('45',32),'hex'),1,decode(repeat('46',32),'hex'),'workload','runner-invocation:'||$9::text,1,true,$2::timestamptz+interval '1 hour','approved','approve','approved exact Marketing release',$6,$2::timestamptz+interval '7 minutes',2,$2,$2::timestamptz+interval '7 minutes')`,
		pgx.QueryExecModeSimpleProtocol, accountID, now, boardroomID, personaID, personaVersionID, userID, conversationID, runID, invocationID, approvalID); err != nil {
		t.Fatal(err)
	}
	cell, err := database.NewCellPool(owner)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewMarketingRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	actor := marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: userID}
	mutation := func(id, kind string, at time.Time) marketingapp.Mutation {
		return marketingapp.Mutation{EventID: id, Kind: kind, Actor: actor, CorrelationID: id, At: at}
	}

	campaignID := ids.MarketingCampaignID("f4000000-0000-4000-8000-000000000004")
	draft := marketingdomain.CampaignDraftInput{ID: campaignID, AccountID: accountID, Name: "Product launch", Objective: "Announce the release",
		Audience: "Current customers", Channels: []marketingdomain.Channel{marketingdomain.ChannelWeb, marketingdomain.ChannelEmail}, CreatedBy: actor,
		Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman}, CreatedAt: now}
	campaign, created, err := repository.CreateCampaign(ctx, draft, accounts.RoleMember, mutation(string(campaignID), "created", now))
	if err != nil || !created || campaign.Version != 1 || campaign.Channels[0] != marketingdomain.ChannelEmail {
		t.Fatalf("campaign=%+v created=%t err=%v", campaign, created, err)
	}
	retryDraft := draft
	retryDraft.CreatedAt = now.Add(time.Minute)
	replayed, created, err := repository.CreateCampaign(ctx, retryDraft, accounts.RoleMember, mutation(string(campaignID), "created", now.Add(time.Minute)))
	if err != nil || created || !reflect.DeepEqual(replayed, campaign) {
		t.Fatalf("campaign replay=%+v created=%t err=%v", replayed, created, err)
	}
	if _, err := repository.GetCampaign(ctx, otherAccountID, campaignID); !errors.Is(err, marketingapp.ErrNotFound) {
		t.Fatalf("cross-Account campaign=%v", err)
	}
	revisionEvent := "f5000000-0000-4000-8000-000000000005"
	campaign, err = repository.ReviseCampaign(ctx, accountID, campaignID, marketingdomain.CampaignRevision{Name: "Product launch", Objective: "Announce the final release",
		Audience: "Current customers", Channels: []marketingdomain.Channel{marketingdomain.ChannelEmail, marketingdomain.ChannelWeb}, ExpectedVersion: 1,
		Actor: actor, Role: accounts.RoleMember, At: now.Add(2 * time.Minute)}, mutation(revisionEvent, "revised", now.Add(2*time.Minute)))
	if err != nil || campaign.Version != 2 || campaign.Objective != "Announce the final release" {
		t.Fatalf("revised campaign=%+v err=%v", campaign, err)
	}
	secondaryCampaignID := ids.MarketingCampaignID("f4100000-0000-4000-8000-000000000004")
	secondaryDraft := draft
	secondaryDraft.ID, secondaryDraft.Name, secondaryDraft.CreatedAt = secondaryCampaignID, "Secondary launch", now.Add(90*time.Second)
	if _, created, err := repository.CreateCampaign(ctx, secondaryDraft, accounts.RoleMember, mutation(string(secondaryCampaignID), "created", secondaryDraft.CreatedAt)); err != nil || !created {
		t.Fatalf("secondary campaign created=%t err=%v", created, err)
	}
	campaignPage, err := repository.ListCampaigns(ctx, accountID, marketingapp.CampaignListQuery{Limit: 1})
	if err != nil || len(campaignPage.Items) != 1 || campaignPage.Items[0].ID != campaignID || campaignPage.NextCursor == nil {
		t.Fatalf("campaign page=%+v err=%v", campaignPage, err)
	}
	campaignRemainder, err := repository.ListCampaigns(ctx, accountID, marketingapp.CampaignListQuery{After: campaignPage.NextCursor, Limit: 1})
	if err != nil || len(campaignRemainder.Items) != 1 || campaignRemainder.Items[0].ID != secondaryCampaignID || campaignRemainder.NextCursor != nil {
		t.Fatalf("campaign remainder=%+v err=%v", campaignRemainder, err)
	}

	assetID := ids.MarketingAssetID("f6000000-0000-4000-8000-000000000006")
	digest1 := sha256.Sum256([]byte("launch copy one"))
	assetInput := marketingdomain.AssetRevisionInput{ID: "f7000000-0000-4000-8000-000000000007", AccountID: accountID, CampaignID: campaignID, AssetID: assetID,
		Kind: marketingdomain.AssetCopy, Title: "Launch copy", MediaType: "text/plain", ContentReference: "marketing/launch/copy/v1", ContentSHA256: digest1,
		ContentBytes: 15, CreatedBy: actor, Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman}, CreatedAt: now.Add(3 * time.Minute)}
	asset, created, err := repository.CreateAssetRevision(ctx, assetInput, accounts.RoleMember, mutation(string(assetInput.ID), "asset_revised", assetInput.CreatedAt))
	if err != nil || !created || asset.Revision != 1 {
		t.Fatalf("asset=%+v created=%t err=%v", asset, created, err)
	}
	digest2 := sha256.Sum256([]byte("launch copy two"))
	assetInput.ID, assetInput.ContentSHA256, assetInput.ContentReference, assetInput.CreatedAt = "f8000000-0000-4000-8000-000000000008", digest2, "marketing/launch/copy/v2", now.Add(4*time.Minute)
	asset, created, err = repository.CreateAssetRevision(ctx, assetInput, accounts.RoleMember, mutation(string(assetInput.ID), "asset_revised", assetInput.CreatedAt))
	if err != nil || !created || asset.Revision != 2 {
		t.Fatalf("asset revision=%+v created=%t err=%v", asset, created, err)
	}
	assetPage, err := repository.ListAssetRevisions(ctx, accountID, marketingapp.AssetRevisionListQuery{CampaignID: campaignID, AssetID: assetID, Limit: 1})
	if err != nil || len(assetPage.Items) != 1 || assetPage.Items[0].Revision != 2 || assetPage.NextCursor == nil {
		t.Fatalf("asset page=%+v err=%v", assetPage, err)
	}
	assetRemainder, err := repository.ListAssetRevisions(ctx, accountID, marketingapp.AssetRevisionListQuery{CampaignID: campaignID, AssetID: assetID, After: assetPage.NextCursor, Limit: 1})
	if err != nil || len(assetRemainder.Items) != 1 || assetRemainder.Items[0].Revision != 1 || assetRemainder.NextCursor != nil {
		t.Fatalf("asset remainder=%+v err=%v", assetRemainder, err)
	}

	releaseID := ids.MarketingReleaseID("f9000000-0000-4000-8000-000000000009")
	releaseInput := marketingdomain.ReleasePlanInput{ID: releaseID, AccountID: accountID, CampaignID: campaignID, CampaignVersion: campaign.Version,
		Name: "Initial release", Channels: campaign.Channels, AssetRevisionIDs: []ids.MarketingAssetRevisionID{asset.ID}, CreatedBy: actor,
		Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman}, CreatedAt: now.Add(5 * time.Minute)}
	release, created, err := repository.CreateReleasePlan(ctx, releaseInput, accounts.RoleMember, mutation(string(releaseID), "created", releaseInput.CreatedAt))
	if err != nil || !created || release.State != marketingdomain.ReleaseDraft {
		t.Fatalf("release=%+v created=%t err=%v", release, created, err)
	}
	secondaryReleaseID := ids.MarketingReleaseID("f9100000-0000-4000-8000-000000000009")
	secondaryReleaseInput := releaseInput
	secondaryReleaseInput.ID, secondaryReleaseInput.Name, secondaryReleaseInput.CreatedAt = secondaryReleaseID, "Secondary release", now.Add(330*time.Second)
	if _, created, err := repository.CreateReleasePlan(ctx, secondaryReleaseInput, accounts.RoleMember, mutation(string(secondaryReleaseID), "created", secondaryReleaseInput.CreatedAt)); err != nil || !created {
		t.Fatalf("secondary release created=%t err=%v", created, err)
	}
	releasePage, err := repository.ListReleasePlans(ctx, accountID, marketingapp.ReleaseListQuery{CampaignID: campaignID, Limit: 1})
	if err != nil || len(releasePage.Items) != 1 || releasePage.Items[0].ID != secondaryReleaseID || releasePage.NextCursor == nil {
		t.Fatalf("release page=%+v err=%v", releasePage, err)
	}
	releaseRemainder, err := repository.ListReleasePlans(ctx, accountID, marketingapp.ReleaseListQuery{CampaignID: campaignID, After: releasePage.NextCursor, Limit: 1})
	if err != nil || len(releaseRemainder.Items) != 1 || releaseRemainder.Items[0].ID != releaseID || releaseRemainder.NextCursor != nil {
		t.Fatalf("release remainder=%+v err=%v", releaseRemainder, err)
	}
	submitEvent := "fa000000-0000-4000-8000-00000000000a"
	release, err = repository.SubmitRelease(ctx, accountID, releaseID, 1, campaign.Version, actor, accounts.RoleMember,
		mutation(submitEvent, "submitted", now.Add(6*time.Minute)))
	if err != nil || release.State != marketingdomain.ReleaseSubmitted || release.Version != 2 {
		t.Fatalf("submitted release=%+v err=%v", release, err)
	}
	approveEvent := "fb000000-0000-4000-8000-00000000000b"
	release, err = repository.ApproveRelease(ctx, accountID, releaseID, 2, approvalID, actor, accounts.RoleAdministrator,
		mutation(approveEvent, "approved", now.Add(8*time.Minute)))
	if err != nil || release.State != marketingdomain.ReleaseApproved || release.Version != 3 || release.ApprovalID != approvalID {
		t.Fatalf("approved release=%+v err=%v", release, err)
	}
	activateEvent := "fc000000-0000-4000-8000-00000000000c"
	campaign, err = repository.ActivateCampaign(ctx, accountID, campaignID, releaseID, 2, actor, accounts.RoleOwner,
		mutation(activateEvent, "activated", now.Add(9*time.Minute)))
	if err != nil || campaign.State != marketingdomain.CampaignActive || campaign.Version != 3 || campaign.ActiveReleaseID != releaseID {
		t.Fatalf("active campaign=%+v err=%v", campaign, err)
	}
	cancelEvent := "ff000000-0000-4000-8000-00000000000f"
	if _, err := repository.CancelRelease(ctx, accountID, releaseID, 3, actor, accounts.RoleAdministrator,
		mutation("fd000000-0000-4000-8000-00000000000d", "cancelled", now.Add(10*time.Minute))); !errors.Is(err, marketingapp.ErrInvalid) {
		t.Fatalf("active release cancellation=%v", err)
	}
	campaign, err = repository.PauseCampaign(ctx, accountID, campaignID, 3, actor, accounts.RoleAdministrator,
		mutation("fe000000-0000-4000-8000-00000000000e", "paused", now.Add(11*time.Minute)))
	if err != nil || campaign.State != marketingdomain.CampaignPaused || campaign.Version != 4 {
		t.Fatalf("paused campaign=%+v err=%v", campaign, err)
	}
	release, err = repository.CancelRelease(ctx, accountID, releaseID, 3, actor, accounts.RoleAdministrator,
		mutation(cancelEvent, "cancelled", now.Add(12*time.Minute)))
	if err != nil || release.State != marketingdomain.ReleaseCancelled || release.Version != 4 {
		t.Fatalf("cancelled release=%+v err=%v", release, err)
	}
	loaded, err := repository.GetReleasePlan(ctx, accountID, releaseID)
	if err != nil || !reflect.DeepEqual(loaded, release) {
		t.Fatalf("loaded release=%+v err=%v", loaded, err)
	}
}
