package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/prototypemigration"
)

// PrototypeMigrationSource reads one retained per-tenant prototype database
// under a repeatable-read, read-only transaction. It has no final Spyglass
// database authority.
type PrototypeMigrationSource struct{ pool *pgxpool.Pool }

func NewPrototypeMigrationSource(pool *pgxpool.Pool) (*PrototypeMigrationSource, error) {
	if pool == nil {
		return nil, errors.New("prototype migration source pool is required")
	}
	return &PrototypeMigrationSource{pool: pool}, nil
}

func (source *PrototypeMigrationSource) Snapshot(ctx context.Context, tenantID string) (prototypemigration.Snapshot, error) {
	tx, err := source.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return prototypemigration.Snapshot{}, fmt.Errorf("begin prototype snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var actualTenant, checkpoint string
	if err := tx.QueryRow(ctx, `SELECT tenant_id::text FROM public.tenant_settings WHERE tenant_id=$1`, tenantID).Scan(&actualTenant); err != nil {
		return prototypemigration.Snapshot{}, fmt.Errorf("resolve prototype tenant: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text||'@'||txid_current_snapshot()::text`).Scan(&checkpoint); err != nil {
		return prototypemigration.Snapshot{}, fmt.Errorf("capture prototype checkpoint: %w", err)
	}
	result := prototypemigration.Snapshot{TenantID: actualTenant, Checkpoint: checkpoint}
	if result.SchemaVersions, err = scanStrings(ctx, tx, `SELECT version FROM public.schema_migrations ORDER BY version`); err != nil {
		return prototypemigration.Snapshot{}, fmt.Errorf("scan prototype schema versions: %w", err)
	}
	if !hasPrototypeMigrationSchema(result.SchemaVersions) {
		return prototypemigration.Snapshot{}, errors.New("prototype database is missing required Knowledge, Baseline, or document schema versions")
	}
	if err := scanPrototypeInventory(ctx, tx, tenantID, &result.Inventory); err != nil {
		return prototypemigration.Snapshot{}, err
	}
	if result.FactVersions, err = scanPrototypeFactVersions(ctx, tx); err != nil {
		return prototypemigration.Snapshot{}, err
	}
	if result.CurrentFacts, err = scanPrototypeCurrentFacts(ctx, tx); err != nil {
		return prototypemigration.Snapshot{}, err
	}
	if result.Documents, err = scanPrototypeDocuments(ctx, tx); err != nil {
		return prototypemigration.Snapshot{}, err
	}
	if result.Assessments, err = scanPrototypeAssessments(ctx, tx, tenantID); err != nil {
		return prototypemigration.Snapshot{}, err
	}
	if result.Deferred, err = scanPrototypeDeferred(ctx, tx, tenantID); err != nil {
		return prototypemigration.Snapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return prototypemigration.Snapshot{}, fmt.Errorf("commit prototype snapshot: %w", err)
	}
	return result, nil
}

func hasPrototypeMigrationSchema(versions []string) bool {
	required := map[string]bool{
		"023_business_baselines.sql":                       false,
		"025_evidence_interview.sql":                       false,
		"027_business_knowledge_and_input_coordinator.sql": false,
		"029_business_knowledge_fact_history.sql":          false,
		"032_agent_managed_documents.sql":                  false,
	}
	for _, version := range versions {
		if _, exists := required[version]; exists {
			required[version] = true
		}
	}
	for _, present := range required {
		if !present {
			return false
		}
	}
	return true
}

func scanPrototypeInventory(ctx context.Context, tx pgx.Tx, tenantID string, result *prototypemigration.Inventory) error {
	queries := []struct {
		target *uint64
		query  string
		args   []any
	}{
		{&result.FactVersions, `SELECT count(*) FROM public.business_knowledge_fact_history`, nil},
		{&result.CurrentFacts, `SELECT count(*) FROM public.business_knowledge_facts`, nil},
		{&result.DocumentRevisions, `SELECT count(*) FROM public.document_revisions`, nil},
		{&result.Assessments, `SELECT count(*) FROM public.baseline_assessments WHERE tenant_id=$1`, []any{tenantID}},
		{&result.AssessmentFacts, `SELECT count(*) FROM public.business_facts f JOIN public.baseline_assessments a ON a.id=f.assessment_id WHERE a.tenant_id=$1`, []any{tenantID}},
		{&result.Requirements, `SELECT count(*) FROM public.evidence_requirements r JOIN public.baseline_assessments a ON a.id=r.assessment_id WHERE a.tenant_id=$1`, []any{tenantID}},
		{&result.EvidenceLinks, `SELECT count(*) FROM public.evidence_links e JOIN public.evidence_requirements r ON r.id=e.requirement_id JOIN public.baseline_assessments a ON a.id=r.assessment_id WHERE a.tenant_id=$1`, []any{tenantID}},
		{&result.SourceItems, `SELECT count(*) FROM public.baseline_source_items i JOIN public.baseline_data_sources s ON s.id=i.data_source_id WHERE s.tenant_id=$1`, []any{tenantID}},
		{&result.DocumentChunks, `SELECT count(*) FROM public.document_chunks`, nil},
		{&result.InterviewMessages, `SELECT count(*) FROM public.baseline_interview_messages m JOIN public.baseline_assessments a ON a.id=m.assessment_id WHERE a.tenant_id=$1`, []any{tenantID}},
		{&result.BaselineWorkLinks, `SELECT count(*) FROM public.baseline_work_items w JOIN public.baseline_assessments a ON a.id=w.assessment_id WHERE a.tenant_id=$1`, []any{tenantID}},
		{&result.ResearchResults, `SELECT count(*) FROM public.baseline_research_results rr JOIN public.baseline_research_runs r ON r.id=rr.research_run_id JOIN public.baseline_assessments a ON a.id=r.assessment_id WHERE a.tenant_id=$1`, []any{tenantID}},
	}
	for _, query := range queries {
		if err := tx.QueryRow(ctx, query.query, query.args...).Scan(query.target); err != nil {
			return fmt.Errorf("scan prototype inventory: %w", err)
		}
	}
	return nil
}

func scanPrototypeFactVersions(ctx context.Context, tx pgx.Tx) ([]prototypemigration.LegacyFactVersion, error) {
	rows, err := tx.Query(ctx, `SELECT id::text,fact_key,label,value,scope,source_type,source_ref,confidence::text,sensitivity,status,recorded_at
		FROM public.business_knowledge_fact_history ORDER BY recorded_at,id`)
	if err != nil {
		return nil, fmt.Errorf("scan prototype fact history: %w", err)
	}
	defer rows.Close()
	result := make([]prototypemigration.LegacyFactVersion, 0)
	for rows.Next() {
		var item prototypemigration.LegacyFactVersion
		if err := rows.Scan(&item.ID, &item.Key, &item.Label, &item.Value, &item.Scope, &item.SourceType, &item.SourceRef, &item.Confidence, &item.Sensitivity, &item.Status, &item.RecordedAt); err != nil {
			return nil, fmt.Errorf("scan prototype fact history row: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func scanPrototypeCurrentFacts(ctx context.Context, tx pgx.Tx) ([]prototypemigration.LegacyCurrentFact, error) {
	rows, err := tx.Query(ctx, `SELECT fact_key,label,value,scope,source_type,source_ref,confidence::text,sensitivity,status,confirmed_at,updated_at
		FROM public.business_knowledge_facts ORDER BY fact_key`)
	if err != nil {
		return nil, fmt.Errorf("scan prototype current facts: %w", err)
	}
	defer rows.Close()
	result := make([]prototypemigration.LegacyCurrentFact, 0)
	for rows.Next() {
		var item prototypemigration.LegacyCurrentFact
		if err := rows.Scan(&item.Key, &item.Label, &item.Value, &item.Scope, &item.SourceType, &item.SourceRef, &item.Confidence, &item.Sensitivity, &item.Status, &item.ConfirmedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan prototype current fact row: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func scanPrototypeDocuments(ctx context.Context, tx pgx.Tx) ([]prototypemigration.LegacyDocumentRevision, error) {
	rows, err := tx.Query(ctx, `SELECT d.id::text,r.id::text,r.revision,r.name,r.media_type,convert_to(r.content,'UTF8'),r.sha256,r.change_summary,d.status,d.deleted_at,r.created_at
		FROM public.document_revisions r JOIN public.documents d ON d.id=r.document_id
		ORDER BY d.id,r.revision,r.id`)
	if err != nil {
		return nil, fmt.Errorf("scan prototype document revisions: %w", err)
	}
	defer rows.Close()
	result := make([]prototypemigration.LegacyDocumentRevision, 0)
	for rows.Next() {
		var item prototypemigration.LegacyDocumentRevision
		if err := rows.Scan(&item.DocumentID, &item.RevisionID, &item.Revision, &item.Name, &item.MediaType, &item.Content, &item.StoredSHA256, &item.ChangeSummary, &item.Status, &item.DeletedAt, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan prototype document revision row: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func scanPrototypeAssessments(ctx context.Context, tx pgx.Tx, tenantID string) ([]prototypemigration.LegacyAssessment, error) {
	rows, err := tx.Query(ctx, `SELECT id::text,status,phase,baseline_version,next_reassessment_at,created_at,updated_at
		FROM public.baseline_assessments WHERE tenant_id=$1 ORDER BY baseline_version,id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("scan prototype assessments: %w", err)
	}
	result := make([]prototypemigration.LegacyAssessment, 0)
	for rows.Next() {
		var item prototypemigration.LegacyAssessment
		if err := rows.Scan(&item.ID, &item.Status, &item.Phase, &item.BaselineVersion, &item.NextReassessmentAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan prototype assessment row: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	byID := make(map[string]int, len(result))
	for index := range result {
		byID[result[index].ID] = index
	}
	if err := scanPrototypeAssessmentFacts(ctx, tx, tenantID, result, byID); err != nil {
		return nil, err
	}
	if err := scanPrototypeRequirements(ctx, tx, tenantID, result, byID); err != nil {
		return nil, err
	}
	return result, nil
}

func scanPrototypeAssessmentFacts(ctx context.Context, tx pgx.Tx, tenantID string, assessments []prototypemigration.LegacyAssessment, byID map[string]int) error {
	rows, err := tx.Query(ctx, `SELECT f.assessment_id::text,f.id::text,f.fact_key,f.value,f.source_type,f.source_ref,f.confidence::text,f.confirmed_at,f.updated_at
		FROM public.business_facts f JOIN public.baseline_assessments a ON a.id=f.assessment_id
		WHERE a.tenant_id=$1 ORDER BY f.assessment_id,f.fact_key,f.id`, tenantID)
	if err != nil {
		return fmt.Errorf("scan prototype assessment facts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var assessmentID string
		var item prototypemigration.LegacyAssessmentFact
		if err := rows.Scan(&assessmentID, &item.ID, &item.Key, &item.Value, &item.SourceType, &item.SourceRef, &item.Confidence, &item.ConfirmedAt, &item.UpdatedAt); err != nil {
			return fmt.Errorf("scan prototype assessment fact row: %w", err)
		}
		index, exists := byID[assessmentID]
		if !exists {
			return errors.New("prototype assessment fact references an unscanned assessment")
		}
		assessments[index].Facts = append(assessments[index].Facts, item)
	}
	return rows.Err()
}

func scanPrototypeRequirements(ctx context.Context, tx pgx.Tx, tenantID string, assessments []prototypemigration.LegacyAssessment, byID map[string]int) error {
	rows, err := tx.Query(ctx, `SELECT r.assessment_id::text,r.id::text,r.requirement_key,r.status,r.disposition,r.responsibility,r.renewal_due_at
		FROM public.evidence_requirements r JOIN public.baseline_assessments a ON a.id=r.assessment_id
		WHERE a.tenant_id=$1 ORDER BY r.assessment_id,r.requirement_key,r.id`, tenantID)
	if err != nil {
		return fmt.Errorf("scan prototype requirements: %w", err)
	}
	type location struct{ assessment, requirement int }
	locations := make(map[string]location)
	for rows.Next() {
		var assessmentID string
		var item prototypemigration.LegacyRequirement
		if err := rows.Scan(&assessmentID, &item.ID, &item.Key, &item.Status, &item.Disposition, &item.Responsibility, &item.RenewalDueAt); err != nil {
			rows.Close()
			return fmt.Errorf("scan prototype requirement row: %w", err)
		}
		assessmentIndex, exists := byID[assessmentID]
		if !exists {
			rows.Close()
			return errors.New("prototype requirement references an unscanned assessment")
		}
		assessments[assessmentIndex].Requirements = append(assessments[assessmentIndex].Requirements, item)
		locations[item.ID] = location{assessment: assessmentIndex, requirement: len(assessments[assessmentIndex].Requirements) - 1}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	evidenceRows, err := tx.Query(ctx, `SELECT e.requirement_id::text,e.id::text,COALESCE(e.document_id::text,''),COALESCE(e.source_item_id::text,''),e.public_url,e.verified_at
		FROM public.evidence_links e JOIN public.evidence_requirements r ON r.id=e.requirement_id
		JOIN public.baseline_assessments a ON a.id=r.assessment_id WHERE a.tenant_id=$1
		ORDER BY e.requirement_id,e.created_at,e.id`, tenantID)
	if err != nil {
		return fmt.Errorf("scan prototype evidence links: %w", err)
	}
	defer evidenceRows.Close()
	for evidenceRows.Next() {
		var requirementID string
		var item prototypemigration.LegacyEvidenceLink
		if err := evidenceRows.Scan(&requirementID, &item.ID, &item.DocumentID, &item.SourceItemID, &item.PublicURL, &item.VerifiedAt); err != nil {
			return fmt.Errorf("scan prototype evidence link row: %w", err)
		}
		location, exists := locations[requirementID]
		if !exists {
			return errors.New("prototype evidence link references an unscanned requirement")
		}
		assessments[location.assessment].Requirements[location.requirement].Evidence = append(assessments[location.assessment].Requirements[location.requirement].Evidence, item)
	}
	return evidenceRows.Err()
}

func scanPrototypeDeferred(ctx context.Context, tx pgx.Tx, tenantID string) ([]prototypemigration.LegacyDeferredRecord, error) {
	rows, err := tx.Query(ctx, `
		SELECT 'source_item',i.id::text,'legacy connector source item ('||s.source_type||')'
		FROM public.baseline_source_items i JOIN public.baseline_data_sources s ON s.id=i.data_source_id WHERE s.tenant_id=$1
		UNION ALL
		SELECT 'interview_message',m.id::text,'legacy Baseline interview message ('||m.role||')'
		FROM public.baseline_interview_messages m JOIN public.baseline_assessments a ON a.id=m.assessment_id WHERE a.tenant_id=$1
		UNION ALL
		SELECT 'baseline_work_link',a.id::text||':'||w.work_item_id::text,'legacy Baseline-to-Work relation'
		FROM public.baseline_work_items w JOIN public.baseline_assessments a ON a.id=w.assessment_id WHERE a.tenant_id=$1
		UNION ALL
		SELECT 'research_result',rr.id::text,'legacy public research result'
		FROM public.baseline_research_results rr JOIN public.baseline_research_runs r ON r.id=rr.research_run_id
		JOIN public.baseline_assessments a ON a.id=r.assessment_id WHERE a.tenant_id=$1
		ORDER BY 1,2`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("scan deferred prototype records: %w", err)
	}
	defer rows.Close()
	result := make([]prototypemigration.LegacyDeferredRecord, 0)
	for rows.Next() {
		var item prototypemigration.LegacyDeferredRecord
		if err := rows.Scan(&item.Kind, &item.SourceID, &item.Detail); err != nil {
			return nil, fmt.Errorf("scan deferred prototype record: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func scanStrings(ctx context.Context, tx pgx.Tx, query string) ([]string, error) {
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}
