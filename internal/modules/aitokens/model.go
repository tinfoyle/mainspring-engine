// Package aitokens owns the provider-neutral customer usage ledger semantics.
// Provider usage and internal cost are execution evidence; this package only
// calculates and moves immutable Infinite Ocean AI Token quantities.
package aitokens

import (
	"errors"
	"math"
	"sort"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidGrant       = errors.New("AI Token grant is invalid")
	ErrInvalidReservation = errors.New("AI Token reservation is invalid")
	ErrInsufficient       = errors.New("ai_tokens_insufficient")
	ErrReservationClosed  = errors.New("AI Token reservation is already closed")
	ErrSettlementExceeded = errors.New("AI Token settlement exceeds its frozen reservation")
	ErrGrantInUse         = errors.New("AI Token grant has an active reservation")
	ErrOverflow           = errors.New("AI Token arithmetic exceeds integer range")
)

type GrantOrigin string

const (
	OriginIncluded  GrantOrigin = "included"
	OriginPurchased GrantOrigin = "purchased"
	OriginPromotion GrantOrigin = "promotion"
)

type GrantState string

const (
	GrantActive       GrantState = "active"
	GrantFrozen       GrantState = "frozen"
	GrantExpired      GrantState = "expired"
	GrantReversed     GrantState = "reversed"
	GrantExtinguished GrantState = "extinguished"
)

type Grant struct {
	ID              ids.AITokenGrantID
	AccountID       ids.AccountID
	Origin          GrantOrigin
	DefinitionCode  string
	CatalogVersion  uint64
	SourceReference string
	Quantity        int64
	Available       int64
	Reserved        int64
	Consumed        int64
	State           GrantState
	ExpiresAt       *time.Time
	CreatedAt       time.Time
}

func NewGrant(id ids.AITokenGrantID, accountID ids.AccountID, origin GrantOrigin, definitionCode string, catalogVersion uint64, sourceReference string, quantity int64, expiresAt *time.Time, now time.Time) (Grant, error) {
	grant := Grant{ID: id, AccountID: accountID, Origin: origin, DefinitionCode: definitionCode, CatalogVersion: catalogVersion, SourceReference: sourceReference, Quantity: quantity, Available: quantity, State: GrantActive, ExpiresAt: expiresAt, CreatedAt: now.UTC()}
	if grant.ExpiresAt != nil {
		value := grant.ExpiresAt.UTC()
		grant.ExpiresAt = &value
	}
	return RestoreGrant(grant)
}

func RestoreGrant(grant Grant) (Grant, error) {
	grant.CreatedAt = grant.CreatedAt.UTC()
	if grant.ExpiresAt != nil {
		value := grant.ExpiresAt.UTC()
		grant.ExpiresAt = &value
	}
	validOrigin := grant.Origin == OriginIncluded || grant.Origin == OriginPurchased || grant.Origin == OriginPromotion
	validState := grant.State == GrantActive || grant.State == GrantFrozen || grant.State == GrantExpired || grant.State == GrantReversed || grant.State == GrantExtinguished
	if ids.Validate(string(grant.ID)) != nil || ids.Validate(string(grant.AccountID)) != nil || !validOrigin || grant.DefinitionCode == "" || grant.CatalogVersion == 0 || grant.SourceReference == "" || grant.Quantity <= 0 || grant.Available < 0 || grant.Reserved < 0 || grant.Consumed < 0 || !validState || grant.CreatedAt.IsZero() {
		return Grant{}, ErrInvalidGrant
	}
	if grant.Available > grant.Quantity || grant.Reserved > grant.Quantity-grant.Available || grant.Consumed > grant.Quantity-grant.Available-grant.Reserved {
		return Grant{}, ErrInvalidGrant
	}
	if grant.Origin == OriginPromotion && grant.ExpiresAt == nil {
		return Grant{}, ErrInvalidGrant
	}
	if grant.Origin == OriginPurchased && grant.ExpiresAt != nil {
		return Grant{}, ErrInvalidGrant
	}
	if grant.State != GrantActive && grant.State != GrantFrozen && grant.Available != 0 {
		return Grant{}, ErrInvalidGrant
	}
	return grant, nil
}

