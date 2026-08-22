package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	financeapp "github.com/tinfoyle/spyglass-engine/internal/application/finance"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type FinanceRepository struct{ cell *database.CellPool }

func NewFinanceRepository(cell *database.CellPool) (*FinanceRepository, error) {
	if cell == nil {
		return nil, errors.New("Finance cell pool is required")
	}
	return &FinanceRepository{cell: cell}, nil
}

func (repository *FinanceRepository) CreateLedger(ctx context.Context, draft domain.LedgerDraft, role accounts.MembershipRole, mutation financeapp.Mutation) (domain.Ledger, bool, error) {
	value, err := domain.NewLedger(draft, role)
	if err != nil || !mutation.Valid() || mutation.Kind != "created" || mutation.Actor != value.CreatedBy || !mutation.At.UTC().Equal(value.CreatedAt) {
		return domain.Ledger{}, false, financeapp.ErrInvalid
	}
	created := false
	result := value
	err = repository.cell.WithAccountTx(ctx, value.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, loadErr := loadFinanceLedger(ctx, tx, value.AccountID, value.ID, true)
		if loadErr == nil {
			matched, err := financeEventMatches(ctx, tx, value.AccountID, mutation.EventID, "ledger", string(value.ID), "created", 0, 1)
			if err != nil {
				return err
			}
			comparison := value
			comparison.CreatedAt, comparison.UpdatedAt = existing.CreatedAt, existing.UpdatedAt
			if !matched || !reflect.DeepEqual(existing, comparison) {
				return financeapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(loadErr, financeapp.ErrNotFound) {
			return loadErr
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.finance_ledgers
			(account_id,id,name,code,description,currency,state,version,created_by_kind,created_by_id,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, value.AccountID, value.ID, value.Name, value.Code, value.Description,
			value.Currency, value.State, value.Version, value.CreatedBy.Kind, value.CreatedBy.ID, value.CreatedAt, value.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.finance_entry_number_counters(account_id,ledger_id,next_number) VALUES ($1,$2,1)`, value.AccountID, value.ID); err != nil {
			return err
		}
		if err := insertFinanceEvent(ctx, tx, value.AccountID, "ledger", string(value.ID), 0, mutation, map[string]any{"currency": value.Currency, "state": value.State}); err != nil {
			return err
		}
		created = true
		return nil
	})
	return result, created, classifyFinance(err)
}

func (repository *FinanceRepository) GetLedger(ctx context.Context, accountID ids.AccountID, ledgerID ids.FinanceLedgerID) (domain.Ledger, error) {
	var result domain.Ledger
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := loadFinanceLedger(ctx, tx, accountID, ledgerID, false)
		result = value
		return err
	})
	return result, classifyFinance(err)
}

func (repository *FinanceRepository) ReviseLedger(ctx context.Context, accountID ids.AccountID, ledgerID ids.FinanceLedgerID, command domain.LedgerRevision, mutation financeapp.Mutation) (domain.Ledger, error) {
	if !mutation.Valid() || mutation.Kind != "revised" || mutation.Actor != command.Actor || !mutation.At.UTC().Equal(command.At.UTC()) {
		return domain.Ledger{}, financeapp.ErrInvalid
	}
	var result domain.Ledger
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadFinanceLedger(ctx, tx, accountID, ledgerID, true)
		if err != nil {
			return err
		}
		if current.Version == command.ExpectedVersion+1 {
			matched, err := financeEventMatches(ctx, tx, accountID, mutation.EventID, "ledger", string(ledgerID), mutation.Kind, command.ExpectedVersion, current.Version)
			if err != nil || !matched || current.Name != strings.TrimSpace(command.Name) || current.Code != strings.ToUpper(strings.TrimSpace(command.Code)) ||
				current.Description != strings.TrimSpace(command.Description) {
				if err != nil {
					return err
				}
				return financeapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != command.ExpectedVersion {
			return financeapp.ErrConflict
		}
		revised, err := current.Revise(command)
		if err != nil {
			return err
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.finance_ledgers SET name=$3,code=$4,description=$5,version=$6,updated_at=$7
			WHERE account_id=$1 AND id=$2 AND version=$8`, accountID, ledgerID, revised.Name, revised.Code, revised.Description, revised.Version, revised.UpdatedAt, command.ExpectedVersion)
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return financeapp.ErrConflict
		}
		if err := insertFinanceEvent(ctx, tx, accountID, "ledger", string(ledgerID), command.ExpectedVersion, mutation,
			map[string]any{"code": revised.Code, "currency": revised.Currency, "state": revised.State}); err != nil {
			return err
		}
		result = revised
		return nil
	})
	return result, classifyFinance(err)
}

func (repository *FinanceRepository) CloseLedgerPeriod(ctx context.Context, accountID ids.AccountID, ledgerID ids.FinanceLedgerID, command domain.ClosePeriodCommand, mutation financeapp.Mutation) (domain.Ledger, error) {
	if !mutation.Valid() || mutation.Kind != "period_closed" || mutation.Actor != command.Actor || !mutation.At.UTC().Equal(command.At.UTC()) {
		return domain.Ledger{}, financeapp.ErrInvalid
	}
	var result domain.Ledger
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadFinanceLedger(ctx, tx, accountID, ledgerID, true)
		if err != nil {
			return err
		}
		if current.Version == command.ExpectedVersion+1 {
			matched, err := financeEventMatches(ctx, tx, accountID, mutation.EventID, "ledger", string(ledgerID), mutation.Kind, command.ExpectedVersion, current.Version)
			if err != nil || !matched || current.ClosedThrough == nil || !sameFinanceDate(*current.ClosedThrough, command.Through) || !sameFinanceEvidence(current.CloseEvidence, command.Evidence) {
				if err != nil {
					return err
				}
				return financeapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != command.ExpectedVersion {
			return financeapp.ErrConflict
		}
		closed, err := current.ClosePeriod(command)
		if err != nil {
			return err
		}
		for _, evidenceID := range closed.CloseEvidence {
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.finance_ledger_close_evidence
				(account_id,ledger_id,ledger_version,evidence_id,closed_through,created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
				accountID, ledgerID, closed.Version, evidenceID, closed.ClosedThrough, closed.UpdatedAt); err != nil {
				return err
			}
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.finance_ledgers SET closed_through=$3,version=$4,updated_at=$5
			WHERE account_id=$1 AND id=$2 AND version=$6`, accountID, ledgerID, closed.ClosedThrough, closed.Version, closed.UpdatedAt, command.ExpectedVersion)
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return financeapp.ErrConflict
		}
		if err := insertFinanceEvent(ctx, tx, accountID, "ledger", string(ledgerID), command.ExpectedVersion, mutation,
			map[string]any{"closed_through": closed.ClosedThrough, "evidence_count": len(closed.CloseEvidence), "state": closed.State}); err != nil {
			return err
		}
		result = closed
		return nil
	})
	return result, classifyFinance(err)
}

func (repository *FinanceRepository) ArchiveLedger(ctx context.Context, accountID ids.AccountID, ledgerID ids.FinanceLedgerID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation financeapp.Mutation) (domain.Ledger, error) {
	if !mutation.Valid() || mutation.Kind != "archived" || mutation.Actor != actor {
		return domain.Ledger{}, financeapp.ErrInvalid
	}
	var result domain.Ledger
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadFinanceLedger(ctx, tx, accountID, ledgerID, true)
		if err != nil {
			return err
		}
		if current.Version == expected+1 && current.State == domain.LedgerArchived {
			matched, err := financeEventMatches(ctx, tx, accountID, mutation.EventID, "ledger", string(ledgerID), mutation.Kind, expected, current.Version)
			if err != nil || !matched {
				if err != nil {
					return err
				}
				return financeapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != expected {
			return financeapp.ErrConflict
		}
		var blocked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM spyglass.finance_accounts WHERE account_id=$1 AND ledger_id=$2 AND state='active') OR
			EXISTS(SELECT 1 FROM spyglass.finance_entries WHERE account_id=$1 AND ledger_id=$2 AND state='draft')`, accountID, ledgerID).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return financeapp.ErrInvalid
		}
		archived, err := current.Archive(expected, actor, role, mutation.At)
		if err != nil {
			return err
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.finance_ledgers SET state='archived',version=$3,updated_at=$4
			WHERE account_id=$1 AND id=$2 AND version=$5`, accountID, ledgerID, archived.Version, archived.UpdatedAt, expected)
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return financeapp.ErrConflict
		}
		if err := insertFinanceEvent(ctx, tx, accountID, "ledger", string(ledgerID), expected, mutation,
			map[string]any{"currency": archived.Currency, "state": archived.State}); err != nil {
			return err
		}
		result = archived
		return nil
	})
	return result, classifyFinance(err)
}

func (repository *FinanceRepository) CreatePostingAccount(ctx context.Context, draft domain.PostingAccountDraft, role accounts.MembershipRole, mutation financeapp.Mutation) (domain.PostingAccount, bool, error) {
	value, err := domain.NewPostingAccount(draft, role)
	if err != nil || !mutation.Valid() || mutation.Kind != "created" || mutation.Actor != value.CreatedBy || !mutation.At.UTC().Equal(value.CreatedAt) {
		return domain.PostingAccount{}, false, financeapp.ErrInvalid
	}
	created := false
	result := value
	err = repository.cell.WithAccountTx(ctx, value.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, loadErr := loadFinancePostingAccount(ctx, tx, value.AccountID, value.ID, true)
		if loadErr == nil {
			matched, err := financeEventMatches(ctx, tx, value.AccountID, mutation.EventID, "account", string(value.ID), "created", 0, 1)
			if err != nil {
				return err
			}
			comparison := value
			comparison.CreatedAt, comparison.UpdatedAt = existing.CreatedAt, existing.UpdatedAt
			if !matched || !reflect.DeepEqual(existing, comparison) {
				return financeapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(loadErr, financeapp.ErrNotFound) {
			return loadErr
		}
		ledger, err := loadFinanceLedger(ctx, tx, value.AccountID, value.LedgerID, true)
		if err != nil || ledger.State != domain.LedgerActive {
			if err != nil {
				return err
			}
			return financeapp.ErrInvalid
		}
		if value.ParentAccountID != "" {
			parent, err := loadFinancePostingAccount(ctx, tx, value.AccountID, value.ParentAccountID, true)
			if err != nil || parent.LedgerID != value.LedgerID || parent.State != domain.LedgerActive {
				if err != nil {
					return err
				}
				return financeapp.ErrInvalid
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.finance_accounts
			(account_id,id,ledger_id,parent_account_id,code,name,description,account_type,normal_balance,allow_posting,state,version,created_by_kind,created_by_id,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, value.AccountID, value.ID, value.LedgerID,
			nullableFinanceAccountID(value.ParentAccountID), value.Code, value.Name, value.Description, value.Type, value.NormalBalance, value.AllowPosting,
			value.State, value.Version, value.CreatedBy.Kind, value.CreatedBy.ID, value.CreatedAt, value.UpdatedAt); err != nil {
			return err
		}
		if err := insertFinanceEvent(ctx, tx, value.AccountID, "account", string(value.ID), 0, mutation,
			map[string]any{"ledger_id": value.LedgerID, "account_type": value.Type, "allow_posting": value.AllowPosting, "state": value.State}); err != nil {
			return err
		}
		created = true
		return nil
	})
	return result, created, classifyFinance(err)
}

func (repository *FinanceRepository) RevisePostingAccount(ctx context.Context, accountID ids.AccountID, postingAccountID ids.FinanceAccountID, command domain.PostingAccountRevision, mutation financeapp.Mutation) (domain.PostingAccount, error) {
	if !mutation.Valid() || mutation.Kind != "revised" || mutation.Actor != command.Actor || !mutation.At.UTC().Equal(command.At.UTC()) {
		return domain.PostingAccount{}, financeapp.ErrInvalid
	}
	var result domain.PostingAccount
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadFinancePostingAccount(ctx, tx, accountID, postingAccountID, true)
		if err != nil {
			return err
		}
		if current.Version == command.ExpectedVersion+1 {
			matched, err := financeEventMatches(ctx, tx, accountID, mutation.EventID, "account", string(postingAccountID), mutation.Kind, command.ExpectedVersion, current.Version)
			if err != nil || !matched || current.ParentAccountID != command.ParentAccountID || current.Code != strings.ToUpper(strings.TrimSpace(command.Code)) ||
				current.Name != strings.TrimSpace(command.Name) || current.Description != strings.TrimSpace(command.Description) || current.AllowPosting != command.AllowPosting {
				if err != nil {
					return err
				}
				return financeapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != command.ExpectedVersion {
			return financeapp.ErrConflict
		}
		if command.ParentAccountID != "" {
			parent, err := loadFinancePostingAccount(ctx, tx, accountID, command.ParentAccountID, true)
			if err != nil || parent.LedgerID != current.LedgerID || parent.State != domain.LedgerActive {
				if err != nil {
					return err
				}
				return financeapp.ErrInvalid
			}
			var cyclic bool
			if err := tx.QueryRow(ctx, `WITH RECURSIVE ancestors AS (
				SELECT id,parent_account_id FROM spyglass.finance_accounts WHERE account_id=$1 AND ledger_id=$2 AND id=$3
				UNION ALL
				SELECT a.id,a.parent_account_id FROM spyglass.finance_accounts a JOIN ancestors p ON a.account_id=$1 AND a.ledger_id=$2 AND a.id=p.parent_account_id
			) SELECT EXISTS(SELECT 1 FROM ancestors WHERE id=$4)`, accountID, current.LedgerID, command.ParentAccountID, postingAccountID).Scan(&cyclic); err != nil {
				return err
			}
			if cyclic {
				return financeapp.ErrInvalid
			}
		}
		revised, err := current.Revise(command)
		if err != nil {
			return err
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.finance_accounts SET parent_account_id=$3,code=$4,name=$5,description=$6,allow_posting=$7,version=$8,updated_at=$9
			WHERE account_id=$1 AND id=$2 AND version=$10`, accountID, postingAccountID, nullableFinanceAccountID(revised.ParentAccountID), revised.Code,
			revised.Name, revised.Description, revised.AllowPosting, revised.Version, revised.UpdatedAt, command.ExpectedVersion)
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return financeapp.ErrConflict
		}
		if err := insertFinanceEvent(ctx, tx, accountID, "account", string(postingAccountID), command.ExpectedVersion, mutation,
			map[string]any{"ledger_id": revised.LedgerID, "code": revised.Code, "allow_posting": revised.AllowPosting, "state": revised.State}); err != nil {
			return err
		}
		result = revised
		return nil
	})
	return result, classifyFinance(err)
}

