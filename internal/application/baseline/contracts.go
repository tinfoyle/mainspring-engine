// Package baseline provides the transport-neutral, Account-authorized command
// boundary for Baseline assessments.
package baseline

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalid    = errors.New("baseline command is invalid")
	ErrNotFound   = errors.New("baseline assessment was not found")
	ErrConflict   = errors.New("baseline assessment version conflict")
	ErrConstraint = errors.New("baseline assessment constraint failed")
	ErrRepository = errors.New("baseline persistence is unavailable")
)

var reasonCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,99}$`)

type Mutation struct {
	Actor         domain.Actor
	ReasonCode    string
	CorrelationID string
	At            time.Time
}

func (mutation Mutation) Valid() bool {
	return ids.Validate(string(mutation.Actor.UserID)) == nil && reasonCodePattern.MatchString(mutation.ReasonCode) &&
		ids.Validate(mutation.CorrelationID) == nil && !mutation.At.IsZero()
}

type Transition struct {
	EventType     string
	RequirementID ids.BaselineRequirementID
	PlanID        ids.BaselinePlanID
}

func (transition Transition) Valid() bool {
	switch transition.EventType {
	case "interview_answered", "inventory_started", "inventory_completed", "evidence_decided", "requirement_dispositioned", "plan_submitted", "plan_approved", "assessment_ready", "work_evidence_confirmed":
	default:
		return false
	}
	if (transition.EventType == "evidence_decided" || transition.EventType == "requirement_dispositioned" || transition.EventType == "work_evidence_confirmed") != (transition.RequirementID != "") {
		return false
	}
	if transition.RequirementID != "" && ids.Validate(string(transition.RequirementID)) != nil {
		return false
	}
	if (transition.EventType == "plan_submitted" || transition.EventType == "plan_approved") != (transition.PlanID != "") {
		return false
	}
	return transition.PlanID == "" || ids.Validate(string(transition.PlanID)) == nil
}

type Repository interface {
	Create(context.Context, domain.Assessment, Mutation) (domain.Assessment, error)
	Get(context.Context, ids.AccountID, ids.BaselineAssessmentID) (domain.Assessment, error)
	Update(context.Context, domain.Assessment, uint64, Transition, Mutation) (domain.Assessment, error)
	Reassess(context.Context, domain.Assessment, domain.Assessment, uint64, Mutation) (domain.Assessment, domain.Assessment, error)
}

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Clock interface{ Now() time.Time }

type ResolvedFact struct {
	Reference      domain.FactReference
	Key            string
	CanonicalValue json.RawMessage
}

type FactResolver interface {
	Resolve(context.Context, ids.AccountID, []domain.FactReference) ([]ResolvedFact, error)
}

type ResolvedEvidence struct {
	ID   ids.KnowledgeEvidenceID
	Kind knowledge.SourceKind
}

type EvidenceResolver interface {
	ResolveEvidence(context.Context, ids.AccountID, ids.KnowledgeEvidenceID) (ResolvedEvidence, error)
}

type ResolvedWork struct {
	ID                    ids.WorkItemID
	State                 workdomain.State
	BaselineRequirementID ids.BaselineRequirementID
	CompletedAt           *time.Time
}

type WorkResolver interface {
	ResolveWork(context.Context, ids.AccountID, ids.WorkItemID) (ResolvedWork, error)
}

type SourceGrantPage struct {
	Items      []domain.SourceGrant
	NextCursor ids.BaselineSourceGrantID
}

type SourceGrantRepository interface {
	CreateSourceGrant(context.Context, domain.SourceGrant, Mutation) (domain.SourceGrant, error)
	GetSourceGrant(context.Context, ids.AccountID, ids.BaselineSourceGrantID) (domain.SourceGrant, error)
	ListSourceGrants(context.Context, ids.AccountID, ids.BaselineAssessmentID, ids.BaselineSourceGrantID, uint16) (SourceGrantPage, error)
	UpdateSourceGrant(context.Context, domain.SourceGrant, uint64, Mutation) (domain.SourceGrant, error)
}

type ResolvedSourceConnection struct {
	Kind           domain.SourceKind
	Capabilities   []string
	DriveFolderIDs []string
}

type SourceConnectionResolver interface {
	ResolveSourceConnection(context.Context, ids.AccountID, string) (ResolvedSourceConnection, error)
}

type WorkCreator interface {
	Create(context.Context, workapp.CreateCommand) (workdomain.Item, error)
}

func validBase(actor access.Actor, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID, correlationID string) bool {
	return actor.UserID != "" && actor.WorkloadID == "" && ids.Validate(string(actor.UserID)) == nil && ids.Validate(string(accountID)) == nil &&
		ids.Validate(string(assessmentID)) == nil && ids.Validate(strings.TrimSpace(correlationID)) == nil
}
