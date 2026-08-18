package usageadmission

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	testUserID    ids.UserID    = "10000000-0000-4000-8000-000000000001"
	testAccountID ids.AccountID = "20000000-0000-4000-8000-000000000002"
	testRequestID               = "30000000-0000-4000-8000-000000000003"
)

type stateSource struct{ state access.State }

func (s stateSource) AccessState(context.Context, ids.UserID, ids.AccountID) (access.State, error) {
	return s.state, nil
}

type store struct {
	command    PersistCommand
	result     Reservation
	err        error
	callCount  int
	releaseKey string
}

func (s *store) Reserve(_ context.Context, command PersistCommand) (Reservation, error) {
	s.command, s.callCount = command, s.callCount+1
	if errors.Is(s.err, ErrEntitlementChanged) && s.callCount == 1 {
		return Reservation{}, s.err
	}
	if errors.Is(s.err, ErrEntitlementChanged) {
		return s.result, nil
	}
	return s.result, s.err
}

func (s *store) Release(_ context.Context, _ ids.AccountID, key string, _ time.Time) (Reservation, error) {
	s.releaseKey = key
	return s.result, s.err
}

type generator struct{}

func (generator) New() string { return "40000000-0000-4000-8000-000000000004" }

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

func TestReserveUsesImmutableLimitPolicyAndRetriesEntitlementRace(t *testing.T) {
	now := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	repository := &store{err: ErrEntitlementChanged, result: Reservation{ID: "reservation", State: ReservationActive}}
	service := serviceFor(t, repository, catalog.ModeEnabled, true, now)
	result, err := service.Reserve(context.Background(), ReserveCommand{Actor: access.Actor{UserID: testUserID}, AccountID: testAccountID, PackageCode: catalog.PackageAgents, LimitCode: "concurrent_runs", Amount: 1, RequestID: testRequestID})
	if err != nil || result.ID != "reservation" || repository.callCount != 2 {
		t.Fatalf("reserve result=%+v calls=%d err=%v", result, repository.callCount, err)
	}
	if repository.command.Maximum != 2 || repository.command.ExpectedEntitlementVersion != 7 || repository.command.ExpiresAt == nil || !repository.command.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("persisted command = %+v", repository.command)
	}
}

func TestReserveReturnsSpecificPackageAndLimitDenials(t *testing.T) {
	now := time.Now().UTC()
	readOnly := serviceFor(t, &store{}, catalog.ModeReadOnly, true, now)
	_, err := readOnly.Reserve(context.Background(), ReserveCommand{Actor: access.Actor{UserID: testUserID}, AccountID: testAccountID, PackageCode: catalog.PackageAgents, LimitCode: "concurrent_runs", Amount: 1, RequestID: testRequestID})
	if !access.IsDenied(err, access.DenialPackageReadOnly) {
		t.Fatalf("read-only error = %v", err)
	}
	undefined := serviceFor(t, &store{}, catalog.ModeEnabled, false, now)
	_, err = undefined.Reserve(context.Background(), ReserveCommand{Actor: access.Actor{UserID: testUserID}, AccountID: testAccountID, PackageCode: catalog.PackageAgents, LimitCode: "concurrent_runs", Amount: 1, RequestID: testRequestID})
	if !access.IsDenied(err, access.DenialLimitNotDefined) {
		t.Fatalf("undefined limit error = %v", err)
	}
}

func TestReserveTranslatesCapacityDenial(t *testing.T) {
	repository := &store{err: &CapacityExceededError{Current: 2, Maximum: 2}}
	service := serviceFor(t, repository, catalog.ModeEnabled, true, time.Now().UTC())
	_, err := service.Reserve(context.Background(), ReserveCommand{Actor: access.Actor{UserID: testUserID}, AccountID: testAccountID, PackageCode: catalog.PackageAgents, LimitCode: "concurrent_runs", Amount: 1, RequestID: testRequestID})
	var denied *access.DeniedError
	if !errors.As(err, &denied) || denied.Code != access.DenialLimitExceeded || denied.Current != 2 || denied.Maximum != 2 {
		t.Fatalf("capacity error = %#v", err)
	}
}

func TestReserveNeverResurrectsAClosedRequest(t *testing.T) {
	repository := &store{result: Reservation{ID: "reservation", State: ReservationReleased}}
	service := serviceFor(t, repository, catalog.ModeEnabled, true, time.Now().UTC())
	_, err := service.Reserve(context.Background(), ReserveCommand{Actor: access.Actor{UserID: testUserID}, AccountID: testAccountID, PackageCode: catalog.PackageAgents, LimitCode: "concurrent_runs", Amount: 1, RequestID: testRequestID})
	if !errors.Is(err, ErrReservationClosed) {
		t.Fatalf("closed reservation error = %v", err)
	}
}

func TestReleaseRemainsAvailableAfterReadOnlyDowngrade(t *testing.T) {
	repository := &store{result: Reservation{ID: "reservation", State: ReservationReleased}}
	service := serviceFor(t, repository, catalog.ModeReadOnly, true, time.Now().UTC())
	result, err := service.Release(context.Background(), ReleaseCommand{Actor: access.Actor{UserID: testUserID}, AccountID: testAccountID, RequestID: testRequestID})
	if err != nil || result.State != ReservationReleased || repository.releaseKey != testRequestID {
		t.Fatalf("release after downgrade = %+v key=%s err=%v", result, repository.releaseKey, err)
	}
}

func serviceFor(t *testing.T, repository Store, mode catalog.PackageMode, includeLimit bool, now time.Time) *Service {
	t.Helper()
	packageAccess := entitlements.PackageAccess{Code: catalog.PackageAgents, Version: 1, Mode: mode}
	if includeLimit {
		packageAccess.Limits = map[catalog.LimitCode]int64{"concurrent_runs": 2}
		packageAccess.LimitPolicies = map[catalog.LimitCode]entitlements.LimitPolicy{"concurrent_runs": {Kind: catalog.LimitKindCapacity, Combine: catalog.LimitMaximum, ReservationTTLSeconds: 3600}}
	}
	authorizer, err := access.NewAuthorizer(stateSource{state: access.State{
		Account:      accounts.Account{ID: testAccountID, State: accounts.AccountActive, EntitlementVersion: 7},
		Membership:   accounts.Membership{AccountID: testAccountID, UserID: testUserID, State: accounts.MembershipActive},
		Entitlements: entitlements.Snapshot{AccountID: testAccountID, Version: 7, Packages: []entitlements.PackageAccess{packageAccess}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(authorizer, repository, generator{}, clock{now})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