type Balance struct {
	Available int64 `json:"available"`
	Reserved  int64 `json:"reserved"`
	Consumed  int64 `json:"consumed"`
	Included  int64 `json:"included"`
	Purchased int64 `json:"purchased"`
	Promotion int64 `json:"promotion"`
}

func Summarize(grants []Grant, now time.Time) (Balance, error) {
	var result Balance
	for _, raw := range grants {
		grant, err := RestoreGrant(raw)
		if err != nil {
			return Balance{}, err
		}
		if err := add(&result.Reserved, grant.Reserved); err != nil {
			return Balance{}, err
		}
		if err := add(&result.Consumed, grant.Consumed); err != nil {
			return Balance{}, err
		}
		available := grant.Available
		if grant.State != GrantActive || (grant.ExpiresAt != nil && !grant.ExpiresAt.After(now)) {
			available = 0
		}
		if err := add(&result.Available, available); err != nil {
			return Balance{}, err
		}
		switch grant.Origin {
		case OriginIncluded:
			err = add(&result.Included, available)
		case OriginPurchased:
			err = add(&result.Purchased, available)
		case OriginPromotion:
			err = add(&result.Promotion, available)
		}
		if err != nil {
			return Balance{}, err
		}
	}
	return result, nil
}

type Allocation struct {
	GrantID ids.AITokenGrantID
	Amount  int64
}

type ReservationState string

const (
	ReservationActive   ReservationState = "active"
	ReservationSettled  ReservationState = "settled"
	ReservationReleased ReservationState = "released"
)

type Reservation struct {
	ID          ids.AITokenReservationID
	AccountID   ids.AccountID
	RequestID   string
	Rate        catalog.AIComplexityRate
	Maximum     int64
	Settled     int64
	Allocations []Allocation
	State       ReservationState
	CreatedAt   time.Time
	ClosedAt    *time.Time
}

// Reserve deterministically allocates earliest-expiring grants first and
// non-expiring purchased grants last. It returns copies and never partially
// mutates the caller's slice when the balance is insufficient.
func Reserve(grants []Grant, reservation Reservation, now time.Time) ([]Grant, Reservation, error) {
	if ids.Validate(string(reservation.ID)) != nil || ids.Validate(string(reservation.AccountID)) != nil || ids.Validate(reservation.RequestID) != nil || reservation.Rate.Code == "" || reservation.Rate.Version == 0 || reservation.Maximum <= 0 || reservation.Maximum > reservation.Rate.MaximumReservation || reservation.State != ReservationActive || !reservation.CreatedAt.IsZero() || len(reservation.Allocations) != 0 {
		return nil, Reservation{}, ErrInvalidReservation
	}
	result := append([]Grant(nil), grants...)
	indexes := make([]int, 0, len(result))
	for index, raw := range result {
		grant, err := RestoreGrant(raw)
		if err != nil || grant.AccountID != reservation.AccountID {
			return nil, Reservation{}, ErrInvalidGrant
		}
		result[index] = grant
		if grant.State == GrantActive && grant.Available > 0 && (grant.ExpiresAt == nil || grant.ExpiresAt.After(now)) {
			indexes = append(indexes, index)
		}
	}
	sort.Slice(indexes, func(left, right int) bool { return grantBefore(result[indexes[left]], result[indexes[right]]) })
	remaining := reservation.Maximum
	allocations := make([]Allocation, 0, len(indexes))
	for _, index := range indexes {
		amount := min(remaining, result[index].Available)
		if amount == 0 {
			continue
		}
		result[index].Available -= amount
		result[index].Reserved += amount
		allocations = append(allocations, Allocation{GrantID: result[index].ID, Amount: amount})
		remaining -= amount
		if remaining == 0 {
			break
		}
	}
	if remaining != 0 {
		return nil, Reservation{}, ErrInsufficient
	}
	reservation.Allocations = allocations
	reservation.CreatedAt = now.UTC()
	return result, reservation, nil
}

