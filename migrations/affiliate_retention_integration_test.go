package migrations_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateprogram"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestAffiliateRetentionHoldAndIdentityFreeMinimization(t *testing.T) {
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
	if _, err := migrations.Apply(ctx, pool, migrations.Global); err != nil {
		t.Fatal(err)
	}

	const affiliateID = "61000000-0000-4000-8000-000000000001"
	const affiliateUser = "61000000-0000-4000-8000-000000000002"
	const replacementUser = "61000000-0000-4000-8000-000000000003"
	const publicCode = "IO-RETENTION1"
	createdAt := time.Now().UTC().Add(-time.Hour)
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id,primary_email,display_name,state,created_at) VALUES
			($1,'retention-affiliate@example.test','Retention Affiliate','active',$3),
			($2,'retention-replacement@example.test','Replacement Affiliate','active',$3)`,
		affiliateUser, replacementUser, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO affiliate_enrollments
			(affiliate_id,user_id,settlement_account_id,public_code,terms_version,rule_version,state,version,created_at,updated_at)
		VALUES ($1,$2,NULL,$3,1,1,'active',1,$4,$4)`,
		affiliateID, affiliateUser, publicCode, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT * FROM spyglass_transition_affiliate_enrollment(
		'61000000-0000-4000-8000-000000000004',$1,1,'closed',
		'retention-operator@example.test','Close enrollment before the retention interval','local')`, affiliateID); err != nil {
		t.Fatal(err)
	}

	repository := postgresadapter.NewAffiliateProgramRepository(pool)
	var heldAffiliate string
	var legalHold bool
	var restrictedAt *time.Time
	var holdVersion int64
	var holdUpdatedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT * FROM spyglass_restrict_affiliate_retention(
		'61000000-0000-4000-8000-000000000009',$1,1,
		'privacy-operator@example.test','Restrict retained evidence after verified Affiliate erasure','local')`, affiliateID).
		Scan(&heldAffiliate, &legalHold, &restrictedAt, &holdVersion, &holdUpdatedAt); err != nil ||
		heldAffiliate != affiliateID || legalHold || restrictedAt == nil || holdVersion != 2 || holdUpdatedAt.IsZero() {
		t.Fatalf("restricted Affiliate=%q hold=%v restricted_at=%v version=%d updated=%v err=%v",
			heldAffiliate, legalHold, restrictedAt, holdVersion, holdUpdatedAt, err)
	}
	if _, err := repository.EnrollmentByUser(ctx, affiliateUser); !errors.Is(err, affiliateprogram.ErrEnrollmentRestricted) {
		t.Fatalf("restricted Affiliate ordinary access error=%v", err)
	}
	if _, err := repository.DataExport(ctx, affiliateUser); !errors.Is(err, affiliateprogram.ErrEnrollmentRestricted) {
		t.Fatalf("restricted Affiliate export access error=%v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT * FROM spyglass_restrict_affiliate_retention(
		'61000000-0000-4000-8000-000000000010',$1,2,
		'privacy-operator@example.test','Attempt to repeat the irreversible restriction','local')`, affiliateID); err == nil {
		t.Fatal("Affiliate retention restriction was repeatable")
	}
	if err := pool.QueryRow(ctx, `SELECT * FROM spyglass_set_affiliate_retention_hold(
		'61000000-0000-4000-8000-000000000005',$1,2,true,
		'retention-operator@example.test','Preserve records for a scoped legal review','local')`, affiliateID).
		Scan(&heldAffiliate, &legalHold, &restrictedAt, &holdVersion, &holdUpdatedAt); err != nil ||
		heldAffiliate != affiliateID || !legalHold || restrictedAt == nil || holdVersion != 3 || holdUpdatedAt.IsZero() {
		t.Fatalf("held Affiliate=%q hold=%v version=%d updated=%v err=%v", heldAffiliate, legalHold, holdVersion, holdUpdatedAt, err)
	}
	if _, err := pool.Exec(ctx, `SELECT * FROM spyglass_set_affiliate_retention_hold(
		'61000000-0000-4000-8000-000000000006',$1,2,false,
		'retention-operator@example.test','Attempt a stale legal-hold release operation','local')`, affiliateID); err == nil {
		t.Fatal("stale Affiliate retention hold release succeeded")
	}
	if _, err := pool.Exec(ctx, `UPDATE affiliate_retention_control_events SET legal_hold=false WHERE affiliate_id=$1`, affiliateID); err == nil {
		t.Fatal("Affiliate retention hold evidence was mutable")
	}

	future := time.Now().UTC().AddDate(8, 0, 0)
	var total, eligible, oldestAge int64
	if err := pool.QueryRow(ctx, `SELECT * FROM spyglass_affiliate_minimization_stats($1)`, future).
		Scan(&total, &eligible, &oldestAge); err != nil || total != 1 || eligible != 0 || oldestAge != 0 {
		t.Fatalf("held minimization stats total=%d eligible=%d age=%d err=%v", total, eligible, oldestAge, err)
	}
	var minimized int64
	if err := pool.QueryRow(ctx, `SELECT spyglass_minimize_due_affiliates($1,10)`, future).Scan(&minimized); err != nil || minimized != 0 {
		t.Fatalf("held minimization count=%d err=%v", minimized, err)
	}
	if err := pool.QueryRow(ctx, `SELECT * FROM spyglass_set_affiliate_retention_hold(
		'61000000-0000-4000-8000-000000000007',$1,3,false,
		'retention-operator@example.test','Release the scoped legal hold after review','local')`, affiliateID).
		Scan(&heldAffiliate, &legalHold, &restrictedAt, &holdVersion, &holdUpdatedAt); err != nil || legalHold || restrictedAt == nil || holdVersion != 4 {
		t.Fatalf("released Affiliate=%q hold=%v version=%d err=%v", heldAffiliate, legalHold, holdVersion, err)
	}
	if err := pool.QueryRow(ctx, `SELECT * FROM spyglass_affiliate_minimization_stats($1)`, future).
		Scan(&total, &eligible, &oldestAge); err != nil || total != 1 || eligible != 1 || oldestAge <= 0 {
		t.Fatalf("due minimization stats total=%d eligible=%d age=%d err=%v", total, eligible, oldestAge, err)
	}
	if err := pool.QueryRow(ctx, `SELECT spyglass_minimize_due_affiliates($1,10)`, future).Scan(&minimized); err != nil || minimized != 1 {
		t.Fatalf("minimization count=%d err=%v", minimized, err)
	}

	var enrollmentCount, eventCount, codeCount, controlCount, tombstoneCount, fingerprintCount int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM affiliate_enrollments WHERE affiliate_id=$1),
		(SELECT count(*) FROM affiliate_enrollment_events WHERE affiliate_id=$1),
		(SELECT count(*) FROM affiliate_public_code_history WHERE affiliate_id=$1),
		(SELECT count(*) FROM affiliate_retention_controls WHERE affiliate_id=$1),
		(SELECT count(*) FROM affiliate_minimization_tombstones),
		(SELECT count(*) FROM affiliate_retired_code_fingerprints)`, affiliateID).Scan(
		&enrollmentCount, &eventCount, &codeCount, &controlCount, &tombstoneCount, &fingerprintCount); err != nil ||
		enrollmentCount != 0 || eventCount != 0 || codeCount != 0 || controlCount != 0 || tombstoneCount != 1 || fingerprintCount != 1 {
		t.Fatalf("retained graph enrollment=%d events=%d codes=%d controls=%d tombstones=%d fingerprints=%d err=%v",
			enrollmentCount, eventCount, codeCount, controlCount, tombstoneCount, fingerprintCount, err)
	}
	var tombstoneCodeCount int
	var fingerprintMatches bool
	var identifyingColumns int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT code_count FROM affiliate_minimization_tombstones LIMIT 1),
		(SELECT code_fingerprint=digest(convert_to('spyglass:affiliate-code:v1:'||$1,'UTF8'),'sha256')
		   FROM affiliate_retired_code_fingerprints LIMIT 1),
		(SELECT count(*) FROM information_schema.columns
		  WHERE table_schema='public'
		    AND table_name IN ('affiliate_minimization_tombstones','affiliate_minimization_financial_totals','affiliate_retired_code_fingerprints')
		    AND column_name IN ('affiliate_id','user_id','public_code','settlement_account_id'))`, publicCode).
		Scan(&tombstoneCodeCount, &fingerprintMatches, &identifyingColumns); err != nil ||
		tombstoneCodeCount != 1 || !fingerprintMatches || identifyingColumns != 0 {
		t.Fatalf("tombstone code_count=%d fingerprint_match=%v identifying_columns=%d err=%v",
			tombstoneCodeCount, fingerprintMatches, identifyingColumns, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE affiliate_minimization_tombstones SET policy_version=1`); err == nil {
		t.Fatal("Affiliate minimization tombstone was mutable")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM affiliate_retired_code_fingerprints`); err == nil {
		t.Fatal("retired Affiliate code fingerprint was deletable")
	}

	replacement, err := affiliates.NewEnrollment(ids.AffiliateID("61000000-0000-4000-8000-000000000008"),
		ids.UserID(replacementUser), "", publicCode, 1, 1, createdAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateEnrollment(ctx, replacement); !errors.Is(err, affiliateprogram.ErrCodeUnavailable) {
		t.Fatalf("permanently retired code reuse error=%v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT * FROM spyglass_affiliate_minimization_stats($1)`, future).
		Scan(&total, &eligible, &oldestAge); err != nil || total != 0 || eligible != 0 || oldestAge != 0 {
		t.Fatalf("post-minimization stats total=%d eligible=%d age=%d err=%v", total, eligible, oldestAge, err)
	}

	const operatorRole = "spyglass_affiliate_retention_operator_contract"
	const workerRole = "spyglass_affiliate_retention_worker_contract"
	if _, err := pool.Exec(ctx, `CREATE ROLE `+operatorRole+` NOLOGIN;
		CREATE ROLE `+workerRole+` NOLOGIN;
		GRANT USAGE ON SCHEMA public TO `+operatorRole+`,`+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_set_affiliate_retention_hold(uuid,uuid,bigint,boolean,text,text,text) TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_restrict_affiliate_retention(uuid,uuid,bigint,text,text,text) TO `+operatorRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_minimize_due_affiliates(timestamptz,integer) TO `+workerRole+`;
		GRANT EXECUTE ON FUNCTION public.spyglass_affiliate_minimization_stats(timestamptz) TO `+workerRole); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DROP OWNED BY `+operatorRole+`; DROP OWNED BY `+workerRole+`; DROP ROLE `+operatorRole+`; DROP ROLE `+workerRole)
	}()
	var operatorTable, operatorHold, operatorRestrict, workerTable, workerMinimize, workerStats, workerCandidates bool
	if err := pool.QueryRow(ctx, `SELECT
		has_table_privilege($1,'public.affiliate_retention_controls','SELECT'),
		has_function_privilege($1,'public.spyglass_set_affiliate_retention_hold(uuid,uuid,bigint,boolean,text,text,text)','EXECUTE'),
		has_function_privilege($1,'public.spyglass_restrict_affiliate_retention(uuid,uuid,bigint,text,text,text)','EXECUTE'),
		has_table_privilege($2,'public.affiliate_minimization_tombstones','SELECT'),
		has_function_privilege($2,'public.spyglass_minimize_due_affiliates(timestamptz,integer)','EXECUTE'),
		has_function_privilege($2,'public.spyglass_affiliate_minimization_stats(timestamptz)','EXECUTE'),
		has_function_privilege($2,'public.spyglass_affiliate_minimization_candidates(timestamptz)','EXECUTE')`, operatorRole, workerRole).Scan(
		&operatorTable, &operatorHold, &operatorRestrict, &workerTable, &workerMinimize, &workerStats, &workerCandidates); err != nil ||
		operatorTable || !operatorHold || !operatorRestrict || workerTable || !workerMinimize || !workerStats || workerCandidates {
		t.Fatalf("retention authority operator_table=%v hold=%v restrict=%v worker_table=%v minimize=%v stats=%v candidates=%v err=%v",
			operatorTable, operatorHold, operatorRestrict, workerTable, workerMinimize, workerStats, workerCandidates, err)
	}
}
