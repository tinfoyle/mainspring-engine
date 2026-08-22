package migrations_test

import (
	"context"
	"crypto/sha256"
	"os"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/prototypemigration"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestPrototypeMigrationSourceTakesCompleteRepeatableSnapshot(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	pool := openPool(t, ctx, databaseURL, nil)
	defer pool.Close()

	if _, err := pool.Exec(ctx, prototypeMigrationFixtureSchema); err != nil {
		t.Fatal(err)
	}
	const tenantID = "91000000-0000-4000-8000-000000000001"
	const documentID = "92000000-0000-4000-8000-000000000002"
	const revisionID = "93000000-0000-4000-8000-000000000003"
	const assessmentID = "94000000-0000-4000-8000-000000000004"
	const requirementID = "95000000-0000-4000-8000-000000000005"
	const evidenceID = "96000000-0000-4000-8000-000000000006"
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	content := "Verified formation record"
	digest := sha256.Sum256([]byte(content))
	for _, command := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO schema_migrations(version) VALUES ('023_business_baselines.sql'),('025_evidence_interview.sql'),('027_business_knowledge_and_input_coordinator.sql'),('029_business_knowledge_fact_history.sql'),('032_agent_managed_documents.sql')`, nil},
		{`INSERT INTO tenant_settings(tenant_id) VALUES ($1)`, []any{tenantID}},
		{`INSERT INTO business_knowledge_facts(fact_key,label,value,scope,source_type,source_ref,confidence,sensitivity,status,confirmed_at,updated_at) VALUES ('organization.legal_name','Legal name','Infinite Ocean LLC','company','owner','',1,'internal','active',$1,$1)`, []any{now}},
		{`INSERT INTO business_knowledge_fact_history(id,fact_key,label,value,scope,source_type,source_ref,confidence,sensitivity,status,recorded_at) VALUES ('97000000-0000-4000-8000-000000000007','organization.legal_name','Legal name','Infinite Ocean LLC','company','owner','',1,'internal','active',$1)`, []any{now}},
		{`INSERT INTO documents(id,status,deleted_at) VALUES ($1,'ready',NULL)`, []any{documentID}},
		{`INSERT INTO document_revisions(id,document_id,revision,name,media_type,content,sha256,change_summary,created_at) VALUES ($1,$2,1,'Formation.txt','text/plain',$3,$4,'Initial',$5)`, []any{revisionID, documentID, content, digest[:], now}},
		{`INSERT INTO document_chunks(id,document_id) VALUES ('98000000-0000-4000-8000-000000000008',$1)`, []any{documentID}},
		{`INSERT INTO baseline_assessments(id,tenant_id,status,phase,baseline_version,next_reassessment_at,created_at,updated_at) VALUES ($1,$2,'ready','baseline_ready',1,$3,$4,$3)`, []any{assessmentID, tenantID, now, now.Add(-time.Hour)}},
		{`INSERT INTO business_facts(id,assessment_id,fact_key,value,source_type,source_ref,confidence,confirmed_at,updated_at) VALUES ('99000000-0000-4000-8000-000000000009',$1,'business_name','Infinite Ocean LLC','owner','',1,$2,$2)`, []any{assessmentID, now}},
		{`INSERT INTO evidence_requirements(id,assessment_id,requirement_key,status,disposition,responsibility,renewal_due_at) VALUES ($1,$2,'identity_registration','confirmed','have_it','owner',$3)`, []any{requirementID, assessmentID, now.Add(90 * 24 * time.Hour)}},
		{`INSERT INTO evidence_links(id,requirement_id,document_id,source_item_id,public_url,verified_at,created_at) VALUES ($1,$2,$3,NULL,'',$4,$4)`, []any{evidenceID, requirementID, documentID, now}},
		{`INSERT INTO baseline_interview_messages(id,assessment_id,role) VALUES ('9a000000-0000-4000-8000-00000000000a',$1,'user')`, []any{assessmentID}},
		{`INSERT INTO baseline_work_items(assessment_id,work_item_id) VALUES ($1,'9b000000-0000-4000-8000-00000000000b')`, []any{assessmentID}},
		{`INSERT INTO baseline_data_sources(id,tenant_id,source_type) VALUES ('9c000000-0000-4000-8000-00000000000c',$1,'email')`, []any{tenantID}},
		{`INSERT INTO baseline_source_items(id,data_source_id) VALUES ('9d000000-0000-4000-8000-00000000000d','9c000000-0000-4000-8000-00000000000c')`, nil},
		{`INSERT INTO baseline_research_runs(id,assessment_id) VALUES ('9e000000-0000-4000-8000-00000000000e',$1)`, []any{assessmentID}},
		{`INSERT INTO baseline_research_results(id,research_run_id) VALUES ('9f000000-0000-4000-8000-00000000000f','9e000000-0000-4000-8000-00000000000e')`, nil},
	} {
		if _, err := pool.Exec(ctx, command.query, command.args...); err != nil {
			t.Fatal(err)
		}
	}

	source, err := postgresadapter.NewPrototypeMigrationSource(pool)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.TenantID != tenantID || snapshot.Checkpoint == "" || len(snapshot.SchemaVersions) != 5 || snapshot.Inventory != (prototypemigration.Inventory{FactVersions: 1, CurrentFacts: 1, DocumentRevisions: 1, Assessments: 1, AssessmentFacts: 1, Requirements: 1, EvidenceLinks: 1, SourceItems: 1, DocumentChunks: 1, InterviewMessages: 1, BaselineWorkLinks: 1, ResearchResults: 1}) {
		t.Fatalf("snapshot metadata=%+v", snapshot)
	}
	if len(snapshot.Documents) != 1 || string(snapshot.Documents[0].Content) != content || len(snapshot.Assessments) != 1 || len(snapshot.Assessments[0].Facts) != 1 || len(snapshot.Assessments[0].Requirements) != 1 || len(snapshot.Assessments[0].Requirements[0].Evidence) != 1 || len(snapshot.Deferred) != 4 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	bundle, err := prototypemigration.NewTransformer().Transform(ids.AccountID("90000000-0000-4000-8000-000000000000"), snapshot)
	if err != nil || bundle.Manifest.Totals.ImportableClaims != 0 || bundle.Manifest.Totals.ImportableDocuments != 1 || bundle.Manifest.Totals.BaselineAssessments != 1 {
		t.Fatalf("bundle totals=%+v err=%v", bundle.Manifest.Totals, err)
	}
}

const prototypeMigrationFixtureSchema = `
CREATE TABLE schema_migrations(version text PRIMARY KEY);
CREATE TABLE tenant_settings(tenant_id uuid PRIMARY KEY);
CREATE TABLE business_knowledge_facts(fact_key text PRIMARY KEY,label text NOT NULL,value text NOT NULL,scope text NOT NULL,source_type text NOT NULL,source_ref text NOT NULL,confidence numeric(4,3) NOT NULL,sensitivity text NOT NULL,status text NOT NULL,confirmed_at timestamptz,updated_at timestamptz NOT NULL);
CREATE TABLE business_knowledge_fact_history(id uuid PRIMARY KEY,fact_key text NOT NULL,label text NOT NULL,value text NOT NULL,scope text NOT NULL,source_type text NOT NULL,source_ref text NOT NULL,confidence numeric(4,3) NOT NULL,sensitivity text NOT NULL,status text NOT NULL,recorded_at timestamptz NOT NULL);
CREATE TABLE documents(id uuid PRIMARY KEY,status text NOT NULL,deleted_at timestamptz);
CREATE TABLE document_revisions(id uuid PRIMARY KEY,document_id uuid NOT NULL REFERENCES documents(id),revision integer NOT NULL,name text NOT NULL,media_type text NOT NULL,content text NOT NULL,sha256 bytea NOT NULL,change_summary text NOT NULL,created_at timestamptz NOT NULL);
CREATE TABLE document_chunks(id uuid PRIMARY KEY,document_id uuid NOT NULL REFERENCES documents(id));
CREATE TABLE baseline_assessments(id uuid PRIMARY KEY,tenant_id uuid NOT NULL REFERENCES tenant_settings(tenant_id),status text NOT NULL,phase text NOT NULL,baseline_version integer NOT NULL,next_reassessment_at timestamptz,created_at timestamptz NOT NULL,updated_at timestamptz NOT NULL);
CREATE TABLE business_facts(id uuid PRIMARY KEY,assessment_id uuid NOT NULL REFERENCES baseline_assessments(id),fact_key text NOT NULL,value text NOT NULL,source_type text NOT NULL,source_ref text NOT NULL,confidence numeric(4,3) NOT NULL,confirmed_at timestamptz,updated_at timestamptz NOT NULL);
CREATE TABLE evidence_requirements(id uuid PRIMARY KEY,assessment_id uuid NOT NULL REFERENCES baseline_assessments(id),requirement_key text NOT NULL,status text NOT NULL,disposition text NOT NULL,responsibility text NOT NULL,renewal_due_at timestamptz);
CREATE TABLE evidence_links(id uuid PRIMARY KEY,requirement_id uuid NOT NULL REFERENCES evidence_requirements(id),document_id uuid,source_item_id uuid,public_url text NOT NULL,verified_at timestamptz,created_at timestamptz NOT NULL);
CREATE TABLE baseline_interview_messages(id uuid PRIMARY KEY,assessment_id uuid NOT NULL REFERENCES baseline_assessments(id),role text NOT NULL);
CREATE TABLE baseline_work_items(assessment_id uuid NOT NULL REFERENCES baseline_assessments(id),work_item_id uuid NOT NULL);
CREATE TABLE baseline_data_sources(id uuid PRIMARY KEY,tenant_id uuid NOT NULL REFERENCES tenant_settings(tenant_id),source_type text NOT NULL);
CREATE TABLE baseline_source_items(id uuid PRIMARY KEY,data_source_id uuid NOT NULL REFERENCES baseline_data_sources(id));
CREATE TABLE baseline_research_runs(id uuid PRIMARY KEY,assessment_id uuid NOT NULL REFERENCES baseline_assessments(id));
CREATE TABLE baseline_research_results(id uuid PRIMARY KEY,research_run_id uuid NOT NULL REFERENCES baseline_research_runs(id));
`
