package migrations_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

type exactKnowledgeExportReader struct {
	want    knowledgeapp.DocumentObjectIdentity
	content []byte
	calls   int
}

func (reader *exactKnowledgeExportReader) Open(_ context.Context, identity knowledgeapp.DocumentObjectIdentity) (io.ReadCloser, error) {
	if identity != reader.want {
		return nil, fmt.Errorf("unexpected Knowledge identity: %+v", identity)
	}
	reader.calls++
	return io.NopCloser(bytes.NewReader(reader.content)), nil
}

type exactMarketingExportReader struct {
	want    marketingdomain.AssetRevision
	content []byte
	calls   int
}

func (reader *exactMarketingExportReader) OpenContent(_ context.Context, revision marketingdomain.AssetRevision) (io.ReadCloser, error) {
	if revision != reader.want {
		return nil, fmt.Errorf("unexpected Marketing revision: %+v", revision)
	}
	reader.calls++
	return io.NopCloser(bytes.NewReader(reader.content)), nil
}

func TestLaunchAccountExportSourcesBuildExactObjectVersions(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	global := openPool(t, ctx, databaseURL, nil)
	defer global.Close()
	cell := openPool(t, ctx, databaseURL, nil)
	defer cell.Close()
	for _, target := range []migrations.Target{migrations.Global, migrations.Development, migrations.Cell} {
		if _, err := migrations.Apply(ctx, global, target); err != nil {
			t.Fatalf("apply %s migrations: %v", target, err)
		}
	}

	accountID := ids.AccountID("ed100000-0000-4000-8000-000000000101")
	ownerID := ids.UserID("ed200000-0000-4000-8000-000000000101")
	documentID := ids.KnowledgeDocumentID("ed300000-0000-4000-8000-000000000101")
	documentRevisionID := ids.KnowledgeDocumentRevisionID("ed400000-0000-4000-8000-000000000101")
	campaignID := ids.MarketingCampaignID("ed500000-0000-4000-8000-000000000101")
	assetID := ids.MarketingAssetID("ed600000-0000-4000-8000-000000000101")
	assetRevisionID := ids.MarketingAssetRevisionID("ed700000-0000-4000-8000-000000000101")
	exportID := "ed800000-0000-4000-8000-000000000101"
	now := time.Now().UTC().Truncate(time.Microsecond)
	knowledgeContent := []byte("portable Knowledge source\n")
	marketingContent := []byte("portable Marketing asset\n")
	knowledgeDigest := sha256.Sum256(knowledgeContent)
	marketingDigest := sha256.Sum256(marketingContent)
	knowledgeKey := fmt.Sprintf("accounts/%s/documents/%s/revisions/%s/source", accountID, documentID, documentRevisionID)
	knowledgeVersion := "knowledge-version-1"
	marketingReference, err := marketingdomain.ContentReferenceForObjectVersion("marketing-version-1")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := global.Exec(ctx, `
		INSERT INTO users(id,primary_email,display_name,state,email_verified_at,created_at)
		VALUES ($1,'export-owner@example.com','Export Owner','active',$3,$3);
		INSERT INTO accounts(id,slug,display_name,account_type,state,cell_id,placement_generation,entitlement_version,last_catalog_reconciled_version,version,created_by_user_id,created_at)
		VALUES ($2,'export-account','Export Account','free','active','cell-us-east-01',3,1,1,7,$1,$3);
		INSERT INTO account_directory(account_id,cell_id,placement_generation,state,data_region,updated_at)
		VALUES ($2,'cell-us-east-01',3,'active','us-east',$3);
		INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at)
		VALUES ($2,3,'active',$3);
		INSERT INTO spyglass.knowledge_documents
			(account_id,id,title,sensitivity,state,version,created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($2,$4,'Portable source','internal','processing',1,'user',$1,$3,$3);
		INSERT INTO spyglass.knowledge_document_revisions
			(account_id,id,document_id,revision,filename,declared_media_type,verified_media_type,byte_size,content_sha256,object_key,object_version,
			 extracted_object_key,extracted_object_version,change_summary,state,scan_state,scan_engine,scan_signature,extraction_state,extractor,index_state,index_generation,failure_code,
			 created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($2,$5,$4,1,'source.txt','text/plain','text/plain',$6,$7,$8,$9,
			'','','','quarantined','pending','','','pending','','pending','','','user',$1,$3,$3);
		INSERT INTO spyglass.marketing_campaigns
			(account_id,id,name,objective,audience,state,version,created_by_kind,created_by_id,origin,created_at,updated_at)
		VALUES ($2,$10,'Portable campaign','Export exact immutable content','Account members','draft',1,'user',$1,'human',$3,$3);
		INSERT INTO spyglass.marketing_assets(account_id,campaign_id,id,created_at) VALUES ($2,$10,$11,$3);
		INSERT INTO spyglass.marketing_asset_revisions
			(account_id,id,campaign_id,asset_id,revision,kind,title,media_type,content_reference,content_sha256,content_bytes,alternative_text,
			 created_by_kind,created_by_id,origin,created_at)
		VALUES ($2,$12,$10,$11,1,'copy','Portable copy','text/plain',$13,$14,$15,'','user',$1,'human',$3)`,
		pgx.QueryExecModeSimpleProtocol, ownerID, accountID, now, documentID, documentRevisionID, len(knowledgeContent), knowledgeDigest[:], knowledgeKey,
		knowledgeVersion, campaignID, assetID, assetRevisionID, marketingReference, marketingDigest[:], len(marketingContent)); err != nil {
		t.Fatal(err)
	}

	knowledgeIdentity := knowledgeapp.DocumentObjectIdentity{Key: knowledgeKey, Version: knowledgeVersion, Size: int64(len(knowledgeContent)), ContentSHA256: knowledgeDigest}
	marketingRevision := marketingdomain.AssetRevision{ID: assetRevisionID, AccountID: accountID, CampaignID: campaignID, AssetID: assetID, Revision: 1,
		Kind: marketingdomain.AssetCopy, Title: "Portable copy", MediaType: "text/plain", ContentReference: marketingReference,
		ContentSHA256: marketingDigest, ContentBytes: uint64(len(marketingContent)), CreatedBy: marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: string(ownerID)},
		Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman}, CreatedAt: now}
	knowledgeReader := &exactKnowledgeExportReader{want: knowledgeIdentity, content: knowledgeContent}
	marketingReader := &exactMarketingExportReader{want: marketingRevision, content: marketingContent}
	factory, err := postgresadapter.NewLaunchAccountExportSourceFactory(knowledgeReader, marketingReader)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := postgresadapter.NewAccountExportSnapshotCoordinator(global, cell, "cell-us-east-01", factory)
	if err != nil {
		t.Fatal(err)
	}
	work := accountexport.Work{ID: exportID, AccountID: accountID, RequestedBy: ownerID, CellID: "cell-us-east-01", PlacementGeneration: 3,
		AccountVersion: 7, Version: 2, LeaseID: "ed900000-0000-4000-8000-000000000101", RequestedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	var artifact bytes.Buffer
	err = coordinator.WithSnapshot(ctx, work, func(snapshot accountexport.Snapshot, sections []accountexport.SectionSource, objects []accountexport.ObjectSource) error {
		registry, registryErr := accountexport.LaunchRegistry()
		if registryErr != nil {
			return registryErr
		}
		builder, builderErr := accountexport.NewBuilder(registry, sections, objects)
		if builderErr != nil {
			return builderErr
		}
		_, builderErr = builder.Build(ctx, accountexport.BuildRequest{AccountID: accountID, ExportID: exportID, RequestedBy: ownerID,
			RequestedAt: now, ExpiresAt: now.Add(24 * time.Hour), Snapshot: snapshot}, &artifact)
		return builderErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if knowledgeReader.calls != 1 || marketingReader.calls != 1 {
		t.Fatalf("exact reader calls Knowledge=%d Marketing=%d", knowledgeReader.calls, marketingReader.calls)
	}

	archive, err := zip.NewReader(bytes.NewReader(artifact.Bytes()), int64(artifact.Len()))
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string][]byte, len(archive.File))
	for _, file := range archive.File {
		body, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		entries[file.Name], openErr = io.ReadAll(body)
		closeErr := body.Close()
		if openErr != nil || closeErr != nil {
			t.Fatalf("read %s: %v / %v", file.Name, openErr, closeErr)
		}
	}
	knowledgePath := fmt.Sprintf("objects/knowledge_objects/documents/%s/revisions/%s/source", documentID, documentRevisionID)
	marketingPath := fmt.Sprintf("objects/marketing_objects/campaigns/%s/assets/%s/revisions/%s/content", campaignID, assetID, assetRevisionID)
	if !bytes.Equal(entries[knowledgePath], knowledgeContent) || !bytes.Equal(entries[marketingPath], marketingContent) {
		t.Fatalf("object entries do not contain exact source bytes")
	}
	var manifest accountexport.Manifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Sections) != 14 || len(manifest.Objects) != 2 || manifest.Objects[0].Path != knowledgePath || manifest.Objects[1].Path != marketingPath {
		t.Fatalf("manifest sections=%d objects=%+v", len(manifest.Sections), manifest.Objects)
	}
	if strings.Contains(string(entries["sections/knowledge.jsonl"]), "object_key") || strings.Contains(string(entries["sections/knowledge.jsonl"]), "object_version") ||
		strings.Contains(string(entries["sections/marketing.jsonl"]), "content_reference") {
		t.Fatal("internal object locators entered database projections")
	}
}
