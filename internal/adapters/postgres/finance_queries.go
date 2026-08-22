package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	financeapp "github.com/tinfoyle/spyglass-engine/internal/application/finance"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (repository *FinanceRepository) ListLedgers(ctx context.Context, accountID ids.AccountID, query financeapp.LedgerListQuery) (financeapp.LedgerPage, error) {
	if !query.Valid() {
		return financeapp.LedgerPage{}, financeapp.ErrInvalid
	}
	page := financeapp.LedgerPage{Items: make([]financeapp.LedgerSummary, 0, query.Limit)}
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT l.id,l.account_id,l.name,l.code,l.description,l.currency,l.state,l.closed_through,l.version,
			(SELECT count(*) FROM spyglass.finance_accounts a WHERE a.account_id=l.account_id AND a.ledger_id=l.id),
			(SELECT count(*) FROM spyglass.finance_entries e WHERE e.account_id=l.account_id AND e.ledger_id=l.id AND e.state='draft'),
			m.income BETWEEN -9223372036854775808::numeric AND 9223372036854775807::numeric AND
			m.expense BETWEEN -9223372036854775808::numeric AND 9223372036854775807::numeric AND
			(m.income-m.expense) BETWEEN -9223372036854775808::numeric AND 9223372036854775807::numeric,
			CASE WHEN m.income BETWEEN -9223372036854775808::numeric AND 9223372036854775807::numeric THEN m.income::bigint END,
			CASE WHEN m.expense BETWEEN -9223372036854775808::numeric AND 9223372036854775807::numeric THEN m.expense::bigint END,
			CASE WHEN (m.income-m.expense) BETWEEN -9223372036854775808::numeric AND 9223372036854775807::numeric THEN (m.income-m.expense)::bigint END,
			l.updated_at
		FROM spyglass.finance_ledgers l
		CROSS JOIN LATERAL (SELECT
			COALESCE(sum((x.credit_minor::numeric-x.debit_minor::numeric)) FILTER (WHERE a.account_type='income'),0) AS income,
			COALESCE(sum((x.debit_minor::numeric-x.credit_minor::numeric)) FILTER (WHERE a.account_type='expense'),0) AS expense
			FROM spyglass.finance_entry_lines x
			JOIN spyglass.finance_entries e ON e.account_id=x.account_id AND e.id=x.entry_id
			JOIN spyglass.finance_accounts a ON a.account_id=x.account_id AND a.ledger_id=x.ledger_id AND a.id=x.posting_account_id
			WHERE x.account_id=l.account_id AND x.ledger_id=l.id AND e.state IN ('posted','reversed')) m
		WHERE l.account_id=$1 AND ($2='' OR l.state=$2) AND ($3::text IS NULL OR (l.code,l.id)>($3,$4::uuid))
		ORDER BY l.code,l.id LIMIT $5`, accountID, query.State, financeLedgerCursorCode(query.After), financeLedgerCursorID(query.After), query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item financeapp.LedgerSummary
			var fits bool
			var income, expense, net *int64
			if err := rows.Scan(&item.ID, &item.AccountID, &item.Name, &item.Code, &item.Description, &item.Currency, &item.State,
				&item.ClosedThrough, &item.Version, &item.AccountCount, &item.DraftCount, &fits, &income, &expense, &net, &item.UpdatedAt); err != nil {
				return err
			}
			if !fits || income == nil || expense == nil || net == nil {
				return financeapp.ErrAggregateOverflow
			}
			item.IncomeMinor, item.ExpenseMinor, item.NetMinor = *income, *expense, *net
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return financeapp.LedgerPage{}, classifyFinance(err)
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &financeapp.LedgerCursor{Code: last.Code, ID: last.ID}
	}
	return page, nil
}

func (repository *FinanceRepository) GetPostingAccount(ctx context.Context, accountID ids.AccountID, postingAccountID ids.FinanceAccountID) (domain.PostingAccount, error) {
	var result domain.PostingAccount
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := loadFinancePostingAccount(ctx, tx, accountID, postingAccountID, false)
		result = value
		return err
	})
	return result, classifyFinance(err)
}

func (repository *FinanceRepository) ListPostingAccounts(ctx context.Context, accountID ids.AccountID, query financeapp.PostingAccountListQuery) (financeapp.PostingAccountPage, error) {
	if !query.Valid() {
		return financeapp.PostingAccountPage{}, financeapp.ErrInvalid
	}
	page := financeapp.PostingAccountPage{Items: make([]financeapp.PostingAccountSummary, 0, query.Limit)}
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT a.id,a.account_id,a.ledger_id,a.parent_account_id,a.code,a.name,a.description,a.account_type,a.normal_balance,
			a.allow_posting,a.state,a.version,a.created_by_kind,a.created_by_id,a.created_at,a.updated_at,
			m.balance BETWEEN -9223372036854775808::numeric AND 9223372036854775807::numeric,
			CASE WHEN m.balance BETWEEN -9223372036854775808::numeric AND 9223372036854775807::numeric THEN m.balance::bigint END
		FROM spyglass.finance_accounts a
		CROSS JOIN LATERAL (SELECT CASE WHEN a.normal_balance='debit' THEN COALESCE(sum(x.debit_minor::numeric-x.credit_minor::numeric),0)
			ELSE COALESCE(sum(x.credit_minor::numeric-x.debit_minor::numeric),0) END AS balance
			FROM spyglass.finance_entry_lines x JOIN spyglass.finance_entries e ON e.account_id=x.account_id AND e.id=x.entry_id
			WHERE x.account_id=a.account_id AND x.ledger_id=a.ledger_id AND x.posting_account_id=a.id AND e.state IN ('posted','reversed')) m
		WHERE a.account_id=$1 AND a.ledger_id=$2 AND ($3='' OR a.state=$3) AND ($4::text IS NULL OR (a.code,a.id)>($4,$5::uuid))
		ORDER BY a.code,a.id LIMIT $6`, accountID, query.LedgerID, query.State, financeAccountCursorCode(query.After), financeAccountCursorID(query.After), query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item financeapp.PostingAccountSummary
			var parentID *string
			var fits bool
			var balance *int64
			if err := rows.Scan(&item.Account.ID, &item.Account.AccountID, &item.Account.LedgerID, &parentID, &item.Account.Code, &item.Account.Name,
				&item.Account.Description, &item.Account.Type, &item.Account.NormalBalance, &item.Account.AllowPosting, &item.Account.State,
				&item.Account.Version, &item.Account.CreatedBy.Kind, &item.Account.CreatedBy.ID, &item.Account.CreatedAt, &item.Account.UpdatedAt, &fits, &balance); err != nil {
				return err
			}
			if parentID != nil {
				item.Account.ParentAccountID = ids.FinanceAccountID(*parentID)
			}
			item.Account, err = domain.RestorePostingAccount(item.Account)
			if err != nil {
				return financeapp.ErrRepository
			}
			if !fits || balance == nil {
				return financeapp.ErrAggregateOverflow
			}
			item.BalanceMinor = *balance
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return financeapp.PostingAccountPage{}, classifyFinance(err)
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1].Account
		page.NextCursor = &financeapp.PostingAccountCursor{Code: last.Code, ID: last.ID}
	}
	return page, nil
}

