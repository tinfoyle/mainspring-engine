package migrations_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

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