// Close settles an exact trusted debit and releases the unused maximum back to
// the same cohorts. Expired or reversed cohorts never regain availability.
func Close(grants []Grant, reservation Reservation, settled int64, now time.Time) ([]Grant, Reservation, error) {
	if reservation.State != ReservationActive {
		return nil, Reservation{}, ErrReservationClosed
	}
	if settled < 0 || settled > reservation.Maximum {
		return nil, Reservation{}, ErrSettlementExceeded
	}
	result := append([]Grant(nil), grants...)
	byID := make(map[ids.AITokenGrantID]int, len(result))
	for index, raw := range result {
		grant, err := RestoreGrant(raw)
		if err != nil || grant.AccountID != reservation.AccountID {
			return nil, Reservation{}, ErrInvalidGrant
		}
		result[index], byID[grant.ID] = grant, index
	}
	remaining := settled
	for _, allocation := range reservation.Allocations {
		index, exists := byID[allocation.GrantID]
		if !exists || allocation.Amount <= 0 || result[index].Reserved < allocation.Amount {
			return nil, Reservation{}, ErrInvalidReservation
		}
		debit := min(remaining, allocation.Amount)
		release := allocation.Amount - debit
		result[index].Reserved -= allocation.Amount
		result[index].Consumed += debit
		if release > 0 && result[index].State == GrantActive && (result[index].ExpiresAt == nil || result[index].ExpiresAt.After(now)) {
			result[index].Available += release
		}
		remaining -= debit
	}
	if remaining != 0 {
		return nil, Reservation{}, ErrInvalidReservation
	}
	closedAt := now.UTC()
	reservation.Settled, reservation.ClosedAt = settled, &closedAt
	if settled == 0 {
		reservation.State = ReservationReleased
	} else {
		reservation.State = ReservationSettled
	}
	return result, reservation, nil
}

// ReverseUnused makes the unused remainder unavailable without rewriting any
// consumed history. Refund review must wait until active reservations clear.
func ReverseUnused(grant Grant, finalState GrantState) (Grant, error) {
	grant, err := RestoreGrant(grant)
	if err != nil || (finalState != GrantReversed && finalState != GrantExpired && finalState != GrantExtinguished) {
		return Grant{}, ErrInvalidGrant
	}
	if grant.Reserved != 0 {
		return Grant{}, ErrGrantInUse
	}
	grant.Available, grant.State = 0, finalState
	return RestoreGrant(grant)
}

func Charge(rate catalog.AIComplexityRate, input, cachedInput, output int64, toolInvocations int64) (int64, error) {
	if input < 0 || cachedInput < 0 || output < 0 || toolInvocations < 0 || cachedInput > input || rate.MinimumCharge <= 0 || rate.MaximumReservation < rate.MinimumCharge {
		return 0, ErrInvalidReservation
	}
	uncached := input - cachedInput
	parts := [][2]int64{{uncached, rate.InputPerThousand}, {cachedInput, rate.CachedInputPerThousand}, {output, rate.OutputPerThousand}, {toolInvocations, rate.ToolInvocation * 1000}}
	var milliTokens int64
	for _, part := range parts {
		if part[0] != 0 && part[1] > math.MaxInt64/part[0] {
			return 0, ErrOverflow
		}
		if part[0]*part[1] > math.MaxInt64-milliTokens {
			return 0, ErrOverflow
		}
		milliTokens += part[0] * part[1]
	}
	charge := (milliTokens + 999) / 1000
	if charge < rate.MinimumCharge {
		charge = rate.MinimumCharge
	}
	if charge > rate.MaximumReservation {
		return 0, ErrSettlementExceeded
	}
	return charge, nil
}

func grantBefore(left, right Grant) bool {
	if left.ExpiresAt == nil && right.ExpiresAt != nil {
		return false
	}
	if left.ExpiresAt != nil && right.ExpiresAt == nil {
		return true
	}
	if left.ExpiresAt != nil && !left.ExpiresAt.Equal(*right.ExpiresAt) {
		return left.ExpiresAt.Before(*right.ExpiresAt)
	}
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.Before(right.CreatedAt)
	}
	return left.ID < right.ID
}

func add(total *int64, value int64) error {
	if value > math.MaxInt64-*total {
		return ErrOverflow
	}
	*total += value
	return nil
}
