// Package finance owns Account-scoped internal operational ledgers. It has no
// database, provider, billing, payment-execution, or transport dependency.
package finance

import (
	"errors"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumLedgerName       = 160
	MaximumCode             = 40
	MaximumDescriptionBytes = 4000
	MaximumMemoBytes        = 1000
	MaximumReferenceBytes   = 500
	MaximumEntryLines       = 100
	MaximumEvidence         = 32
)

var (
	ErrInvalid      = errors.New("finance aggregate is invalid")
	ErrConflict     = errors.New("finance aggregate version conflict")
	ErrState        = errors.New("finance state change is not allowed")
	ErrRole         = errors.New("role cannot perform the finance command")
	ErrUnbalanced   = errors.New("journal entry is not balanced")
	ErrOverflow     = errors.New("finance amount overflow")
	ErrPeriodClosed = errors.New("journal date is in a closed period")
	ErrEvidence     = errors.New("accepted finance evidence is required")
	ErrMismatch     = errors.New("reconciliation has a non-zero difference")
)

type Currency string

func NewCurrency(value string) (Currency, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 3 {
		return "", ErrInvalid
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return "", ErrInvalid
		}
	}
	return Currency(value), nil
}

type Money struct {
	Currency Currency `json:"currency"`
	Minor    int64    `json:"minor"`
}

func NewMoney(currency string, minor int64) (Money, error) {
	parsed, err := NewCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	return Money{Currency: parsed, Minor: minor}, nil
}

func (money Money) valid() bool {
	currency, err := NewCurrency(string(money.Currency))
	return err == nil && currency == money.Currency
}

func add(left, right int64) (int64, error) {
	if right > 0 && left > math.MaxInt64-right || right < 0 && left < math.MinInt64-right {
		return 0, ErrOverflow
	}
	return left + right, nil
}

func subtract(left, right int64) (int64, error) {
	if right == math.MinInt64 {
		if left >= 0 {
			return 0, ErrOverflow
		}
		return left - right, nil
	}
	return add(left, -right)
}

type ActorKind string

const (
	ActorUser     ActorKind = "user"
	ActorWorkload ActorKind = "workload"
)

type Actor struct {
	Kind ActorKind `json:"kind"`
	ID   string    `json:"id"`
}

func (actor Actor) valid() bool {
	if actor.ID == "" || actor.ID != strings.TrimSpace(actor.ID) || len(actor.ID) > 200 {
		return false
	}
	if actor.Kind == ActorUser {
		return ids.Validate(actor.ID) == nil
	}
	return actor.Kind == ActorWorkload
}

func canParticipate(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator || role == accounts.RoleMember
}

func canManage(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator
}

func validText(value string, maximum int, required bool) bool {
	return strings.TrimSpace(value) == value && (!required || value != "") && utf8.ValidString(value) && len(value) <= maximum && !strings.ContainsRune(value, '\x00')
}

func normalizeDate(value time.Time) (time.Time, error) {
	if value.IsZero() {
		return time.Time{}, ErrInvalid
	}
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC), nil
}

func normalizeEvidence(values []ids.KnowledgeEvidenceID) ([]ids.KnowledgeEvidenceID, error) {
	values = append([]ids.KnowledgeEvidenceID(nil), values...)
	if len(values) > MaximumEvidence {
		return nil, ErrEvidence
	}
	for index, value := range values {
		if ids.Validate(string(value)) != nil || slices.Contains(values[:index], value) {
			return nil, ErrEvidence
		}
	}
	slices.Sort(values)
	return values, nil
}
