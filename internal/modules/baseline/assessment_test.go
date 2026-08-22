package baseline

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	testAssessmentID ids.BaselineAssessmentID  = "b1000000-0000-4000-8000-000000000001"
	testAccountID    ids.AccountID             = "b2000000-0000-4000-8000-000000000002"
	testUserID       ids.UserID                = "b3000000-0000-4000-8000-000000000003"
	testFactID       ids.KnowledgeFactID       = "b4000000-0000-4000-8000-000000000004"
	testRequirement  ids.BaselineRequirementID = "b5000000-0000-4000-8000-000000000005"
	testEvidenceID   ids.KnowledgeEvidenceID   = "b6000000-0000-4000-8000-000000000006"
	testPlanID       ids.BaselinePlanID        = "b7000000-0000-4000-8000-000000000007"
)

func TestAssessmentLifecycleFreezesEvidenceAndPlanVersions(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	actor := Actor{UserID: testUserID}
	assessment, err := NewAssessment(AssessmentDraft{ID: testAssessmentID, AccountID: testAccountID, CatalogVersion: "catalog-2026-08", ScopePolicyVersion: "scope-v1", CreatedBy: actor}, now)
	if err != nil {
		t.Fatal(err)
	}
	assessment, err = assessment.AnswerInterview(AnswerInterviewCommand{Answer: InterviewAnswer{QuestionKey: "company.legal_name", Kind: AnswerFact, Fact: &FactReference{FactID: testFactID, Revision: 3}, AnsweredBy: actor, AnsweredAt: now.Add(time.Minute)}, Role: accounts.RoleMember, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err = assessment.AnswerInterview(AnswerInterviewCommand{Answer: InterviewAnswer{QuestionKey: "company.tax_id", Kind: AnswerUnknown, Reason: "Owner has not confirmed it yet", AnsweredBy: actor, AnsweredAt: now.Add(2 * time.Minute)}, Role: accounts.RoleMember, ExpectedVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err = assessment.BeginInventory(BeginInventoryCommand{Actor: actor, Role: accounts.RoleMember, ExpectedVersion: 3, At: now.Add(3 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err = assessment.CompleteInventory(CompleteInventoryCommand{Actor: actor, Role: accounts.RoleMember, ExpectedVersion: 4, At: now.Add(4 * time.Minute), Requirements: []RequirementDraft{{ID: testRequirement, Code: "legal.formation", Title: "Confirm the legal formation record", Responsibility: Responsibility{Kind: ResponsibilityAccount}, RenewAfterDays: 365, CatalogVersion: "catalog-2026-08", ScopePolicyVersion: "scope-v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err = assessment.DecideEvidence(DecideEvidenceCommand{RequirementID: testRequirement, Decision: EvidenceDecision{EvidenceID: testEvidenceID, Decision: EvidenceAccepted, Reason: "Current filed formation record", DecidedBy: actor, DecidedAt: now.Add(5 * time.Minute)}, Role: accounts.RoleMember, ExpectedVersion: 5})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err = assessment.SubmitPlan(SubmitPlanCommand{PlanID: testPlanID, Actor: actor, Role: accounts.RoleMember, ExpectedVersion: 6, At: now.Add(6 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	planDigest := assessment.Plan.ContentSHA256
	if _, err := assessment.ApprovePlan(ApprovePlanCommand{PlanID: testPlanID, PlanSHA256: sha256.Sum256([]byte("changed")), AssessmentVersion: 6, Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: 7, At: now.Add(7 * time.Minute)}); !errors.Is(err, ErrPlan) {
		t.Fatalf("changed plan approved: %v", err)
	}
	assessment, err = assessment.ApprovePlan(ApprovePlanCommand{PlanID: testPlanID, PlanSHA256: planDigest, AssessmentVersion: 6, Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: 7, At: now.Add(7 * time.Minute)})
	if err != nil || assessment.State != StateActive {
		t.Fatalf("approve=%+v err=%v", assessment, err)
	}
	assessment, err = assessment.MarkReady(MarkReadyCommand{Actor: actor, Role: accounts.RoleAdministrator, ExpectedVersion: 8, At: now.Add(8 * time.Minute)})
	if err != nil || assessment.State != StateReady {
		t.Fatalf("ready=%+v err=%v", assessment, err)
	}
	due := assessment.RenewalDue(now.Add(366 * 24 * time.Hour))
	if len(due) != 1 || due[0] != testRequirement {
		t.Fatalf("renewal due=%v", due)
	}
	archived, next, err := assessment.StartReassessment(StartReassessmentCommand{NewAssessmentID: "b8000000-0000-4000-8000-000000000008", CatalogVersion: "catalog-2027-08", ScopePolicyVersion: "scope-v2", Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: 9, At: now.Add(367 * 24 * time.Hour)})
	if err != nil || archived.State != StateArchived || next.State != StateInterview || archived.SupersededBy != next.ID || next.AccountID != assessment.AccountID {
		t.Fatalf("reassessment archived=%+v next=%+v err=%v", archived, next, err)
	}
}

func TestAssessmentPlanRequiresExplicitGapReview(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	assessment, actor := assessmentAtGapReview(t, now)
	if _, err := assessment.SubmitPlan(SubmitPlanCommand{PlanID: testPlanID, Actor: actor, Role: accounts.RoleMember, ExpectedVersion: assessment.Version, At: now.Add(5 * time.Minute)}); !errors.Is(err, ErrState) {
		t.Fatalf("pending requirement accepted into a plan: %v", err)
	}
	assessment, err := assessment.DispositionRequirement(DispositionRequirementCommand{RequirementID: testRequirement, Disposition: DispositionGap, Reason: "Formation record is not available", Actor: actor, Role: accounts.RoleMember, ExpectedVersion: assessment.Version, At: now.Add(5 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err = assessment.SubmitPlan(SubmitPlanCommand{PlanID: testPlanID, Actor: actor, Role: accounts.RoleMember, ExpectedVersion: assessment.Version, At: now.Add(6 * time.Minute)})
	if err != nil || assessment.State != StatePlanApproval || assessment.Plan.ProposedWorkCount != 1 || len(assessment.PlannedWork()) != 1 {
		t.Fatalf("gap plan=%+v err=%v", assessment, err)
	}
	assessment, err = assessment.ApprovePlan(ApprovePlanCommand{PlanID: testPlanID, PlanSHA256: assessment.Plan.ContentSHA256, AssessmentVersion: assessment.Plan.AssessmentVersion, Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version, At: now.Add(7 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assessment.MarkReady(MarkReadyCommand{Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version, At: now.Add(8 * time.Minute)}); !errors.Is(err, ErrState) {
		t.Fatalf("assessment with an unresolved gap marked ready: %v", err)
	}
}

func TestAssessmentEvidenceDecisionsAreImmutableByEvidenceIdentity(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	assessment, actor := assessmentAtGapReview(t, now)
	decision := EvidenceDecision{EvidenceID: testEvidenceID, Decision: EvidenceRejected, Reason: "The record is expired", DecidedBy: actor, DecidedAt: now.Add(5 * time.Minute)}
	assessment, err := assessment.DecideEvidence(DecideEvidenceCommand{RequirementID: testRequirement, Decision: decision, Role: accounts.RoleMember, ExpectedVersion: assessment.Version})
	if err != nil || len(assessment.Requirements[0].Evidence) != 1 || assessment.Requirements[0].Disposition != DispositionPending {
		t.Fatalf("reject=%+v err=%v", assessment, err)
	}
	decision.Decision = EvidenceAccepted
	decision.Reason = "The renewed record is current"
	decision.DecidedAt = now.Add(6 * time.Minute)
	if _, err = assessment.DecideEvidence(DecideEvidenceCommand{RequirementID: testRequirement, Decision: decision, Role: accounts.RoleMember, ExpectedVersion: assessment.Version}); !errors.Is(err, ErrConflict) {
		t.Fatalf("immutable evidence decision replaced: %v", err)
	}
	accepted := decision
	accepted.EvidenceID = "b9000000-0000-4000-8000-000000000009"
	assessment, err = assessment.DecideEvidence(DecideEvidenceCommand{RequirementID: testRequirement, Decision: accepted, Role: accounts.RoleMember, ExpectedVersion: assessment.Version})
	if err != nil || len(assessment.Requirements[0].Evidence) != 2 || assessment.Requirements[0].Disposition != DispositionSatisfied {
		t.Fatalf("new evidence=%+v err=%v", assessment, err)
	}
	if _, err := assessment.DispositionRequirement(DispositionRequirementCommand{RequirementID: testRequirement, Disposition: DispositionNotApplicable, Reason: "No longer applies", Actor: actor, Role: accounts.RoleMember, ExpectedVersion: assessment.Version, At: now.Add(7 * time.Minute)}); !errors.Is(err, ErrState) {
		t.Fatalf("accepted evidence discarded by disposition: %v", err)
	}
}

func assessmentAtGapReview(t *testing.T, now time.Time) (Assessment, Actor) {
	t.Helper()
	actor := Actor{UserID: testUserID}
	assessment, err := NewAssessment(AssessmentDraft{ID: testAssessmentID, AccountID: testAccountID, CatalogVersion: "catalog-v1", ScopePolicyVersion: "scope-v1", CreatedBy: actor}, now)
	if err != nil {
		t.Fatal(err)
	}
	assessment, err = assessment.AnswerInterview(AnswerInterviewCommand{Answer: InterviewAnswer{QuestionKey: "company.legal_name", Kind: AnswerFact, Fact: &FactReference{FactID: testFactID, Revision: 1}, AnsweredBy: actor, AnsweredAt: now.Add(time.Minute)}, Role: accounts.RoleMember, ExpectedVersion: assessment.Version})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err = assessment.BeginInventory(BeginInventoryCommand{Actor: actor, Role: accounts.RoleMember, ExpectedVersion: assessment.Version, At: now.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err = assessment.CompleteInventory(CompleteInventoryCommand{Actor: actor, Role: accounts.RoleMember, ExpectedVersion: assessment.Version, At: now.Add(3 * time.Minute), Requirements: []RequirementDraft{{ID: testRequirement, Code: "legal.formation", Title: "Confirm formation", Responsibility: Responsibility{Kind: ResponsibilityAccount}, CatalogVersion: "catalog-v1", ScopePolicyVersion: "scope-v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	return assessment, actor
}