func (repository *FinanceRepository) ArchivePostingAccount(ctx context.Context, accountID ids.AccountID, postingAccountID ids.FinanceAccountID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation financeapp.Mutation) (domain.PostingAccount, error) {
	if !mutation.Valid() || mutation.Kind != "archived" || mutation.Actor != actor {
		return domain.PostingAccount{}, financeapp.ErrInvalid
	}
	var result domain.PostingAccount
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadFinancePostingAccount(ctx, tx, accountID, postingAccountID, true)
		if err != nil {
			return err
		}
		if current.Version == expected+1 && current.State == domain.LedgerArchived {
			matched, err := financeEventMatches(ctx, tx, accountID, mutation.EventID, "account", string(postingAccountID), mutation.Kind, expected, current.Version)
			if err != nil || !matched {
				if err != nil {
					return err
				}
				return financeapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != expected {
			return financeapp.ErrConflict
		}
		var blocked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM spyglass.finance_accounts
			WHERE account_id=$1 AND ledger_id=$2 AND parent_account_id=$3 AND state='active') OR EXISTS(
				SELECT 1 FROM spyglass.finance_entry_lines l JOIN spyglass.finance_entries e ON e.account_id=l.account_id AND e.id=l.entry_id
				WHERE l.account_id=$1 AND l.ledger_id=$2 AND l.posting_account_id=$3 AND e.state='draft')`, accountID, current.LedgerID, postingAccountID).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return financeapp.ErrInvalid
		}
		archived, err := current.Archive(expected, actor, role, mutation.At)
		if err != nil {
			return err
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.finance_accounts SET allow_posting=false,state='archived',version=$3,updated_at=$4
			WHERE account_id=$1 AND id=$2 AND version=$5`, accountID, postingAccountID, archived.Version, archived.UpdatedAt, expected)
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return financeapp.ErrConflict
		}
		if err := insertFinanceEvent(ctx, tx, accountID, "account", string(postingAccountID), expected, mutation,
			map[string]any{"ledger_id": archived.LedgerID, "code": archived.Code, "allow_posting": archived.AllowPosting, "state": archived.State}); err != nil {
			return err
		}
		result = archived
		return nil
	})
	return result, classifyFinance(err)
}

