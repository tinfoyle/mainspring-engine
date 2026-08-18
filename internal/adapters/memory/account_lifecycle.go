package memory

import (
	"context"
	"sort"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountlifecycle"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type closureRecord struct {
	status        accountlifecycle.Status
	requestedBy   ids.UserID
	attempt       int
	nextAttemptAt time.Time
	leaseExpires  time.Time
}

func (s *Store) Request(_ context.Context, mutation accountlifecycle.RequestMutation) (accountlifecycle.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[mutation.AccountID]
	if !ok {
		return accountlifecycle.Status{}, accountlifecycle.ErrNotFound
	}
	if account.State != accounts.AccountActive {
		return accountlifecycle.Status{}, accountlifecycle.ErrStateConflict
	}
	if account.Version != mutation.ExpectedAccountVersion {
		return accountlifecycle.Status{}, accountlifecycle.ErrVersionConflict
	}
	if !s.activeOwner(mutation.AccountID, mutation.ActorUserID) {
		return accountlifecycle.Status{}, accountlifecycle.ErrOwnershipRequired
	}
	for _, record := range s.closures {
		if record.status.AccountID == mutation.AccountID && currentClosure(record.status.State) {
			return accountlifecycle.Status{}, accountlifecycle.ErrVersionConflict
		}
	}
	account.State, account.Version = accounts.AccountClosing, account.Version+1
	s.accounts[account.ID] = account
	status := accountlifecycle.Status{RequestID: mutation.RequestID, AccountID: account.ID, AccountName: account.DisplayName, AccountState: account.State, AccountVersion: account.Version, State: accountlifecycle.StateCoolingOff, Reason: mutation.Reason, RequestedAt: mutation.At, ExecuteAfter: mutation.ExecuteAfter}
	s.closures[mutation.RequestID] = closureRecord{status: status, requestedBy: mutation.ActorUserID, nextAttemptAt: mutation.ExecuteAfter}
	return status, nil
}

func (s *Store) Cancel(_ context.Context, mutation accountlifecycle.CancelMutation) (accountlifecycle.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[mutation.AccountID]
	if !ok {
		return accountlifecycle.Status{}, accountlifecycle.ErrNotFound
	}
	if account.State != accounts.AccountClosing {
		return accountlifecycle.Status{}, accountlifecycle.ErrStateConflict
	}
	if account.Version != mutation.ExpectedAccountVersion {
		return accountlifecycle.Status{}, accountlifecycle.ErrVersionConflict
	}
	if !s.activeOwner(mutation.AccountID, mutation.ActorUserID) {
		return accountlifecycle.Status{}, accountlifecycle.ErrOwnershipRequired
	}
	id, record, ok := s.currentClosure(mutation.AccountID)
	if !ok {
		return accountlifecycle.Status{}, accountlifecycle.ErrNotFound
	}
	account.State, account.Version = accounts.AccountActive, account.Version+1
	s.accounts[account.ID] = account
	canceledAt := mutation.At.UTC()
	record.status.AccountState, record.status.AccountVersion = account.State, account.Version
	record.status.State, record.status.BlockerCode, record.status.CanceledAt = accountlifecycle.StateCanceled, "", &canceledAt
	record.leaseExpires = time.Time{}
	s.closures[id] = record
	return record.status, nil
}

func (s *Store) ListOwned(_ context.Context, userID ids.UserID) ([]accountlifecycle.Status, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]accountlifecycle.Status, 0)
	for _, record := range s.closures {
		if s.activeOwner(record.status.AccountID, userID) {
			result = append(result, record.status)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].RequestedAt.Equal(result[j].RequestedAt) {
			return result[i].RequestID > result[j].RequestID
		}
		return result[i].RequestedAt.After(result[j].RequestedAt)
	})
	return result, nil
}

func (s *Store) Claim(_ context.Context, now time.Time, lease time.Duration) (accountlifecycle.Work, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var selectedID string
	var selected closureRecord
	for id, record := range s.closures {
		eligible := (record.status.State == accountlifecycle.StateCoolingOff || record.status.State == accountlifecycle.StateBlocked) && !record.nextAttemptAt.After(now)
		eligible = eligible || record.status.State == accountlifecycle.StateProcessing && !record.leaseExpires.After(now)
		if eligible && (selectedID == "" || record.nextAttemptAt.Before(selected.nextAttemptAt) || record.nextAttemptAt.Equal(selected.nextAttemptAt) && id < selectedID) {
			selectedID, selected = id, record
		}
	}
	if selectedID == "" {
		return accountlifecycle.Work{}, false, nil
	}
	selected.status.State, selected.status.BlockerCode = accountlifecycle.StateProcessing, ""
	selected.attempt++
	selected.leaseExpires = now.Add(lease)
	s.closures[selectedID] = selected
	return accountlifecycle.Work{RequestID: selectedID, AccountID: selected.status.AccountID, Attempt: selected.attempt, RequestedBy: selected.requestedBy}, true, nil
}

func (s *Store) Evaluate(_ context.Context, work accountlifecycle.Work, _ string, now time.Time, retention, _ time.Duration) (accountlifecycle.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.closures[work.RequestID]
	if !ok {
		return accountlifecycle.Status{}, accountlifecycle.ErrNotFound
	}
	account := s.accounts[work.AccountID]
	if record.status.State != accountlifecycle.StateProcessing || record.attempt != work.Attempt || account.State != accounts.AccountClosing {
		return accountlifecycle.Status{}, accountlifecycle.ErrStateConflict
	}
	closedAt, deleteAfter := now.UTC(), now.UTC().Add(retention)
	account.State, account.Version = accounts.AccountClosed, account.Version+1
	s.accounts[account.ID] = account
	record.status.AccountState, record.status.AccountVersion = account.State, account.Version
	record.status.State, record.status.ClosedAt, record.status.DeleteAfter = accountlifecycle.StateClosed, &closedAt, &deleteAfter
	record.leaseExpires = time.Time{}
	s.closures[work.RequestID] = record
	return record.status, nil
}

func (s *Store) activeOwner(accountID ids.AccountID, userID ids.UserID) bool {
	for _, membership := range s.memberships {
		if membership.AccountID == accountID && membership.UserID == userID && membership.Role == accounts.RoleOwner && membership.State == accounts.MembershipActive {
			return true
		}
	}
	return false
}

func (s *Store) currentClosure(accountID ids.AccountID) (string, closureRecord, bool) {
	for id, record := range s.closures {
		if record.status.AccountID == accountID && currentClosure(record.status.State) {
			return id, record, true
		}
	}
	return "", closureRecord{}, false
}

func currentClosure(state accountlifecycle.State) bool {
	return state == accountlifecycle.StateCoolingOff || state == accountlifecycle.StateProcessing || state == accountlifecycle.StateBlocked
}

var _ accountlifecycle.Repository = (*Store)(nil)
