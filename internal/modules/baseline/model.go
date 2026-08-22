// Package baseline owns the versioned assessment lifecycle that turns
// accepted Knowledge references and explicit evidence decisions into an
// approved, repeatable Work plan.
package baseline

import (
	"crypto/sha256"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumCodeLength          = 128
	MaximumReasonLength        = 1000
	MaximumQuestionLength      = 4000
	MaximumRequirementTitle    = 300
	MaximumAssessmentAnswers   = 256
	MaximumRequirements        = 256
	MaximumRequirementEvidence = 128
)

var (
	ErrInvalid  = errors.New("baseline aggregate is invalid")
	ErrConflict = errors.New("baseline aggregate version conflicts with the command")
	ErrState    = errors.New("baseline aggregate state rejects the command")
	ErrRole     = errors.New("baseline role is not eligible")
	ErrPlan     = errors.New("baseline plan binding does not match")
)

var codePattern = regexp.MustCompile(`^[a-z][a-z0-9._:/-]{0,127}$`)

type Actor struct {
	UserID ids.UserID
}

func (actor Actor) valid() bool { return ids.Validate(string(actor.UserID)) == nil }

func canParticipate(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator || role == accounts.RoleMember
}

func canManage(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator
}

type AssessmentState string

const (
	StateInterview    AssessmentState = "interview"
	StateInventory    AssessmentState = "inventory"
	StateGapReview    AssessmentState = "gap_review"
	StatePlanApproval AssessmentState = "plan_approval"
	StateActive       AssessmentState = "active"
	StateReady        AssessmentState = "ready"
	StateArchived     AssessmentState = "archived"
)

func (state AssessmentState) valid() bool {
	switch state {
	case StateInterview, StateInventory, StateGapReview, StatePlanApproval, StateActive, StateReady, StateArchived:
		return true
	default:
		return false
	}
}

type AnswerKind string

const (
	AnswerFact    AnswerKind = "fact"
	AnswerUnknown AnswerKind = "unknown"
)

type FactReference struct {
	FactID   ids.KnowledgeFactID
	Revision uint64
}

func (reference FactReference) valid() bool {
	return ids.Validate(string(reference.FactID)) == nil && reference.Revision > 0
}

type InterviewAnswer struct {
	QuestionKey string
	Kind        AnswerKind
	Fact        *FactReference
	Reason      string
	AnsweredBy  Actor
	AnsweredAt  time.Time
}

func (answer InterviewAnswer) normalized() InterviewAnswer {
	answer.QuestionKey = strings.TrimSpace(answer.QuestionKey)
	answer.Reason = strings.TrimSpace(answer.Reason)
	answer.AnsweredAt = answer.AnsweredAt.UTC()
	if answer.Fact != nil {
		value := *answer.Fact
		answer.Fact = &value
	}
	return answer
}

func (answer InterviewAnswer) valid() bool {
	if !codePattern.MatchString(answer.QuestionKey) || !answer.AnsweredBy.valid() || answer.AnsweredAt.IsZero() {
		return false
	}
	switch answer.Kind {
	case AnswerFact:
		return answer.Fact != nil && answer.Fact.valid() && answer.Reason == ""
	case AnswerUnknown:
		return answer.Fact == nil && validReason(answer.Reason)
	default:
		return false
	}
}

type RequirementDisposition string

const (
	DispositionPending       RequirementDisposition = "pending"
	DispositionSatisfied     RequirementDisposition = "satisfied"
	DispositionGap           RequirementDisposition = "gap"
	DispositionNotApplicable RequirementDisposition = "not_applicable"
)

func (disposition RequirementDisposition) valid() bool {
	return disposition == DispositionPending || disposition == DispositionSatisfied || disposition == DispositionGap || disposition == DispositionNotApplicable
}

type ResponsibilityKind string

const (
	ResponsibilityAccount ResponsibilityKind = "account"
	ResponsibilityUser    ResponsibilityKind = "user"
	ResponsibilityPersona ResponsibilityKind = "persona"
)

type Responsibility struct {
	Kind ResponsibilityKind
	ID   string
}

func (responsibility Responsibility) valid() bool {
	switch responsibility.Kind {
	case ResponsibilityAccount:
		return responsibility.ID == ""
	case ResponsibilityUser, ResponsibilityPersona:
		return ids.Validate(responsibility.ID) == nil
	default:
		return false
	}
}

type EvidenceDecisionKind string

const (
	EvidenceAccepted EvidenceDecisionKind = "accepted"
	EvidenceRejected EvidenceDecisionKind = "rejected"
)

type EvidenceDecision struct {
	EvidenceID ids.KnowledgeEvidenceID
	Decision   EvidenceDecisionKind
	Reason     string
	DecidedBy  Actor
	DecidedAt  time.Time
}

