package baseline

import (
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AssessmentDraft struct {
	ID                 ids.BaselineAssessmentID
	AccountID          ids.AccountID
	CatalogVersion     string
	ScopePolicyVersion string
	CreatedBy          Actor
}

type Assessment struct {
	AssessmentDraft
	State        AssessmentState
	Answers      []InterviewAnswer
	Requirements []Requirement
	Plan         *PlanBinding
	SupersededBy ids.BaselineAssessmentID
	Version      uint64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func NewAssessment(draft AssessmentDraft, now time.Time) (Assessment, error) {
	draft.CatalogVersion = strings.TrimSpace(draft.CatalogVersion)
	draft.ScopePolicyVersion = strings.TrimSpace(draft.ScopePolicyVersion)
	assessment := Assessment{AssessmentDraft: draft, State: StateInterview, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	return RestoreAssessment(assessment)
}

func RestoreAssessment(assessment Assessment) (Assessment, error) {
	assessment.CatalogVersion = strings.TrimSpace(assessment.CatalogVersion)
	assessment.ScopePolicyVersion = strings.TrimSpace(assessment.ScopePolicyVersion)
	assessment.CreatedAt, assessment.UpdatedAt = assessment.CreatedAt.UTC(), assessment.UpdatedAt.UTC()
	assessment.Answers = append([]InterviewAnswer(nil), assessment.Answers...)
	assessment.Requirements = append([]Requirement(nil), assessment.Requirements...)
	if assessment.Plan != nil {
		value := assessment.Plan.normalized()
		assessment.Plan = &value
	}
	if ids.Validate(string(assessment.ID)) != nil || ids.Validate(string(assessment.AccountID)) != nil ||
		!validVersionLabel(assessment.CatalogVersion) || !validVersionLabel(assessment.ScopePolicyVersion) || !assessment.CreatedBy.valid() ||
		!assessment.State.valid() || assessment.Version == 0 || assessment.CreatedAt.IsZero() || assessment.UpdatedAt.Before(assessment.CreatedAt) ||
		len(assessment.Answers) > MaximumAssessmentAnswers || len(assessment.Requirements) > MaximumRequirements {
		return Assessment{}, ErrInvalid
	}
	answerKeys := make(map[string]struct{}, len(assessment.Answers))
	for index := range assessment.Answers {
		assessment.Answers[index] = assessment.Answers[index].normalized()
		if !assessment.Answers[index].valid() || assessment.Answers[index].AnsweredAt.Before(assessment.CreatedAt) {
			return Assessment{}, ErrInvalid
		}
		if _, exists := answerKeys[assessment.Answers[index].QuestionKey]; exists {
			return Assessment{}, ErrInvalid
		}
		answerKeys[assessment.Answers[index].QuestionKey] = struct{}{}
	}
	requirementIDs := make(map[ids.BaselineRequirementID]struct{}, len(assessment.Requirements))
	requirementCodes := make(map[string]struct{}, len(assessment.Requirements))
	for index := range assessment.Requirements {
		assessment.Requirements[index] = assessment.Requirements[index].normalized()
		requirement := assessment.Requirements[index]
		if !requirement.valid(assessment.CatalogVersion, assessment.ScopePolicyVersion) {
			return Assessment{}, ErrInvalid
		}
		if _, exists := requirementIDs[requirement.ID]; exists {
			return Assessment{}, ErrInvalid
		}
		if _, exists := requirementCodes[requirement.Code]; exists {
			return Assessment{}, ErrInvalid
		}
		requirementIDs[requirement.ID] = struct{}{}
		requirementCodes[requirement.Code] = struct{}{}
	}
	if !validAssessmentStateShape(assessment) {
		return Assessment{}, ErrInvalid
	}
	return assessment, nil
}

func validAssessmentStateShape(assessment Assessment) bool {
	switch assessment.State {
	case StateInterview:
		return len(assessment.Requirements) == 0 && assessment.Plan == nil && assessment.SupersededBy == ""
	case StateInventory:
		return len(assessment.Answers) > 0 && len(assessment.Requirements) == 0 && assessment.Plan == nil && assessment.SupersededBy == ""
	case StateGapReview:
		return len(assessment.Answers) > 0 && len(assessment.Requirements) > 0 && assessment.Plan == nil && assessment.SupersededBy == ""
	case StatePlanApproval:
		return requirementsReviewed(assessment.Requirements) && assessment.Plan != nil && assessment.Plan.valid(false) && assessment.Plan.AssessmentVersion+1 == assessment.Version && assessment.SupersededBy == ""
	case StateActive, StateReady:
		return requirementsReviewed(assessment.Requirements) && assessment.Plan != nil && assessment.Plan.valid(true) && assessment.Plan.AssessmentVersion+2 <= assessment.Version && assessment.SupersededBy == ""
	case StateArchived:
		return ids.Validate(string(assessment.SupersededBy)) == nil && assessment.SupersededBy != assessment.ID
	default:
		return false
	}
}

type AnswerInterviewCommand struct {
	Answer          InterviewAnswer
	Role            accounts.MembershipRole
	ExpectedVersion uint64
}

func (assessment Assessment) AnswerInterview(command AnswerInterviewCommand) (Assessment, error) {
	if command.ExpectedVersion != assessment.Version {
		return Assessment{}, ErrConflict
	}
	answer := command.Answer.normalized()
	if assessment.State != StateInterview || !answer.valid() || answer.AnsweredAt.Before(assessment.CreatedAt) {
		return Assessment{}, ErrState
	}
	if !canParticipate(command.Role) {
		return Assessment{}, ErrRole
	}
	result := assessment
	result.Answers = append([]InterviewAnswer(nil), assessment.Answers...)
	replaced := false
	for index := range result.Answers {
		if result.Answers[index].QuestionKey == answer.QuestionKey {
			result.Answers[index] = answer
			replaced = true
			break
		}
	}
	if !replaced {
		if len(result.Answers) >= MaximumAssessmentAnswers {
			return Assessment{}, ErrInvalid
		}
		result.Answers = append(result.Answers, answer)
	}
	result.Version++
	result.UpdatedAt = answer.AnsweredAt
	return RestoreAssessment(result)
}

type BeginInventoryCommand struct {
	Actor           Actor
	Role            accounts.MembershipRole
	ExpectedVersion uint64
	At              time.Time
}

func (assessment Assessment) BeginInventory(command BeginInventoryCommand) (Assessment, error) {
	if command.ExpectedVersion != assessment.Version {
		return Assessment{}, ErrConflict
	}
	if assessment.State != StateInterview || len(assessment.Answers) == 0 || !command.Actor.valid() || command.At.IsZero() || command.At.Before(assessment.UpdatedAt) {
		return Assessment{}, ErrState
	}
	if !canParticipate(command.Role) {
		return Assessment{}, ErrRole
	}
	result := assessment
	result.State = StateInventory
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return RestoreAssessment(result)
}

type CompleteInventoryCommand struct {
	Requirements    []RequirementDraft
	Actor           Actor
	Role            accounts.MembershipRole
	ExpectedVersion uint64
	At              time.Time
}

func (assessment Assessment) CompleteInventory(command CompleteInventoryCommand) (Assessment, error) {
	if command.ExpectedVersion != assessment.Version {
		return Assessment{}, ErrConflict
	}
	if assessment.State != StateInventory || len(command.Requirements) == 0 || len(command.Requirements) > MaximumRequirements ||
		!command.Actor.valid() || command.At.IsZero() || command.At.Before(assessment.UpdatedAt) {
		return Assessment{}, ErrState
	}
	if !canParticipate(command.Role) {
		return Assessment{}, ErrRole
	}
	result := assessment
	result.Requirements = make([]Requirement, 0, len(command.Requirements))
	for _, draft := range command.Requirements {
		requirement := Requirement{RequirementDraft: draft, Disposition: DispositionPending}.normalized()
		if !requirement.valid(assessment.CatalogVersion, assessment.ScopePolicyVersion) {
			return Assessment{}, ErrInvalid
		}
		result.Requirements = append(result.Requirements, requirement)
	}
	result.State = StateGapReview
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return RestoreAssessment(result)
}

type DecideEvidenceCommand struct {
	RequirementID   ids.BaselineRequirementID
	Decision        EvidenceDecision
	Role            accounts.MembershipRole
	ExpectedVersion uint64
}

func (assessment Assessment) DecideEvidence(command DecideEvidenceCommand) (Assessment, error) {
	if command.ExpectedVersion != assessment.Version {
		return Assessment{}, ErrConflict
	}
	decision := command.Decision.normalized()
	if assessment.State != StateGapReview || !decision.valid() || decision.DecidedAt.Before(assessment.UpdatedAt) {
		return Assessment{}, ErrState
	}
	if !canParticipate(command.Role) {
		return Assessment{}, ErrRole
	}
	result := assessment
	result.Requirements = append([]Requirement(nil), assessment.Requirements...)
	index := requirementIndex(result.Requirements, command.RequirementID)
	if index < 0 || result.Requirements[index].Disposition == DispositionNotApplicable {
		return Assessment{}, ErrState
	}
	requirement := result.Requirements[index]
	requirement.Evidence = append([]EvidenceDecision(nil), requirement.Evidence...)
	for _, existing := range requirement.Evidence {
		if existing.EvidenceID == decision.EvidenceID {
			return Assessment{}, ErrConflict
		}
	}
	if len(requirement.Evidence) >= MaximumRequirementEvidence {
		return Assessment{}, ErrInvalid
	}
	requirement.Evidence = append(requirement.Evidence, decision)
	requirement.Reason = ""
	requirement.RenewAt = nil
	requirement.Disposition = DispositionPending
	for _, item := range requirement.Evidence {
		if item.Decision == EvidenceAccepted {
			requirement.Disposition = DispositionSatisfied
			if requirement.RenewAfterDays > 0 {
				renewAt := item.DecidedAt.Add(time.Duration(requirement.RenewAfterDays) * 24 * time.Hour)
				requirement.RenewAt = &renewAt
			}
			break
		}
	}
	result.Requirements[index] = requirement
	result.Version++
	result.UpdatedAt = decision.DecidedAt
	return RestoreAssessment(result)
}

type DispositionRequirementCommand struct {
	RequirementID   ids.BaselineRequirementID
	Disposition     RequirementDisposition
	Reason          string
	Actor           Actor
	Role            accounts.MembershipRole
	ExpectedVersion uint64
	At              time.Time
}

func (assessment Assessment) DispositionRequirement(command DispositionRequirementCommand) (Assessment, error) {
	if command.ExpectedVersion != assessment.Version {
		return Assessment{}, ErrConflict
	}
	if assessment.State != StateGapReview || (command.Disposition != DispositionGap && command.Disposition != DispositionNotApplicable) ||
		!validReason(command.Reason) || !command.Actor.valid() || command.At.IsZero() || command.At.Before(assessment.UpdatedAt) {
		return Assessment{}, ErrState
	}
	if !canParticipate(command.Role) {
		return Assessment{}, ErrRole
	}
	result := assessment
	result.Requirements = append([]Requirement(nil), assessment.Requirements...)
	index := requirementIndex(result.Requirements, command.RequirementID)
	if index < 0 {
		return Assessment{}, ErrInvalid
	}
	requirement := result.Requirements[index]
	for _, decision := range requirement.Evidence {
		if decision.Decision == EvidenceAccepted {
			return Assessment{}, ErrState
		}
	}
	requirement.Disposition = command.Disposition
	requirement.Reason = strings.TrimSpace(command.Reason)
	requirement.RenewAt = nil
	result.Requirements[index] = requirement
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return RestoreAssessment(result)
}

type SubmitPlanCommand struct {
	Plan            PlanBinding
	Actor           Actor
	Role            accounts.MembershipRole
	ExpectedVersion uint64
	At              time.Time
}

func (assessment Assessment) SubmitPlan(command SubmitPlanCommand) (Assessment, error) {
	if command.ExpectedVersion != assessment.Version {
		return Assessment{}, ErrConflict
	}
	plan := command.Plan.normalized()
	plan.AssessmentVersion = assessment.Version
	if assessment.State != StateGapReview || !requirementsReviewed(assessment.Requirements) || !plan.valid(false) ||
		!command.Actor.valid() || command.At.IsZero() || command.At.Before(assessment.UpdatedAt) {
		return Assessment{}, ErrState
	}
	if !canParticipate(command.Role) {
		return Assessment{}, ErrRole
	}
	result := assessment
	result.State = StatePlanApproval
	result.Plan = &plan
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return RestoreAssessment(result)
}

type ApprovePlanCommand struct {
	PlanID            ids.BaselinePlanID
	PlanSHA256        [32]byte
	AssessmentVersion uint64
	Actor             Actor
	Role              accounts.MembershipRole
	ExpectedVersion   uint64
	At                time.Time
}

func (assessment Assessment) ApprovePlan(command ApprovePlanCommand) (Assessment, error) {
	if command.ExpectedVersion != assessment.Version {
		return Assessment{}, ErrConflict
	}
	if assessment.State != StatePlanApproval || assessment.Plan == nil || !command.Actor.valid() || command.At.IsZero() || command.At.Before(assessment.UpdatedAt) {
		return Assessment{}, ErrState
	}
	if !canManage(command.Role) {
		return Assessment{}, ErrRole
	}
	if command.PlanID != assessment.Plan.ID || command.PlanSHA256 != assessment.Plan.ContentSHA256 || command.AssessmentVersion != assessment.Plan.AssessmentVersion {
		return Assessment{}, ErrPlan
	}
	result := assessment
	plan := *assessment.Plan
	actor := command.Actor
	at := command.At.UTC()
	plan.ApprovedBy, plan.ApprovedAt = &actor, &at
	result.Plan = &plan
	result.State = StateActive
	result.Version++
	result.UpdatedAt = at
	return RestoreAssessment(result)
}

type MarkReadyCommand struct {
	Actor           Actor
	Role            accounts.MembershipRole
	ExpectedVersion uint64
	At              time.Time
}

func (assessment Assessment) MarkReady(command MarkReadyCommand) (Assessment, error) {
	if command.ExpectedVersion != assessment.Version {
		return Assessment{}, ErrConflict
	}
	if assessment.State != StateActive || !requirementsReady(assessment.Requirements) || !command.Actor.valid() || command.At.IsZero() || command.At.Before(assessment.UpdatedAt) {
		return Assessment{}, ErrState
	}
	if !canManage(command.Role) {
		return Assessment{}, ErrRole
	}
	result := assessment
	result.State = StateReady
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return RestoreAssessment(result)
}

type StartReassessmentCommand struct {
	NewAssessmentID    ids.BaselineAssessmentID
	CatalogVersion     string
	ScopePolicyVersion string
	Actor              Actor
	Role               accounts.MembershipRole
	ExpectedVersion    uint64
	At                 time.Time
}

func (assessment Assessment) StartReassessment(command StartReassessmentCommand) (Assessment, Assessment, error) {
	if command.ExpectedVersion != assessment.Version {
		return Assessment{}, Assessment{}, ErrConflict
	}
	if assessment.State != StateReady || ids.Validate(string(command.NewAssessmentID)) != nil || command.NewAssessmentID == assessment.ID ||
		!command.Actor.valid() || command.At.IsZero() || command.At.Before(assessment.UpdatedAt) {
		return Assessment{}, Assessment{}, ErrState
	}
	if !canManage(command.Role) {
		return Assessment{}, Assessment{}, ErrRole
	}
	next, err := NewAssessment(AssessmentDraft{ID: command.NewAssessmentID, AccountID: assessment.AccountID, CatalogVersion: command.CatalogVersion, ScopePolicyVersion: command.ScopePolicyVersion, CreatedBy: command.Actor}, command.At)
	if err != nil {
		return Assessment{}, Assessment{}, err
	}
	archived := assessment
	archived.State = StateArchived
	archived.SupersededBy = next.ID
	archived.Version++
	archived.UpdatedAt = command.At.UTC()
	archived, err = RestoreAssessment(archived)
	if err != nil {
		return Assessment{}, Assessment{}, err
	}
	return archived, next, nil
}

func (assessment Assessment) RenewalDue(at time.Time) []ids.BaselineRequirementID {
	if assessment.State != StateActive && assessment.State != StateReady {
		return nil
	}
	result := make([]ids.BaselineRequirementID, 0)
	for _, requirement := range assessment.Requirements {
		if requirement.Disposition == DispositionSatisfied && requirement.RenewAt != nil && !at.UTC().Before(*requirement.RenewAt) {
			result = append(result, requirement.ID)
		}
	}
	return result
}

func requirementIndex(requirements []Requirement, requirementID ids.BaselineRequirementID) int {
	for index := range requirements {
		if requirements[index].ID == requirementID {
			return index
		}
	}
	return -1
}

func requirementsReviewed(requirements []Requirement) bool {
	if len(requirements) == 0 {
		return false
	}
	for _, requirement := range requirements {
		if requirement.Disposition == DispositionPending {
			return false
		}
	}
	return true
}

func requirementsReady(requirements []Requirement) bool {
	if len(requirements) == 0 {
		return false
	}
	for _, requirement := range requirements {
		if requirement.Disposition != DispositionSatisfied && requirement.Disposition != DispositionNotApplicable {
			return false
		}
	}
	return true
}
