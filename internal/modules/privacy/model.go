package privacy

import (
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Category string
type Surface string

const (
	CategoryNecessary Category = "necessary"
	CategoryAnalytics Category = "analytics"
	CategoryMarketing Category = "marketing"

	SurfacePublic  Surface = "public"
	SurfacePrivate Surface = "private"
)

var ErrInvalidDecision = errors.New("privacy consent decision is invalid")

// Decision is one immutable consent choice. Necessary processing is not
// represented as optional consent and therefore is always allowed separately.
type Decision struct {
	ID            ids.ConsentDecisionID `json:"decision_id"`
	SubjectID     ids.ConsentSubjectID  `json:"subject_id"`
	PolicyVersion uint64                `json:"policy_version"`
	Surface       Surface               `json:"surface"`
	Analytics     bool                  `json:"analytics"`
	Marketing     bool                  `json:"marketing"`
	EffectiveAt   time.Time             `json:"effective_at"`
}

// PreferenceReference is the minimal claim protected in the browser cookie.
// It contains no identity and is bound to one host surface and policy version.
type PreferenceReference struct {
	SubjectID     ids.ConsentSubjectID
	PolicyVersion uint64
	Surface       Surface
}

func (r PreferenceReference) Validate() error {
	if ids.Validate(string(r.SubjectID)) != nil || r.PolicyVersion == 0 ||
		(r.Surface != SurfacePublic && r.Surface != SurfacePrivate) {
		return ErrInvalidDecision
	}
	return nil
}

func NewDecision(id ids.ConsentDecisionID, subjectID ids.ConsentSubjectID, policyVersion uint64, surface Surface, analytics, marketing bool, now time.Time) (Decision, error) {
	if ids.Validate(string(id)) != nil || ids.Validate(string(subjectID)) != nil || policyVersion == 0 || now.IsZero() {
		return Decision{}, ErrInvalidDecision
	}
	if surface != SurfacePublic && surface != SurfacePrivate {
		return Decision{}, ErrInvalidDecision
	}
	return Decision{ID: id, SubjectID: subjectID, PolicyVersion: policyVersion, Surface: surface, Analytics: analytics, Marketing: marketing, EffectiveAt: now.UTC()}, nil
}

func (d Decision) Allows(category Category) bool {
	switch category {
	case CategoryNecessary:
		return true
	case CategoryAnalytics:
		return d.Analytics
	case CategoryMarketing:
		return d.Marketing
	default:
		return false
	}
}

func (d Decision) Validate() error {
	_, err := NewDecision(d.ID, d.SubjectID, d.PolicyVersion, d.Surface, d.Analytics, d.Marketing, d.EffectiveAt)
	return err
}
