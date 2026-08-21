package knowledge

import (
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type FactState string

const (
	FactActive FactState = "active"
	FactStale  FactState = "stale"
)

type Fact struct {
	ID             ids.KnowledgeFactID
	AccountID      ids.AccountID
	Scope          Scope
	Key            string
	CurrentClaimID ids.KnowledgeClaimID
	State          FactState
	Revision       uint64
	AcceptedBy     ids.UserID
	AcceptedAt     time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func NewFact(id ids.KnowledgeFactID, claim Claim) (Fact, error) {
	if ids.Validate(string(id)) != nil || claim.State != ClaimAccepted || claim.Decision == nil {
		return Fact{}, ErrInvalid
	}
	fact := Fact{ID: id, AccountID: claim.AccountID, Scope: claim.Scope, Key: claim.Key, CurrentClaimID: claim.ID, State: FactActive, Revision: 1, AcceptedBy: claim.Decision.DecidedBy, AcceptedAt: claim.Decision.DecidedAt, CreatedAt: claim.Decision.DecidedAt, UpdatedAt: claim.Decision.DecidedAt}
	return RestoreFact(fact)
}

func RestoreFact(fact Fact) (Fact, error) {
	fact.AcceptedAt, fact.CreatedAt, fact.UpdatedAt = fact.AcceptedAt.UTC(), fact.CreatedAt.UTC(), fact.UpdatedAt.UTC()
	if ids.Validate(string(fact.ID)) != nil || ids.Validate(string(fact.AccountID)) != nil || !fact.Scope.Valid() || !codePattern.MatchString(fact.Key) || ids.Validate(string(fact.CurrentClaimID)) != nil || (fact.State != FactActive && fact.State != FactStale) || fact.Revision == 0 || ids.Validate(string(fact.AcceptedBy)) != nil || fact.AcceptedAt.IsZero() || fact.CreatedAt.IsZero() || fact.UpdatedAt.Before(fact.CreatedAt) || fact.AcceptedAt.After(fact.UpdatedAt) {
		return Fact{}, ErrInvalid
	}
	return fact, nil
}

func (fact Fact) Revise(claim Claim) (Fact, error) {
	if fact.State != FactActive || claim.State != ClaimAccepted || claim.Decision == nil || fact.AccountID != claim.AccountID || fact.Scope != claim.Scope || fact.Key != claim.Key || !claim.Decision.DecidedAt.After(fact.AcceptedAt) {
		return Fact{}, ErrState
	}
	result := fact
	result.CurrentClaimID = claim.ID
	result.Revision++
	result.AcceptedBy = claim.Decision.DecidedBy
	result.AcceptedAt = claim.Decision.DecidedAt
	result.UpdatedAt = claim.Decision.DecidedAt
	return RestoreFact(result)
}
