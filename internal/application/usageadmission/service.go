// Package usageadmission is the transport-neutral package and capacity guard.
// HTTP, MCP, schedules, workers, and tools call this application boundary
// rather than interpreting entitlement snapshots independently.
package usageadmission

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidRequest      = errors.New("usage reservation request is invalid")
	ErrReservationConflict = errors.New("usage reservation key conflicts with an existing request")
	ErrReservationClosed   = errors.New("usage reservation is already closed")
	ErrEntitlementChanged  = errors.New("entitlement changed during usage admission")
	ErrCorruptUsage        = errors.New("usage counter state is corrupt")
)

type ReservationState string

const (
	ReservationActive   ReservationState = "active"
	ReservationReleased ReservationState = "released"
	ReservationExpired  ReservationState = "expired"
)

type Reservation struct {
	ID                 string
	AccountID          ids.AccountID
	RequestID          string
	PackageCode        catalog.PackageCode
	LimitCode          catalog.LimitCode
	Amount             int64
	Current            int64
	Maximum            int64
	EntitlementVersion uint64
	State              ReservationState
	ExpiresAt          *time.Time
	CreatedAt          time.Time
	ClosedAt           *time.Time
	NewlyCreated       bool
}

type PersistCommand struct {
	ID                         string
	AccountID                  ids.AccountID
	RequestID                  string
	PackageCode                catalog.PackageCode
	LimitCode                  catalog.LimitCode
	Amount                     int64
	Maximum                    int64
	ExpectedEntitlementVersion uint64
	ExpiresAt                  *time.Time
	Now                        time.Time
}

type CapacityExceededError struct {
	Current int64
	Maximum int64
}

func (e *CapacityExceededError) Error() string { return string(access.DenialLimitExceeded) }

type Store interface {
	Reserve(context.Context, PersistCommand) (Reservation, error)
	Release(context.Context, ids.AccountID, string, time.Time) (Reservation, error)
}

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	authorizer Authorizer
	store      Store
	ids        ids.Generator
	clock      Clock
}

func NewService(authorizer Authorizer, store Store, generator ids.Generator, clock Clock) (*Service, error) {
	if authorizer == nil || store == nil || generator == nil || clock == nil {
		return nil, errors.New("usage admission dependencies are required")
	}
	return &Service{authorizer: authorizer, store: store, ids: generator, clock: clock}, nil
}

type ReserveCommand struct {
	Actor       access.Actor
	AccountID   ids.AccountID
	PackageCode catalog.PackageCode
	LimitCode   catalog.LimitCode
	Amount      int64
	RequestID   string
}

func (s *Service) Reserve(ctx context.Context, command ReserveCommand) (Reservation, error) {
	if !command.Actor.Valid() || command.AccountID == "" || command.PackageCode == "" || command.LimitCode == "" || command.Amount <= 0 || ids.Validate(command.RequestID) != nil {
		return Reservation{}, ErrInvalidRequest
	}
	for attempt := 0; attempt < 2; attempt++ {
		accountContext, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Package: command.PackageCode, Mutation: true})
		if err != nil {
			return Reservation{}, err
		}
		if accountContext.PackageAccess == nil {
			return Reservation{}, &access.DeniedError{Code: access.DenialCorruptContext, Package: command.PackageCode}
		}
		maximum, hasMaximum := accountContext.PackageAccess.Limits[command.LimitCode]
		policy, hasPolicy := accountContext.PackageAccess.LimitPolicies[command.LimitCode]
		if !hasMaximum || !hasPolicy || policy.Kind != catalog.LimitKindCapacity || policy.ReservationTTLSeconds < 0 || policy.ReservationTTLSeconds > int64((30*24*time.Hour)/time.Second) {
			return Reservation{}, &access.DeniedError{Code: access.DenialLimitNotDefined, Package: command.PackageCode, Limit: command.LimitCode}
		}
		if maximum <= 0 || command.Amount > maximum {
			return Reservation{}, &access.DeniedError{Code: access.DenialLimitExceeded, Package: command.PackageCode, Limit: command.LimitCode, Maximum: maximum}
		}
		now := s.clock.Now().UTC()
		var expiresAt *time.Time
		if policy.ReservationTTLSeconds > 0 {
			value := now.Add(time.Duration(policy.ReservationTTLSeconds) * time.Second)
			expiresAt = &value
		}
		reservation, err := s.store.Reserve(ctx, PersistCommand{ID: s.ids.New(), AccountID: command.AccountID, RequestID: command.RequestID, PackageCode: command.PackageCode, LimitCode: command.LimitCode, Amount: command.Amount, Maximum: maximum, ExpectedEntitlementVersion: accountContext.EntitlementVersion, ExpiresAt: expiresAt, Now: now})
		if errors.Is(err, ErrEntitlementChanged) {
			continue
		}
		var exceeded *CapacityExceededError
		if errors.As(err, &exceeded) {
			return Reservation{}, &access.DeniedError{Code: access.DenialLimitExceeded, Package: command.PackageCode, Limit: command.LimitCode, Current: exceeded.Current, Maximum: exceeded.Maximum}
		}
		if err == nil && reservation.State != ReservationActive {
			return Reservation{}, ErrReservationClosed
		}
		return reservation, err
	}
	return Reservation{}, ErrEntitlementChanged
}

type ReleaseCommand struct {
	Actor     access.Actor
	AccountID ids.AccountID
	RequestID string
}

// Release checks Account Membership but intentionally does not require the
// package to remain entitled: downgrade and cancellation paths must be able to
// return capacity after new mutations have been disabled.
func (s *Service) Release(ctx context.Context, command ReleaseCommand) (Reservation, error) {
	if !command.Actor.Valid() || command.AccountID == "" || ids.Validate(command.RequestID) != nil {
		return Reservation{}, ErrInvalidRequest
	}
	if _, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{}); err != nil {
		return Reservation{}, err
	}
	return s.store.Release(ctx, command.AccountID, command.RequestID, s.clock.Now().UTC())
}
