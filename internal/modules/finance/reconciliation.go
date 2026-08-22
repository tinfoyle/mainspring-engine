package finance

import (
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type ReconciliationState string

const (
	ReconciliationProposed    ReconciliationState = "proposed"
	ReconciliationDiscrepancy ReconciliationState = "discrepancy"
	ReconciliationConfirmed   ReconciliationState = "confirmed"
)

type Reconciliation struct {
	ID               ids.FinanceReconciliationID `json:"id"`
	AccountID        ids.AccountID               `json:"account_id"`
	LedgerID         ids.FinanceLedgerID         `json:"ledger_id"`
	PostingAccountID ids.FinanceAccountID        `json:"posting_account_id"`
	AsOf             time.Time                   `json:"as_of"`
	StatementBalance Money                       `json:"statement_balance"`
	LedgerBalance    Money                       `json:"ledger_balance"`
	DifferenceMinor  int64                       `json:"difference_minor"`
	Evidence         []ids.KnowledgeEvidenceID   `json:"evidence"`
	State            ReconciliationState         `json:"state"`
	Version          uint64                      `json:"version"`
	CreatedBy        Actor                       `json:"created_by"`
	ConfirmedBy      *Actor                      `json:"confirmed_by,omitempty"`
	CreatedAt        time.Time                   `json:"created_at"`
	UpdatedAt        time.Time                   `json:"updated_at"`
	ConfirmedAt      *time.Time                  `json:"confirmed_at,omitempty"`
}

type ReconciliationDraft struct {
	ID               ids.FinanceReconciliationID
	AccountID        ids.AccountID
	LedgerID         ids.FinanceLedgerID
	PostingAccountID ids.FinanceAccountID
	AsOf             time.Time
	StatementBalance Money
	LedgerBalance    Money
	Evidence         []ids.KnowledgeEvidenceID
	CreatedBy        Actor
	CreatedAt        time.Time
}

func NewReconciliation(draft ReconciliationDraft, role accounts.MembershipRole) (Reconciliation, error) {
	if draft.CreatedBy.Kind != ActorUser || !draft.CreatedBy.valid() || !canParticipate(role) {
		return Reconciliation{}, ErrRole
	}
	asOf, err := normalizeDate(draft.AsOf)
	if err != nil || !draft.StatementBalance.valid() || !draft.LedgerBalance.valid() || draft.StatementBalance.Currency != draft.LedgerBalance.Currency {
		return Reconciliation{}, ErrInvalid
	}
	evidence, err := normalizeEvidence(draft.Evidence)
	if err != nil || len(evidence) == 0 {
		return Reconciliation{}, ErrEvidence
	}
	difference, err := subtract(draft.StatementBalance.Minor, draft.LedgerBalance.Minor)
	if err != nil {
		return Reconciliation{}, err
	}
	state := ReconciliationProposed
	if difference != 0 {
		state = ReconciliationDiscrepancy
	}
	value := Reconciliation{ID: draft.ID, AccountID: draft.AccountID, LedgerID: draft.LedgerID, PostingAccountID: draft.PostingAccountID,
		AsOf: asOf, StatementBalance: draft.StatementBalance, LedgerBalance: draft.LedgerBalance, DifferenceMinor: difference, Evidence: evidence,
		State: state, Version: 1, CreatedBy: draft.CreatedBy, CreatedAt: draft.CreatedAt.UTC(), UpdatedAt: draft.CreatedAt.UTC()}
	return RestoreReconciliation(value)
}

func RestoreReconciliation(value Reconciliation) (Reconciliation, error) {
	asOf, dateErr := normalizeDate(value.AsOf)
	evidence, evidenceErr := normalizeEvidence(value.Evidence)
	difference, differenceErr := subtract(value.StatementBalance.Minor, value.LedgerBalance.Minor)
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.LedgerID)) != nil || ids.Validate(string(value.PostingAccountID)) != nil ||
		dateErr != nil || !value.StatementBalance.valid() || !value.LedgerBalance.valid() || value.StatementBalance.Currency != value.LedgerBalance.Currency || evidenceErr != nil || len(evidence) == 0 ||
		differenceErr != nil || difference != value.DifferenceMinor || value.Version == 0 || !value.CreatedBy.valid() || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return Reconciliation{}, ErrInvalid
	}
	value.AsOf, value.Evidence, value.CreatedAt, value.UpdatedAt = asOf, evidence, value.CreatedAt.UTC(), value.UpdatedAt.UTC()
	switch value.State {
	case ReconciliationProposed:
		if difference != 0 || value.ConfirmedBy != nil || value.ConfirmedAt != nil {
			return Reconciliation{}, ErrInvalid
		}
	case ReconciliationDiscrepancy:
		if difference == 0 || value.ConfirmedBy != nil || value.ConfirmedAt != nil {
			return Reconciliation{}, ErrInvalid
		}
	case ReconciliationConfirmed:
		if difference != 0 || value.ConfirmedBy == nil || value.ConfirmedBy.Kind != ActorUser || !value.ConfirmedBy.valid() || value.ConfirmedAt == nil || value.ConfirmedAt.Before(value.CreatedAt) {
			return Reconciliation{}, ErrInvalid
		}
	default:
		return Reconciliation{}, ErrInvalid
	}
	return value, nil
}

func (value Reconciliation) Confirm(expectedVersion uint64, actor Actor, role accounts.MembershipRole, at time.Time) (Reconciliation, error) {
	if expectedVersion != value.Version {
		return Reconciliation{}, ErrConflict
	}
	if value.State == ReconciliationDiscrepancy {
		return Reconciliation{}, ErrMismatch
	}
	if value.State != ReconciliationProposed {
		return Reconciliation{}, ErrState
	}
	if actor.Kind != ActorUser || !actor.valid() || !canManage(role) {
		return Reconciliation{}, ErrRole
	}
	if at.IsZero() || at.UTC().Before(value.UpdatedAt) {
		return Reconciliation{}, ErrInvalid
	}
	at = at.UTC()
	value.State, value.ConfirmedBy, value.ConfirmedAt, value.Version, value.UpdatedAt = ReconciliationConfirmed, &actor, &at, value.Version+1, at
	return RestoreReconciliation(value)
}