func (decision EvidenceDecision) normalized() EvidenceDecision {
	decision.Reason = strings.TrimSpace(decision.Reason)
	decision.DecidedAt = decision.DecidedAt.UTC()
	return decision
}

func (decision EvidenceDecision) valid() bool {
	return ids.Validate(string(decision.EvidenceID)) == nil &&
		(decision.Decision == EvidenceAccepted || decision.Decision == EvidenceRejected) &&
		validReason(decision.Reason) && decision.DecidedBy.valid() && !decision.DecidedAt.IsZero()
}

type RequirementDraft struct {
	ID                 ids.BaselineRequirementID
	Code               string
	Title              string
	Responsibility     Responsibility
	RenewAfterDays     uint16
	CatalogVersion     string
	ScopePolicyVersion string
}

type Requirement struct {
	RequirementDraft
	Disposition RequirementDisposition
	Reason      string
	Evidence    []EvidenceDecision
	RenewAt     *time.Time
}

func (requirement Requirement) normalized() Requirement {
	requirement.Code = strings.TrimSpace(requirement.Code)
	requirement.Title = strings.TrimSpace(requirement.Title)
	requirement.CatalogVersion = strings.TrimSpace(requirement.CatalogVersion)
	requirement.ScopePolicyVersion = strings.TrimSpace(requirement.ScopePolicyVersion)
	requirement.Reason = strings.TrimSpace(requirement.Reason)
	requirement.Evidence = append([]EvidenceDecision(nil), requirement.Evidence...)
	for index := range requirement.Evidence {
		requirement.Evidence[index] = requirement.Evidence[index].normalized()
	}
	if requirement.RenewAt != nil {
		value := requirement.RenewAt.UTC()
		requirement.RenewAt = &value
	}
	return requirement
}

func (requirement Requirement) valid(assessmentCatalogVersion, assessmentScopePolicyVersion string) bool {
	if ids.Validate(string(requirement.ID)) != nil || !codePattern.MatchString(requirement.Code) ||
		requirement.Title == "" || len(requirement.Title) > MaximumRequirementTitle || !requirement.Responsibility.valid() ||
		requirement.RenewAfterDays > 3650 || requirement.CatalogVersion != assessmentCatalogVersion ||
		requirement.ScopePolicyVersion != assessmentScopePolicyVersion || !requirement.Disposition.valid() ||
		len(requirement.Evidence) > MaximumRequirementEvidence {
		return false
	}
	seen := make(map[ids.KnowledgeEvidenceID]struct{}, len(requirement.Evidence))
	accepted := false
	for _, decision := range requirement.Evidence {
		if !decision.valid() {
			return false
		}
		if _, exists := seen[decision.EvidenceID]; exists {
			return false
		}
		seen[decision.EvidenceID] = struct{}{}
		accepted = accepted || decision.Decision == EvidenceAccepted
	}
	switch requirement.Disposition {
	case DispositionPending:
		return requirement.Reason == "" && requirement.RenewAt == nil && !accepted
	case DispositionSatisfied:
		if !accepted || requirement.Reason != "" {
			return false
		}
		return (requirement.RenewAfterDays == 0 && requirement.RenewAt == nil) || (requirement.RenewAfterDays > 0 && requirement.RenewAt != nil)
	case DispositionGap, DispositionNotApplicable:
		return validReason(requirement.Reason) && requirement.RenewAt == nil && !accepted
	default:
		return false
	}
}

type PlanBinding struct {
	ID                ids.BaselinePlanID
	AssessmentVersion uint64
	ContentSHA256     [sha256.Size]byte
	ProposedWorkCount uint16
	ApprovedBy        *Actor
	ApprovedAt        *time.Time
}

func (plan PlanBinding) normalized() PlanBinding {
	if plan.ApprovedBy != nil {
		value := *plan.ApprovedBy
		plan.ApprovedBy = &value
	}
	if plan.ApprovedAt != nil {
		value := plan.ApprovedAt.UTC()
		plan.ApprovedAt = &value
	}
	return plan
}

func (plan PlanBinding) valid(approved bool) bool {
	if ids.Validate(string(plan.ID)) != nil || plan.AssessmentVersion == 0 || plan.ContentSHA256 == ([sha256.Size]byte{}) || plan.ProposedWorkCount > MaximumRequirements {
		return false
	}
	if approved {
		return plan.ApprovedBy != nil && plan.ApprovedBy.valid() && plan.ApprovedAt != nil && !plan.ApprovedAt.IsZero()
	}
	return plan.ApprovedBy == nil && plan.ApprovedAt == nil
}

func validVersionLabel(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 200 && !strings.ContainsRune(value, '\x00')
}

func validReason(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 3 && len(value) <= MaximumReasonLength && !strings.ContainsRune(value, '\x00')
}
