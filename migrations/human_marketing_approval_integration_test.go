package migrations_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	postgres "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	attention "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	marketing "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestHumanMarketingSubmissionApprovalAndStaleDecision(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	pool := openPool(t, ctx, databaseURL, nil)
	defer pool.Close()
	if _, err := migrations.Apply(ctx, pool, migrations.Cell); err != nil {
		t.Fatal(err)
	}
	cell, err := database.NewCellPool(pool)
	if err != nil {
		t.Fatal(err)
	}
	market, err := postgres.NewMarketingRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := postgres.NewAttentionRepository(cell, ids.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	account := ids.AccountID("f1000000-0000-4000-8000-000000000001")
	actor := marketing.Actor{Kind: marketing.ActorUser, ID: "f3000000-0000-4000-8000-000000000003"}
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$2)`, account, now); err != nil {
		t.Fatal(err)
	}
	newID := (ids.RandomGenerator{}).New
	mutation := func(kind string) marketingapp.Mutation {
		return marketingapp.Mutation{EventID: newID(), Kind: kind, Actor: actor, CorrelationID: "f3000000-0000-4000-8000-000000000003", At: now}
	}
	for _, scenario := range []string{"approve", "reject", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			campaign, _, err := market.CreateCampaign(ctx, marketing.CampaignDraftInput{ID: ids.MarketingCampaignID(newID()), AccountID: account, Name: "Audit campaign", Objective: "Test approval", Audience: "Test only", Channels: []marketing.Channel{marketing.ChannelWeb}, CreatedBy: actor, Provenance: marketing.Provenance{Origin: marketing.OriginHuman}, CreatedAt: now}, accounts.RoleOwner, mutation("created"))
			if err != nil {
				t.Fatal(err)
			}
			asset, _, err := market.CreateAssetRevision(ctx, marketing.AssetRevisionInput{ID: ids.MarketingAssetRevisionID(newID()), AccountID: account, CampaignID: campaign.ID, AssetID: ids.MarketingAssetID(newID()), Kind: marketing.AssetCopy, Title: "Audit copy", MediaType: "text/plain", ContentReference: "marketing/audit/copy", ContentSHA256: sha256.Sum256([]byte("test")), ContentBytes: 4, CreatedBy: actor, Provenance: marketing.Provenance{Origin: marketing.OriginHuman}, CreatedAt: now}, accounts.RoleOwner, mutation("asset_revised"))
			if err != nil {
				t.Fatal(err)
			}
			loadedAsset, err := market.GetAssetRevision(ctx, account, asset.ID)
			if err != nil || loadedAsset.ContentSHA256 != asset.ContentSHA256 || loadedAsset.CampaignID != campaign.ID {
				t.Fatalf("asset restore=%+v %v", loadedAsset, err)
			}
			if _, err := market.GetAssetRevision(ctx, "f2000000-0000-4000-8000-000000000002", asset.ID); !errors.Is(err, marketingapp.ErrNotFound) {
				t.Fatalf("cross-account asset=%v", err)
			}
			release, _, err := market.CreateReleasePlan(ctx, marketing.ReleasePlanInput{ID: ids.MarketingReleaseID(newID()), AccountID: account, CampaignID: campaign.ID, CampaignVersion: campaign.Version, Name: "Audit release", Channels: campaign.Channels, AssetRevisionIDs: []ids.MarketingAssetRevisionID{asset.ID}, CreatedBy: actor, Provenance: marketing.Provenance{Origin: marketing.OriginHuman}, CreatedAt: now}, accounts.RoleOwner, mutation("created"))
			if err != nil {
				t.Fatal(err)
			}
			submission := mutation("submitted")
			release, err = market.SubmitRelease(ctx, account, release.ID, 1, campaign.Version, actor, accounts.RoleOwner, submission)
			if err != nil {
				t.Fatal(err)
			}
			// A retried request, including an old already-submitted release, has one approval.
			if _, err := market.SubmitRelease(ctx, account, release.ID, release.Version, campaign.Version, actor, accounts.RoleOwner, mutation("submitted")); err != nil {
				t.Fatal(err)
			}
			approvalID, _ := ids.Derive(submission.EventID, "marketing-release-approval")
			item, err := inbox.GetApproval(ctx, account, ids.ConsequentialApprovalID(approvalID))
			if err != nil || item.InvocationID != "" || item.State != attention.ConsequentialApprovalOpen {
				t.Fatalf("approval=%+v err=%v", item, err)
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM spyglass.attention_consequential_approvals WHERE account_id=$1 AND convert_from(canonical_payload,'UTF8')::jsonb->>'release_id'=$2`, account, string(release.ID)).Scan(&count); err != nil || count != 1 {
				t.Fatalf("approval count=%d err=%v", count, err)
			}
			if _, err := inbox.GetApproval(ctx, "f2000000-0000-4000-8000-000000000002", item.ID); !errors.Is(err, attentionapp.ErrNotFound) {
				t.Fatalf("cross-account read: %v", err)
			}
			decision := attention.DecisionApprove
			if scenario == "reject" {
				decision = attention.DecisionReject
			}
			decided, err := item.Decide(attention.DecideApprovalCommand{ExpectedVersion: item.Version, Actor: attention.Actor{Kind: attention.ActorUser, ID: actor.ID}, Role: accounts.RoleOwner, Decision: decision, Reason: "Reviewed audit release", At: now.Add(time.Minute)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decided.Authorization(now.Add(2 * time.Minute)); err == nil {
				t.Fatal("human approval produced a runner authorization")
			}
			if scenario == "stale" {
				_, err = market.ReviseCampaign(ctx, account, campaign.ID, marketing.CampaignRevision{Name: campaign.Name, Objective: "Changed objective", Audience: campaign.Audience, Channels: campaign.Channels, ExpectedVersion: campaign.Version, Actor: actor, Role: accounts.RoleOwner, At: now}, mutation("revised"))
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = inbox.UpdateApproval(ctx, decided, item.Version, attentionapp.Mutation{Actor: attention.Actor{Kind: attention.ActorUser, ID: actor.ID}, CorrelationID: "human-marketing-test", At: now.Add(time.Minute)})
			if scenario == "stale" {
				if !errors.Is(err, attentionapp.ErrConflict) {
					t.Fatalf("stale approval=%v", err)
				}
				unchanged, err := inbox.GetApproval(ctx, account, item.ID)
				if err != nil || unchanged.State != attention.ConsequentialApprovalOpen {
					t.Fatalf("failed decision was not rolled back: %+v %v", unchanged, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			loaded, err := market.GetCampaign(ctx, account, campaign.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "approve" {
				if loaded.State != marketing.CampaignActive || loaded.ActiveReleaseID != release.ID {
					t.Fatalf("campaign not activated: %+v", loaded)
				}
				approved, err := market.GetReleasePlan(ctx, account, release.ID)
				if err != nil || approved.State != marketing.ReleaseApproved || approved.ApprovalID != item.ID {
					t.Fatalf("release=%+v err=%v", approved, err)
				}
			} else if loaded.State != marketing.CampaignDraft {
				t.Fatalf("unexpected activation: %+v", loaded)
			}
		})
	}
}