func (repository *FinanceRepository) ListEntries(ctx context.Context, accountID ids.AccountID, query financeapp.EntryListQuery) (financeapp.EntryPage, error) {
	if !query.Valid() {
		return financeapp.EntryPage{}, financeapp.ErrInvalid
	}
	page := financeapp.EntryPage{Items: make([]financeapp.EntrySummary, 0, query.Limit)}
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,account_id,ledger_id,entry_number,entry_date,description,reference,currency,total_minor,source,state,
			reversal_of_id,reversed_by_id,version,updated_at FROM spyglass.finance_entries
		WHERE account_id=$1 AND ledger_id=$2 AND ($3='' OR state=$3) AND ($4::date IS NULL OR (entry_date,entry_number)<($4,$5))
		ORDER BY entry_date DESC,entry_number DESC LIMIT $6`, accountID, query.LedgerID, query.State, financeEntryCursorDate(query.After), financeEntryCursorNumber(query.After), query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item financeapp.EntrySummary
			var reversalOfID, reversedByID *string
			if err := rows.Scan(&item.ID, &item.AccountID, &item.LedgerID, &item.Number, &item.EntryDate, &item.Description, &item.Reference,
				&item.Currency, &item.TotalMinor, &item.Source, &item.State, &reversalOfID, &reversedByID, &item.Version, &item.UpdatedAt); err != nil {
				return err
			}
			if reversalOfID != nil {
				item.ReversalOfID = ids.FinanceEntryID(*reversalOfID)
			}
			if reversedByID != nil {
				item.ReversedByID = ids.FinanceEntryID(*reversedByID)
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return financeapp.EntryPage{}, classifyFinance(err)
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &financeapp.EntryCursor{EntryDate: last.EntryDate, Number: last.Number}
	}
	return page, nil
}

func (repository *FinanceRepository) GetReconciliation(ctx context.Context, accountID ids.AccountID, reconciliationID ids.FinanceReconciliationID) (domain.Reconciliation, error) {
	var result domain.Reconciliation
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := loadFinanceReconciliation(ctx, tx, accountID, reconciliationID, false)
		result = value
		return err
	})
	return result, classifyFinance(err)
}

func (repository *FinanceRepository) ListReconciliations(ctx context.Context, accountID ids.AccountID, query financeapp.ReconciliationListQuery) (financeapp.ReconciliationPage, error) {
	if !query.Valid() {
		return financeapp.ReconciliationPage{}, financeapp.ErrInvalid
	}
	page := financeapp.ReconciliationPage{Items: make([]financeapp.ReconciliationSummary, 0, query.Limit)}
	err := repository.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,account_id,ledger_id,posting_account_id,as_of,currency,statement_balance_minor,ledger_balance_minor,
			difference_minor,state,version,updated_at FROM spyglass.finance_reconciliations
		WHERE account_id=$1 AND ledger_id=$2 AND ($3='' OR state=$3) AND ($4::date IS NULL OR (as_of,id)<($4,$5::uuid))
		ORDER BY as_of DESC,id DESC LIMIT $6`, accountID, query.LedgerID, query.State, financeReconciliationCursorDate(query.After), financeReconciliationCursorID(query.After), query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item financeapp.ReconciliationSummary
			var currency domain.Currency
			if err := rows.Scan(&item.ID, &item.AccountID, &item.LedgerID, &item.PostingAccountID, &item.AsOf, &currency,
				&item.StatementBalance.Minor, &item.LedgerBalance.Minor, &item.DifferenceMinor, &item.State, &item.Version, &item.UpdatedAt); err != nil {
				return err
			}
			item.StatementBalance.Currency, item.LedgerBalance.Currency = currency, currency
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return financeapp.ReconciliationPage{}, classifyFinance(err)
	}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &financeapp.ReconciliationCursor{AsOf: last.AsOf, ID: last.ID}
	}
	return page, nil
}

func financeLedgerCursorCode(cursor *financeapp.LedgerCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.Code
}

func financeLedgerCursorID(cursor *financeapp.LedgerCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.ID
}

func financeAccountCursorCode(cursor *financeapp.PostingAccountCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.Code
}

func financeAccountCursorID(cursor *financeapp.PostingAccountCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.ID
}

func financeEntryCursorDate(cursor *financeapp.EntryCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.EntryDate.UTC()
}

func financeEntryCursorNumber(cursor *financeapp.EntryCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.Number
}

func financeReconciliationCursorDate(cursor *financeapp.ReconciliationCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.AsOf.UTC()
}

func financeReconciliationCursorID(cursor *financeapp.ReconciliationCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.ID
}

var _ financeapp.Store = (*FinanceRepository)(nil)