func (repository *FinanceRepository) CreateEntry(ctx context.Context, draft domain.EntryDraft, role accounts.MembershipRole, mutation financeapp.Mutation) (domain.JournalEntry, bool, error) {
	if draft.Number != 0 || !mutation.Valid() || mutation.Kind != "created" || mutation.Actor != draft.CreatedBy || !mutation.At.UTC().Equal(draft.CreatedAt.UTC()) {
		return domain.JournalEntry{}, false, financeapp.ErrInvalid
	}
	var result domain.JournalEntry
	created := false
	err := repository.cell.WithAccountTx(ctx, draft.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, loadErr := loadFinanceEntry(ctx, tx, draft.AccountID, draft.ID, true)
		if loadErr == nil {
			matched, err := financeEventMatches(ctx, tx, draft.AccountID, mutation.EventID, "entry", string(draft.ID), "created", 0, 1)
			if err != nil {
				return err
			}
			comparison := draft
			comparison.Number = existing.Number
			comparison.CreatedAt = existing.CreatedAt
			expected, err := domain.NewJournalEntry(comparison, role)
			if err != nil || !matched || !reflect.DeepEqual(existing, expected) {
				return financeapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(loadErr, financeapp.ErrNotFound) {
			return loadErr
		}
		ledger, err := loadFinanceLedger(ctx, tx, draft.AccountID, draft.LedgerID, true)
		if err != nil || ledger.State != domain.LedgerActive || string(ledger.Currency) != draft.Currency {
			if err != nil {
				return err
			}
			return financeapp.ErrInvalid
		}
		if err := tx.QueryRow(ctx, `UPDATE spyglass.finance_entry_number_counters SET next_number=next_number+1
			WHERE account_id=$1 AND ledger_id=$2 RETURNING next_number-1`, draft.AccountID, draft.LedgerID).Scan(&draft.Number); err != nil {
			return err
		}
		value, err := domain.NewJournalEntry(draft, role)
		if err != nil {
			return financeapp.ErrInvalid
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.finance_entries
			(account_id,id,ledger_id,entry_number,entry_date,description,reference,currency,total_minor,source,work_item_id,run_id,invocation_id,state,version,created_by_kind,created_by_id,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'draft',1,$14,$15,$16,$16)`, value.AccountID, value.ID,
			value.LedgerID, value.Number, value.EntryDate, value.Description, value.Reference, value.Currency, value.TotalMinor, value.Provenance.Source,
			financeNullableWorkItemID(value.Provenance.WorkItemID), financeNullableRunID(value.Provenance.RunID), financeNullableInvocationID(value.Provenance.InvocationID),
			value.CreatedBy.Kind, value.CreatedBy.ID, value.CreatedAt); err != nil {
			return err
		}
		if err := insertFinanceEntryChildren(ctx, tx, value); err != nil {
			return err
		}
		if err := insertFinanceEvent(ctx, tx, value.AccountID, "entry", string(value.ID), 0, mutation,
			map[string]any{"ledger_id": value.LedgerID, "entry_number": value.Number, "currency": value.Currency, "total_minor": value.TotalMinor, "source": value.Provenance.Source, "state": value.State}); err != nil {
			return err
		}
		result, created = value, true
		return nil
	})
	return result, created, classifyFinance(err)
}

func (repository *FinanceRepository) GetEntry(ctx context.Context, accountID ids.AccountID, entryID ids.FinanceEntryID) (domain.JournalEntry, error) {
	var result domain.JournalEntry
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := loadFinanceEntry(ctx, tx, accountID, entryID, false)
		result = value
		return err
	})
	return result, classifyFinance(err)
}

func (repository *FinanceRepository) PostEntry(ctx context.Context, accountID ids.AccountID, entryID ids.FinanceEntryID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation financeapp.Mutation) (domain.JournalEntry, error) {
	if !mutation.Valid() || mutation.Kind != "posted" || mutation.Actor != actor || !mutation.At.UTC().Equal(mutation.At) {
		return domain.JournalEntry{}, financeapp.ErrInvalid
	}
	var result domain.JournalEntry
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadFinanceEntry(ctx, tx, accountID, entryID, true)
		if err != nil {
			return err
		}
		if current.Version == expected+1 && current.State == domain.EntryStatePosted {
			matched, err := financeEventMatches(ctx, tx, accountID, mutation.EventID, "entry", string(entryID), "posted", expected, expected+1)
			if err != nil || !matched {
				if err != nil {
					return err
				}
				return financeapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != expected {
			return financeapp.ErrConflict
		}
		ledger, err := loadFinanceLedger(ctx, tx, accountID, current.LedgerID, true)
		if err != nil {
			return err
		}
		posted, err := current.Post(domain.PostCommand{Actor: actor, Role: role, ExpectedVersion: expected, At: mutation.At}, ledger.ClosedThrough)
		if err != nil {
			return err
		}
		command, err := tx.Exec(ctx, `UPDATE spyglass.finance_entries SET state='posted',version=$3,posted_by_user_id=$4,posted_at=$5,updated_at=$5
			WHERE account_id=$1 AND id=$2 AND version=$6`, accountID, entryID, posted.Version, actor.ID, mutation.At, expected)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return financeapp.ErrConflict
		}
		if err := insertFinanceEvent(ctx, tx, accountID, "entry", string(entryID), expected, mutation,
			map[string]any{"ledger_id": posted.LedgerID, "entry_number": posted.Number, "currency": posted.Currency, "total_minor": posted.TotalMinor, "state": posted.State}); err != nil {
			return err
		}
		result = posted
		return nil
	})
	return result, classifyFinance(err)
}

func (repository *FinanceRepository) ReverseEntry(ctx context.Context, accountID ids.AccountID, entryID ids.FinanceEntryID, expected uint64, command domain.ReverseCommand, mutation financeapp.Mutation) (domain.JournalEntry, domain.JournalEntry, error) {
	if !mutation.Valid() || mutation.Kind != "reversed" || mutation.Actor != command.Actor || command.ExpectedVersion != expected || !mutation.At.UTC().Equal(command.At.UTC()) {
		return domain.JournalEntry{}, domain.JournalEntry{}, financeapp.ErrInvalid
	}
	var original, reversal domain.JournalEntry
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadFinanceEntry(ctx, tx, accountID, entryID, true)
		if err != nil {
			return err
		}
		if current.Version == expected+1 && current.State == domain.EntryStateReversed {
			matched, err := financeEventMatches(ctx, tx, accountID, mutation.EventID, "entry", string(entryID), "reversed", expected, expected+1)
			if err != nil || !matched || current.ReversedByID != command.ReversalID {
				if err != nil {
					return err
				}
				return financeapp.ErrConflict
			}
			replayed, err := loadFinanceEntry(ctx, tx, accountID, current.ReversedByID, false)
			if err != nil {
				return err
			}
			original, reversal = current, replayed
			return nil
		}
		if current.Version != expected {
			return financeapp.ErrConflict
		}
		ledger, err := loadFinanceLedger(ctx, tx, accountID, current.LedgerID, true)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `UPDATE spyglass.finance_entry_number_counters SET next_number=next_number+1
			WHERE account_id=$1 AND ledger_id=$2 RETURNING next_number-1`, accountID, current.LedgerID).Scan(&command.ReversalNumber); err != nil {
			return err
		}
		original, reversal, err = current.Reverse(command, ledger.ClosedThrough)
		if err != nil {
			return err
		}
		if err := insertFinanceReversal(ctx, tx, reversal); err != nil {
			return err
		}
		createdEventID, err := ids.Derive(mutation.EventID, "finance-reversal-created")
		if err != nil {
			return financeapp.ErrInvalid
		}
		createdMutation := mutation
		createdMutation.EventID, createdMutation.Kind = createdEventID, "created"
		if err := insertFinanceEvent(ctx, tx, accountID, "entry", string(reversal.ID), 0, createdMutation,
			map[string]any{"ledger_id": reversal.LedgerID, "entry_number": reversal.Number, "reversal_of_id": entryID, "currency": reversal.Currency, "total_minor": reversal.TotalMinor, "source": reversal.Provenance.Source, "state": domain.EntryStateDraft}); err != nil {
			return err
		}
		postedEventID, err := ids.Derive(mutation.EventID, "finance-reversal-posted")
		if err != nil {
			return financeapp.ErrInvalid
		}
		postedMutation := mutation
		postedMutation.EventID, postedMutation.Kind = postedEventID, "posted"
		if err := insertFinanceEvent(ctx, tx, accountID, "entry", string(reversal.ID), 1, postedMutation,
			map[string]any{"ledger_id": reversal.LedgerID, "entry_number": reversal.Number, "reversal_of_id": entryID, "currency": reversal.Currency, "total_minor": reversal.TotalMinor, "state": reversal.State}); err != nil {
			return err
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.finance_entries SET state='reversed',version=$3,reversed_by_id=$4,reversed_by_user_id=$5,reversed_at=$6,updated_at=$6
			WHERE account_id=$1 AND id=$2 AND version=$7`, accountID, entryID, original.Version, reversal.ID, command.Actor.ID, command.At, expected)
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return financeapp.ErrConflict
		}
		if err := insertFinanceEvent(ctx, tx, accountID, "entry", string(entryID), expected, mutation,
			map[string]any{"ledger_id": original.LedgerID, "entry_number": original.Number, "reversal_id": reversal.ID, "reversal_number": reversal.Number, "currency": original.Currency, "total_minor": original.TotalMinor, "state": original.State}); err != nil {
			return err
		}
		return nil
	})
	return original, reversal, classifyFinance(err)
}

