package memory

import (
	"context"
	"sync"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/aitokenledger"
	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AITokenLedger struct {
	mu                sync.Mutex
	grants            map[ids.AccountID][]aitokens.Grant
	reservations      map[ids.AccountID]map[string]aitokens.Reservation
	promotionVersions map[ids.AITokenGrantID]uint64
}

func NewAITokenLedger() *AITokenLedger {
	return &AITokenLedger{grants: map[ids.AccountID][]aitokens.Grant{}, reservations: map[ids.AccountID]map[string]aitokens.Reservation{}, promotionVersions: map[ids.AITokenGrantID]uint64{}}
}

func (s *AITokenLedger) Balance(_ context.Context, accountID ids.AccountID, now time.Time) (aitokens.Balance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return aitokens.Summarize(s.grants[accountID], now.UTC())
}

func (s *AITokenLedger) Reserve(_ context.Context, requested aitokens.Reservation, now time.Time) (aitokens.Reservation, aitokens.Balance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, exists := s.reservations[requested.AccountID][requested.RequestID]; exists {
		if existing.Rate.Complexity != requested.Rate.Complexity {
			return aitokens.Reservation{}, aitokens.Balance{}, aitokens.ErrInvalidReservation
		}
		balance, err := aitokens.Summarize(s.grants[requested.AccountID], now.UTC())
		return existing, balance, err
	}
	grants, reservation, err := aitokens.Reserve(s.grants[requested.AccountID], requested, now.UTC())
	if err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	if s.reservations[requested.AccountID] == nil {
		s.reservations[requested.AccountID] = map[string]aitokens.Reservation{}
	}
	s.grants[requested.AccountID], s.reservations[requested.AccountID][requested.RequestID] = grants, reservation
	balance, err := aitokens.Summarize(grants, now.UTC())
	return reservation, balance, err
}

func (s *AITokenLedger) Close(_ context.Context, accountID ids.AccountID, requestID string, usage aitokenledger.Usage, now time.Time) (aitokens.Reservation, aitokens.Balance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reservation, exists := s.reservations[accountID][requestID]
	if !exists {
		return aitokens.Reservation{}, aitokens.Balance{}, aitokens.ErrInvalidReservation
	}
	if reservation.State != aitokens.ReservationActive {
		balance, err := aitokens.Summarize(s.grants[accountID], now.UTC())
		return reservation, balance, err
	}
	var settled int64
	var err error
	if usage.ProviderStarted {
		settled, err = aitokens.Charge(reservation.Rate, usage.InputTokens, usage.CachedInputTokens, usage.OutputTokens, usage.ToolInvocations)
		if err != nil {
			return aitokens.Reservation{}, aitokens.Balance{}, err
		}
	}
	grants, reservation, err := aitokens.Close(s.grants[accountID], reservation, settled, now.UTC())
	if err != nil {
		return aitokens.Reservation{}, aitokens.Balance{}, err
	}
	s.grants[accountID], s.reservations[accountID][requestID] = grants, reservation
	balance, err := aitokens.Summarize(grants, now.UTC())
	return reservation, balance, err
}

func (s *AITokenLedger) Issue(_ context.Context, requested aitokens.Grant, expireIncluded bool) (aitokens.Grant, aitokens.Balance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if requested.Origin == aitokens.OriginPromotion {
		return aitokens.Grant{}, aitokens.Balance{}, aitokens.ErrInvalidGrant
	}
	grants := append([]aitokens.Grant(nil), s.grants[requested.AccountID]...)
	for _, grant := range grants {
		if grant.Origin == requested.Origin && grant.DefinitionCode == requested.DefinitionCode && grant.SourceReference == requested.SourceReference {
			if grant.Quantity != requested.Quantity || grant.CatalogVersion != requested.CatalogVersion {
				return aitokens.Grant{}, aitokens.Balance{}, aitokens.ErrInvalidGrant
			}
			balance, err := aitokens.Summarize(grants, requested.CreatedAt)
			return grant, balance, err
		}
	}
	if expireIncluded {
		for index := range grants {
			if grants[index].Origin != aitokens.OriginIncluded || (grants[index].State != aitokens.GrantActive && grants[index].State != aitokens.GrantFrozen) {
				continue
			}
			grants[index].Available, grants[index].State = 0, aitokens.GrantExpired
		}
	}
	grants = append(grants, requested)
	s.grants[requested.AccountID] = grants
	balance, err := aitokens.Summarize(grants, requested.CreatedAt)
	return requested, balance, err
}

func (s *AITokenLedger) Promotion(_ context.Context, accountID ids.AccountID, sourceReference string, now time.Time) (aitokens.Grant, aitokens.Balance, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	grants := s.grants[accountID]
	for _, grant := range grants {
		if grant.Origin == aitokens.OriginPromotion && grant.SourceReference == sourceReference {
			balance, err := aitokens.Summarize(grants, now.UTC())
			return grant, balance, true, err
		}
	}
	return aitokens.Grant{}, aitokens.Balance{}, false, nil
}

func (s *AITokenLedger) RedeemPromotion(_ context.Context, requested aitokens.Grant, campaignVersion uint64, perAccountLimit, issuanceCap int64) (aitokens.Grant, aitokens.Balance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	grants := append([]aitokens.Grant(nil), s.grants[requested.AccountID]...)
	for _, grant := range grants {
		if grant.Origin != aitokens.OriginPromotion || grant.SourceReference != requested.SourceReference {
			continue
		}
		if grant.DefinitionCode != requested.DefinitionCode || grant.Quantity != requested.Quantity || grant.CatalogVersion != requested.CatalogVersion || !sameTimePointer(grant.ExpiresAt, requested.ExpiresAt) {
			return aitokens.Grant{}, aitokens.Balance{}, aitokenledger.ErrPromotionConflict
		}
		balance, err := aitokens.Summarize(grants, requested.CreatedAt)
		return grant, balance, err
	}
	var accountRedemptions, totalRedemptions int64
	for accountID, accountGrants := range s.grants {
		for _, grant := range accountGrants {
			if grant.Origin == aitokens.OriginPromotion && grant.DefinitionCode == requested.DefinitionCode && s.promotionVersions[grant.ID] == campaignVersion {
				totalRedemptions++
				if accountID == requested.AccountID {
					accountRedemptions++
				}
			}
		}
	}
	if accountRedemptions >= perAccountLimit {
		return aitokens.Grant{}, aitokens.Balance{}, aitokenledger.ErrPromotionAccountLimit
	}
	if totalRedemptions >= issuanceCap {
		return aitokens.Grant{}, aitokens.Balance{}, aitokenledger.ErrPromotionIssuanceLimit
	}
	grants = append(grants, requested)
	s.grants[requested.AccountID], s.promotionVersions[requested.ID] = grants, campaignVersion
	balance, err := aitokens.Summarize(grants, requested.CreatedAt)
	return requested, balance, err
}

func sameTimePointer(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Equal(*right)
}

func (s *AITokenLedger) ReverseUnused(_ context.Context, accountID ids.AccountID, origin aitokens.GrantOrigin, sourceReference, _ string, now time.Time) (aitokens.Grant, aitokens.Balance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	grants := append([]aitokens.Grant(nil), s.grants[accountID]...)
	for index := range grants {
		if grants[index].Origin != origin || grants[index].SourceReference != sourceReference {
			continue
		}
		if grants[index].State == aitokens.GrantReversed {
			balance, err := aitokens.Summarize(grants, now.UTC())
			return grants[index], balance, err
		}
		reversed, err := aitokens.ReverseUnused(grants[index], aitokens.GrantReversed)
		if err != nil {
			return aitokens.Grant{}, aitokens.Balance{}, err
		}
		grants[index] = reversed
		s.grants[accountID] = grants
		balance, err := aitokens.Summarize(grants, now.UTC())
		return reversed, balance, err
	}
	return aitokens.Grant{}, aitokens.Balance{}, aitokens.ErrInvalidGrant
}

var _ aitokenledger.Store = (*AITokenLedger)(nil)
