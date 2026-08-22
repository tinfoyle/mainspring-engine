package migrations_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	baselineapp "github.com/tinfoyle/spyglass-engine/internal/application/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	baselinedomain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestBaselineRepositoryPersistsIsolatedImmutableLifecycle(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	owner := openPool(t, ctx, databaseURL, nil)
	defer owner.Close()
	if _, err := migrations.Apply(ctx, owner, migrations.Cell); err != nil {
		t.Fatal(err)
	}
	var databaseNow time.Time
	if err := owner.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("d1000000-0000-4000-8000-000000000001")
	otherAccountID := ids.AccountID("e1000000-0000-4000-8000-000000000001")
	for _, value := range []ids.AccountID{accountID, otherAccountID} {
		if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$2)`, value, databaseNow); err != nil {
			t.Fatal(err)
		}
	}
	userID := ids.UserID("d2000000-0000-4000-8000-000000000002")
	evidenceID := ids.KnowledgeEvidenceID("d3000000-0000-4000-8000-000000000003")
	factID := ids.KnowledgeFactID("d5000000-0000-4000-8000-000000000005")
	seedKnowledgeFoundation(t, ctx, owner, string(accountID), string(userID), string(evidenceID), "d4000000-0000-4000-8000-000000000004", string(factID), "d6000000-0000-4000-8000-000000000006", databaseNow)

	cell, err := database.NewCellPool(owner)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewBaselineRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	assessmentID := ids.BaselineAssessmentID("d7000000-0000-4000-8000-000000000007")
	requirementID := ids.BaselineRequirementID("d8000000-0000-4000-8000-000000000008")
	planID := ids.BaselinePlanID("d9000000-0000-4000-8000-000000000009")
	actor := baselinedomain.Actor{UserID: userID}
	now := databaseNow.Add(10 * time.Second).UTC()
	correlation := func(index byte) string { return fmt.Sprintf("da000000-0000-4000-8000-%012d", index) }
	mutation := func(index byte, reason string) baselineapp.Mutation {
		return baselineapp.Mutation{Actor: actor, ReasonCode: reason, CorrelationID: correlation(index), At: now}
	}
	assessment, err := baselinedomain.NewAssessment(baselinedomain.AssessmentDraft{ID: assessmentID, AccountID: accountID, CatalogVersion: "catalog-v1", ScopePolicyVersion: "scope-v1", CreatedBy: actor}, now)
	if err != nil {
		t.Fatal(err)
	}
	assessment, err = repository.Create(ctx, assessment, mutation(1, "assessment_started"))
	if err != nil {
		t.Fatal(err)
	}
	if replayed, err := repository.Create(ctx, assessment, mutation(1, "assessment_started")); err != nil || replayed.ID != assessment.ID {
		t.Fatalf("idempotent create=%+v err=%v", replayed, err)
	}

	now = now.Add(time.Second)
	previous := assessment
	assessment, err = assessment.AnswerInterview(baselinedomain.AnswerInterviewCommand{Answer: baselinedomain.InterviewAnswer{QuestionKey: "organization.legal_name", Kind: baselinedomain.AnswerFact, Fact: &baselinedomain.FactReference{FactID: factID, Revision: 1}, AnsweredBy: actor, AnsweredAt: now}, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version})
	assessment = persistBaselineUpdate(t, ctx, repository, assessment, previous.Version, baselineapp.Transition{EventType: "interview_answered"}, mutation(2, "owner_answered"), err)
	now = now.Add(time.Second)
	previous = assessment
	assessment, err = assessment.BeginInventory(baselinedomain.BeginInventoryCommand{Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version, At: now})
	assessment = persistBaselineUpdate(t, ctx, repository, assessment, previous.Version, baselineapp.Transition{EventType: "inventory_started"}, mutation(3, "inventory_started"), err)
	if _, err := repository.Update(ctx, assessment, previous.Version, baselineapp.Transition{EventType: "inventory_started"}, mutation(3, "inventory_started")); !errors.Is(err, baselineapp.ErrConflict) {
		t.Fatalf("stale update error=%v", err)
	}
	now = now.Add(time.Second)
	previous = assessment
	assessment, err = assessment.CompleteInventory(baselinedomain.CompleteInventoryCommand{Requirements: []baselinedomain.RequirementDraft{{ID: requirementID, Code: "legal.formation", Title: "Verify formation", Responsibility: baselinedomain.Responsibility{Kind: baselinedomain.ResponsibilityAccount}, RenewAfterDays: 30, CatalogVersion: "catalog-v1", ScopePolicyVersion: "scope-v1"}}, Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version, At: now})
	assessment = persistBaselineUpdate(t, ctx, repository, assessment, previous.Version, baselineapp.Transition{EventType: "inventory_completed"}, mutation(4, "inventory_completed"), err)
	now = now.Add(time.Second)
	previous = assessment
	assessment, err = assessment.DecideEvidence(baselinedomain.DecideEvidenceCommand{RequirementID: requirementID, Decision: baselinedomain.EvidenceDecision{EvidenceID: evidenceID, Decision: baselinedomain.EvidenceAccepted, Reason: "Current verified formation record", DecidedBy: actor, DecidedAt: now}, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version})
	assessment = persistBaselineUpdate(t, ctx, repository, assessment, previous.Version, baselineapp.Transition{EventType: "evidence_decided", RequirementID: requirementID}, mutation(5, "evidence_reviewed"), err)
	now = now.Add(time.Second)
	previous = assessment
	digest := sha256.Sum256([]byte("canonical baseline plan"))
	assessment, err = assessment.SubmitPlan(baselinedomain.SubmitPlanCommand{Plan: baselinedomain.PlanBinding{ID: planID, ContentSHA256: digest, ProposedWorkCount: 1}, Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version, At: now})
	assessment = persistBaselineUpdate(t, ctx, repository, assessment, previous.Version, baselineapp.Transition{EventType: "plan_submitted", PlanID: planID}, mutation(6, "plan_submitted"), err)
	now = now.Add(time.Second)
	previous = assessment
	assessment, err = assessment.ApprovePlan(baselinedomain.ApprovePlanCommand{PlanID: planID, PlanSHA256: digest, AssessmentVersion: assessment.Plan.AssessmentVersion, Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version, At: now})
	assessment = persistBaselineUpdate(t, ctx, repository, assessment, previous.Version, baselineapp.Transition{EventType: "plan_approved", PlanID: planID}, mutation(7, "plan_approved"), err)
	now = now.Add(time.Second)
	previous = assessment
	assessment, err = assessment.MarkReady(baselinedomain.MarkReadyCommand{Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version, At: now})
	assessment = persistBaselineUpdate(t, ctx, repository, assessment, previous.Version, baselineapp.Transition{EventType: "assessment_ready"}, mutation(8, "assessment_ready"), err)

	loaded, err := repository.Get(ctx, accountID, assessmentID)
	if err != nil || loaded.State != baselinedomain.StateReady || loaded.Version != assessment.Version || len(loaded.Requirements) != 1 || len(loaded.Requirements[0].Evidence) != 1 || loaded.Plan == nil || loaded.Plan.ApprovedAt == nil {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	if _, err := repository.Get(ctx, otherAccountID, assessmentID); !errors.Is(err, baselineapp.ErrNotFound) {
		t.Fatalf("cross-account get error=%v", err)
	}
	var eventCount int
	var payloads string
	if err := owner.QueryRow(ctx, `SELECT count(*),string_agg(redacted_payload::text,' ') FROM spyglass.baseline_events WHERE account_id=$1 AND assessment_id=$2`, accountID, assessmentID).Scan(&eventCount, &payloads); err != nil || eventCount != 8 {
		t.Fatalf("events=%d payloads=%q err=%v", eventCount, payloads, err)
	}
	if strings.Contains(payloads, "Current verified formation record") || strings.Contains(payloads, "organization.legal_name") {
		t.Fatalf("event payload leaked content: %s", payloads)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.baseline_evidence_decisions SET reason='changed' WHERE account_id=$1 AND requirement_id=$2`, accountID, requirementID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("evidence decision update=%v", err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.baseline_interview_answers SET answered_at=answered_at WHERE account_id=$1 AND assessment_id=$2`, accountID, assessmentID); err == nil || !strings.Contains(err.Error(), "frozen") {
		t.Fatalf("frozen interview update=%v", err)
	}

	now = now.Add(time.Second)
	archived, next, err := assessment.StartReassessment(baselinedomain.StartReassessmentCommand{NewAssessmentID: "db000000-0000-4000-8000-000000000001", CatalogVersion: "catalog-v2", ScopePolicyVersion: "scope-v2", Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version, At: now})
	if err != nil {
		t.Fatal(err)
	}
	archived, next, err = repository.Reassess(ctx, archived, next, assessment.Version, mutation(9, "reassessment_started"))
	if err != nil || archived.State != baselinedomain.StateArchived || next.State != baselinedomain.StateInterview {
		t.Fatalf("archived=%+v next=%+v err=%v", archived, next, err)
	}
	var currentCount int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.baseline_assessments WHERE account_id=$1 AND state<>'archived'`, accountID).Scan(&currentCount); err != nil || currentCount != 1 {
		t.Fatalf("current assessments=%d err=%v", currentCount, err)
	}
}

func persistBaselineUpdate(t *testing.T, ctx context.Context, repository *postgresadapter.BaselineRepository, updated baselinedomain.Assessment, expected uint64, transition baselineapp.Transition, mutation baselineapp.Mutation, domainErr error) baselinedomain.Assessment {
	t.Helper()
	if domainErr != nil {
		t.Fatal(domainErr)
	}
	result, err := repository.Update(ctx, updated, expected, transition, mutation)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