func (repository *FinanceRepository) CreateReconciliation(ctx context.Context, draft domain.ReconciliationDraft, role accounts.MembershipRole, mutation financeapp.Mutation) (domain.Reconciliation, bool, error) {
	if !mutation.Valid() || mutation.Kind != "reconciliation_proposed" || mutation.Actor != draft.CreatedBy || !mutation.At.UTC().Equal(draft.CreatedAt.UTC()) || draft.LedgerBalance.Minor != 0 || draft.LedgerBalance.Currency != "" {
		return domain.Reconciliation{}, false, financeapp.ErrInvalid
	}
	created := false
	var result domain.Reconciliation
	err := repository.cell.WithAccountTx(ctx, draft.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, loadErr := loadFinanceReconciliation(ctx, tx, draft.AccountID, draft.ID, true)
		if loadErr == nil {
			existingKind := "reconciliation_proposed"
			if existing.State == domain.ReconciliationDiscrepancy {
				existingKind = "reconciliation_discrepancy"
			}
			matched, err := financeEventMatches(ctx, tx, draft.AccountID, mutation.EventID, "reconciliation", string(draft.ID), existingKind, 0, 1)
			if err != nil {
				return err
			}
			if !matched || existing.LedgerID != draft.LedgerID || existing.PostingAccountID != draft.PostingAccountID || !sameFinanceDate(existing.AsOf, draft.AsOf) ||
				existing.StatementBalance != draft.StatementBalance || !sameFinanceEvidence(existing.Evidence, draft.Evidence) || existing.CreatedBy != draft.CreatedBy {
				return financeapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(loadErr, financeapp.ErrNotFound) {
			return loadErr
		}
		ledger, err := loadFinanceLedger(ctx, tx, draft.AccountID, draft.LedgerID, true)
		if err != nil || ledger.Currency != draft.StatementBalance.Currency {
			if err != nil {
				return err
			}
			return financeapp.ErrInvalid
		}
		postingAccount, err := loadFinancePostingAccount(ctx, tx, draft.AccountID, draft.PostingAccountID, true)
		if err != nil || postingAccount.LedgerID != draft.LedgerID {
			if err != nil {
				return err
			}
			return financeapp.ErrInvalid
		}
		var balanceFits bool
		var balanceMinor *int64
		if err := tx.QueryRow(ctx, `WITH calculated AS (
			SELECT CASE WHEN $4='debit' THEN COALESCE(sum(l.debit_minor::numeric-l.credit_minor::numeric),0)
			            ELSE COALESCE(sum(l.credit_minor::numeric-l.debit_minor::numeric),0) END AS amount
			FROM spyglass.finance_entries e JOIN spyglass.finance_entry_lines l ON l.account_id=e.account_id AND l.entry_id=e.id
			WHERE e.account_id=$1 AND e.ledger_id=$2 AND l.posting_account_id=$3 AND e.entry_date<=$5 AND e.state IN ('posted','reversed'))
			SELECT amount BETWEEN -9223372036854775808::numeric AND 9223372036854775807::numeric,
			       CASE WHEN amount BETWEEN -9223372036854775808::numeric AND 9223372036854775807::numeric THEN amount::bigint END FROM calculated`,
			draft.AccountID, draft.LedgerID, draft.PostingAccountID, postingAccount.NormalBalance, draft.AsOf).Scan(&balanceFits, &balanceMinor); err != nil {
			return err
		}
		if !balanceFits || balanceMinor == nil {
			return domain.ErrOverflow
		}
		draft.LedgerBalance = domain.Money{Currency: ledger.Currency, Minor: *balanceMinor}
		value, err := domain.NewReconciliation(draft, role)
		if err != nil {
			return err
		}
		wantKind := "reconciliation_proposed"
		if value.State == domain.ReconciliationDiscrepancy {
			wantKind = "reconciliation_discrepancy"
		}
		mutation.Kind = wantKind
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.finance_reconciliations
			(account_id,id,ledger_id,posting_account_id,as_of,currency,statement_balance_minor,ledger_balance_minor,difference_minor,primary_evidence_id,state,version,created_by_user_id,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,1,$12,$13,$13)`, value.AccountID, value.ID, value.LedgerID,
			value.PostingAccountID, value.AsOf, value.StatementBalance.Currency, value.StatementBalance.Minor, value.LedgerBalance.Minor,
			value.DifferenceMinor, value.Evidence[0], value.State, value.CreatedBy.ID, value.CreatedAt); err != nil {
			return err
		}
		for _, evidenceID := range value.Evidence {
			if _, err := tx.Exec(ctx, `INSERT INTO spyglass.finance_reconciliation_evidence(account_id,reconciliation_id,evidence_id) VALUES ($1,$2,$3)`, value.AccountID, value.ID, evidenceID); err != nil {
				return err
			}
		}
		if err := insertFinanceEvent(ctx, tx, value.AccountID, "reconciliation", string(value.ID), 0, mutation,
			map[string]any{"ledger_id": value.LedgerID, "posting_account_id": value.PostingAccountID, "currency": value.StatementBalance.Currency, "difference_minor": value.DifferenceMinor, "state": value.State}); err != nil {
			return err
		}
		created = true
		result = value
		return nil
	})
	return result, created, classifyFinance(err)
}

func (repository *FinanceRepository) ConfirmReconciliation(ctx context.Context, accountID ids.AccountID, reconciliationID ids.FinanceReconciliationID, expected uint64, actor domain.Actor, role accounts.MembershipRole, mutation financeapp.Mutation) (domain.Reconciliation, error) {
	if !mutation.Valid() || mutation.Kind != "reconciliation_confirmed" || mutation.Actor != actor {
		return domain.Reconciliation{}, financeapp.ErrInvalid
	}
	var result domain.Reconciliation
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadFinanceReconciliation(ctx, tx, accountID, reconciliationID, true)
		if err != nil {
			return err
		}
		if current.Version == expected+1 && current.State == domain.ReconciliationConfirmed {
			matched, err := financeEventMatches(ctx, tx, accountID, mutation.EventID, "reconciliation", string(reconciliationID), mutation.Kind, expected, expected+1)
			if err != nil || !matched {
				if err != nil {
					return err
				}
				return financeapp.ErrConflict
			}
			result = current
			return nil
		}
		if current.Version != expected {
			return financeapp.ErrConflict
		}
		confirmed, err := current.Confirm(expected, actor, role, mutation.At)
		if err != nil {
			return err
		}
		updated, err := tx.Exec(ctx, `UPDATE spyglass.finance_reconciliations SET state='confirmed',version=$3,confirmed_by_user_id=$4,confirmed_at=$5,updated_at=$5
			WHERE account_id=$1 AND id=$2 AND version=$6`, accountID, reconciliationID, confirmed.Version, actor.ID, mutation.At, expected)
		if err != nil {
			return err
		}
		if updated.RowsAffected() != 1 {
			return financeapp.ErrConflict
		}
		if err := insertFinanceEvent(ctx, tx, accountID, "reconciliation", string(reconciliationID), expected, mutation,
			map[string]any{"ledger_id": confirmed.LedgerID, "posting_account_id": confirmed.PostingAccountID, "currency": confirmed.StatementBalance.Currency, "difference_minor": confirmed.DifferenceMinor, "state": confirmed.State}); err != nil {
			return err
		}
		result = confirmed
		return nil
	})
	return result, classifyFinance(err)
}

func insertFinanceEntryChildren(ctx context.Context, tx pgx.Tx, value domain.JournalEntry) error {
	for index, line := range value.Lines {
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.finance_entry_lines
			(account_id,entry_id,line_number,ledger_id,posting_account_id,memo,debit_minor,credit_minor)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, value.AccountID, value.ID, index+1, value.LedgerID, line.AccountID, line.Memo, line.DebitMinor, line.CreditMinor); err != nil {
			return err
		}
	}
	for _, evidenceID := range value.Evidence {
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.finance_entry_evidence(account_id,entry_id,evidence_id) VALUES ($1,$2,$3)`, value.AccountID, value.ID, evidenceID); err != nil {
			return err
		}
	}
	return nil
}

func insertFinanceReversal(ctx context.Context, tx pgx.Tx, value domain.JournalEntry) error {
	if _, err := tx.Exec(ctx, `INSERT INTO spyglass.finance_entries
		(account_id,id,ledger_id,entry_number,entry_date,description,reference,currency,total_minor,source,state,reversal_of_id,version,created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'reversal','draft',$10,1,'user',$11,$12,$12)`, value.AccountID, value.ID, value.LedgerID,
		value.Number, value.EntryDate, value.Description, value.Reference, value.Currency, value.TotalMinor, value.ReversalOfID, value.CreatedBy.ID, value.CreatedAt); err != nil {
		return err
	}
	if err := insertFinanceEntryChildren(ctx, tx, value); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE spyglass.finance_entries SET state='posted',version=2,posted_by_user_id=$3,posted_at=$4,updated_at=$4
		WHERE account_id=$1 AND id=$2 AND version=1`, value.AccountID, value.ID, value.PostedBy.ID, value.PostedAt)
	return err
}

func loadFinanceLedger(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, ledgerID ids.FinanceLedgerID, lock bool) (domain.Ledger, error) {
	query := `SELECT id,account_id,name,code,description,currency,state,closed_through,version,created_by_kind,created_by_id,created_at,updated_at
		FROM spyglass.finance_ledgers WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.Ledger
	err := tx.QueryRow(ctx, query, accountID, ledgerID).Scan(&value.ID, &value.AccountID, &value.Name, &value.Code, &value.Description, &value.Currency,
		&value.State, &value.ClosedThrough, &value.Version, &value.CreatedBy.Kind, &value.CreatedBy.ID, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Ledger{}, financeapp.ErrNotFound
	}
	if err != nil {
		return domain.Ledger{}, err
	}
	if value.ClosedThrough != nil {
		rows, err := tx.Query(ctx, `SELECT evidence_id FROM spyglass.finance_ledger_close_evidence
			WHERE account_id=$1 AND ledger_id=$2 AND closed_through=$3 AND ledger_version=(
				SELECT max(ledger_version) FROM spyglass.finance_ledger_close_evidence WHERE account_id=$1 AND ledger_id=$2 AND closed_through=$3)
			ORDER BY evidence_id`, accountID, ledgerID, value.ClosedThrough)
		if err != nil {
			return domain.Ledger{}, err
		}
		for rows.Next() {
			var evidenceID ids.KnowledgeEvidenceID
			if err := rows.Scan(&evidenceID); err != nil {
				rows.Close()
				return domain.Ledger{}, err
			}
			value.CloseEvidence = append(value.CloseEvidence, evidenceID)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return domain.Ledger{}, err
		}
	}
	value, err = domain.RestoreLedger(value)
	if err != nil {
		return domain.Ledger{}, financeapp.ErrRepository
	}
	return value, nil
}

func loadFinancePostingAccount(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, postingAccountID ids.FinanceAccountID, lock bool) (domain.PostingAccount, error) {
	query := `SELECT id,account_id,ledger_id,parent_account_id,code,name,description,account_type,normal_balance,allow_posting,state,version,created_by_kind,created_by_id,created_at,updated_at
		FROM spyglass.finance_accounts WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.PostingAccount
	var parentID *string
	err := tx.QueryRow(ctx, query, accountID, postingAccountID).Scan(&value.ID, &value.AccountID, &value.LedgerID, &parentID, &value.Code,
		&value.Name, &value.Description, &value.Type, &value.NormalBalance, &value.AllowPosting, &value.State, &value.Version,
		&value.CreatedBy.Kind, &value.CreatedBy.ID, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PostingAccount{}, financeapp.ErrNotFound
	}
	if err != nil {
		return domain.PostingAccount{}, err
	}
	if parentID != nil {
		value.ParentAccountID = ids.FinanceAccountID(*parentID)
	}
	value, err = domain.RestorePostingAccount(value)
	if err != nil {
		return domain.PostingAccount{}, financeapp.ErrRepository
	}
	return value, nil
}

func loadFinanceEntry(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, entryID ids.FinanceEntryID, lock bool) (domain.JournalEntry, error) {
	query := `SELECT id,account_id,ledger_id,entry_number,entry_date,description,reference,currency,total_minor,source,work_item_id,run_id,invocation_id,state,
		reversal_of_id,reversed_by_id,version,created_by_kind,created_by_id,posted_by_user_id,reversed_by_user_id,created_at,updated_at,posted_at,reversed_at
		FROM spyglass.finance_entries WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.JournalEntry
	var workItemID, runID, invocationID, reversalOfID, reversedByID, postedByID, reversedByUserID *string
	err := tx.QueryRow(ctx, query, accountID, entryID).Scan(&value.ID, &value.AccountID, &value.LedgerID, &value.Number, &value.EntryDate,
		&value.Description, &value.Reference, &value.Currency, &value.TotalMinor, &value.Provenance.Source, &workItemID, &runID, &invocationID,
		&value.State, &reversalOfID, &reversedByID, &value.Version, &value.CreatedBy.Kind, &value.CreatedBy.ID, &postedByID, &reversedByUserID,
		&value.CreatedAt, &value.UpdatedAt, &value.PostedAt, &value.ReversedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.JournalEntry{}, financeapp.ErrNotFound
	}
	if err != nil {
		return domain.JournalEntry{}, err
	}
	if workItemID != nil {
		value.Provenance.WorkItemID = ids.WorkItemID(*workItemID)
	}
	if runID != nil {
		value.Provenance.RunID = ids.RunID(*runID)
	}
	if invocationID != nil {
		value.Provenance.InvocationID = ids.AgentInvocationID(*invocationID)
	}
	if reversalOfID != nil {
		value.ReversalOfID = ids.FinanceEntryID(*reversalOfID)
	}
	if reversedByID != nil {
		value.ReversedByID = ids.FinanceEntryID(*reversedByID)
	}
	if postedByID != nil {
		value.PostedBy = &domain.Actor{Kind: domain.ActorUser, ID: *postedByID}
	}
	if reversedByUserID != nil {
		value.ReversedBy = &domain.Actor{Kind: domain.ActorUser, ID: *reversedByUserID}
	}
	rows, err := tx.Query(ctx, `SELECT posting_account_id,memo,debit_minor,credit_minor FROM spyglass.finance_entry_lines
		WHERE account_id=$1 AND entry_id=$2 ORDER BY line_number`, accountID, entryID)
	if err != nil {
		return domain.JournalEntry{}, err
	}
	for rows.Next() {
		var line domain.JournalLine
		if err := rows.Scan(&line.AccountID, &line.Memo, &line.DebitMinor, &line.CreditMinor); err != nil {
			rows.Close()
			return domain.JournalEntry{}, err
		}
		value.Lines = append(value.Lines, line)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return domain.JournalEntry{}, err
	}
	evidenceRows, err := tx.Query(ctx, `SELECT evidence_id FROM spyglass.finance_entry_evidence WHERE account_id=$1 AND entry_id=$2 ORDER BY evidence_id`, accountID, entryID)
	if err != nil {
		return domain.JournalEntry{}, err
	}
	for evidenceRows.Next() {
		var evidenceID ids.KnowledgeEvidenceID
		if err := evidenceRows.Scan(&evidenceID); err != nil {
			evidenceRows.Close()
			return domain.JournalEntry{}, err
		}
		value.Evidence = append(value.Evidence, evidenceID)
	}
	err = evidenceRows.Err()
	evidenceRows.Close()
	if err != nil {
		return domain.JournalEntry{}, err
	}
	value, err = domain.RestoreJournalEntry(value)
	if err != nil {
		return domain.JournalEntry{}, financeapp.ErrRepository
	}
	return value, nil
}

func loadFinanceReconciliation(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, reconciliationID ids.FinanceReconciliationID, lock bool) (domain.Reconciliation, error) {
	query := `SELECT id,account_id,ledger_id,posting_account_id,as_of,currency,statement_balance_minor,ledger_balance_minor,difference_minor,state,version,
		created_by_user_id,confirmed_by_user_id,created_at,updated_at,confirmed_at FROM spyglass.finance_reconciliations WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value domain.Reconciliation
	var currency domain.Currency
	var confirmedByID *string
	err := tx.QueryRow(ctx, query, accountID, reconciliationID).Scan(&value.ID, &value.AccountID, &value.LedgerID, &value.PostingAccountID,
		&value.AsOf, &currency, &value.StatementBalance.Minor, &value.LedgerBalance.Minor, &value.DifferenceMinor, &value.State, &value.Version,
		&value.CreatedBy.ID, &confirmedByID, &value.CreatedAt, &value.UpdatedAt, &value.ConfirmedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Reconciliation{}, financeapp.ErrNotFound
	}
	if err != nil {
		return domain.Reconciliation{}, err
	}
	value.StatementBalance.Currency, value.LedgerBalance.Currency = currency, currency
	value.CreatedBy.Kind = domain.ActorUser
	if confirmedByID != nil {
		value.ConfirmedBy = &domain.Actor{Kind: domain.ActorUser, ID: *confirmedByID}
	}
	rows, err := tx.Query(ctx, `SELECT evidence_id FROM spyglass.finance_reconciliation_evidence WHERE account_id=$1 AND reconciliation_id=$2 ORDER BY evidence_id`, accountID, reconciliationID)
	if err != nil {
		return domain.Reconciliation{}, err
	}
	for rows.Next() {
		var evidenceID ids.KnowledgeEvidenceID
		if err := rows.Scan(&evidenceID); err != nil {
			rows.Close()
			return domain.Reconciliation{}, err
		}
		value.Evidence = append(value.Evidence, evidenceID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return domain.Reconciliation{}, err
	}
	value, err = domain.RestoreReconciliation(value)
	if err != nil {
		return domain.Reconciliation{}, financeapp.ErrRepository
	}
	return value, nil
}

func insertFinanceEvent(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, aggregateKind, aggregateID string, from uint64, mutation financeapp.Mutation, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO spyglass.finance_events
		(account_id,id,aggregate_kind,aggregate_id,event_type,from_version,to_version,actor_kind,actor_id,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, accountID, mutation.EventID, aggregateKind, aggregateID, mutation.Kind,
		from, from+1, mutation.Actor.Kind, mutation.Actor.ID, mutation.CorrelationID, encoded, mutation.At.UTC())
	return err
}

func financeEventMatches(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, eventID, aggregateKind, aggregateID, kind string, from, to uint64) (bool, error) {
	var storedAggregateKind, storedAggregateID, storedKind string
	var storedFrom, storedTo uint64
	err := tx.QueryRow(ctx, `SELECT aggregate_kind,aggregate_id,event_type,from_version,to_version FROM spyglass.finance_events WHERE account_id=$1 AND id=$2`, accountID, eventID).
		Scan(&storedAggregateKind, &storedAggregateID, &storedKind, &storedFrom, &storedTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil && storedAggregateKind == aggregateKind && storedAggregateID == aggregateID && storedKind == kind && storedFrom == from && storedTo == to, err
}

func classifyFinance(err error) error {
	if err == nil || errors.Is(err, financeapp.ErrInvalid) || errors.Is(err, financeapp.ErrNotFound) || errors.Is(err, financeapp.ErrConflict) || errors.Is(err, financeapp.ErrAggregateOverflow) {
		return err
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		if postgresError.Code == "23505" || postgresError.Code == "40001" {
			return errors.Join(financeapp.ErrConflict, err)
		}
		if postgresError.Code == "23503" || postgresError.Code == "23514" || postgresError.Code == "P0001" {
			return errors.Join(financeapp.ErrInvalid, err)
		}
	}
	return financeapp.ClassifyForAdapter(err)
}

func nullableFinanceAccountID(value ids.FinanceAccountID) any {
	if value == "" {
		return nil
	}
	return value
}

func sameFinanceDate(left, right time.Time) bool {
	left, right = left.UTC(), right.UTC()
	return left.Year() == right.Year() && left.Month() == right.Month() && left.Day() == right.Day()
}

func sameFinanceEvidence(left, right []ids.KnowledgeEvidenceID) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[ids.KnowledgeEvidenceID]struct{}, len(left))
	for _, value := range left {
		seen[value] = struct{}{}
	}
	if len(seen) != len(left) {
		return false
	}
	for _, value := range right {
		if _, exists := seen[value]; !exists {
			return false
		}
	}
	return true
}

func financeNullableWorkItemID(value ids.WorkItemID) any {
	if value == "" {
		return nil
	}
	return value
}

func financeNullableRunID(value ids.RunID) any {
	if value == "" {
		return nil
	}
	return value
}

func financeNullableInvocationID(value ids.AgentInvocationID) any {
	if value == "" {
		return nil
	}
	return value
}

var _ financeapp.Store = (*FinanceRepository)(nil)
