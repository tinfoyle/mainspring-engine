package finance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound       = errors.New("financial record not found")
	ErrPostedEntry    = errors.New("posted entries are immutable; create a reversal instead")
	ErrUnbalanced     = errors.New("journal entry debits and credits must be equal and greater than zero")
	ErrInvalidAccount = errors.New("journal line account is unavailable for posting")
)

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func normalizeActor(actor Actor) (Actor, error) {
	actor.Type, actor.ID = strings.TrimSpace(actor.Type), strings.TrimSpace(actor.ID)
	if actor.ID == "" {
		return Actor{}, errors.New("financial actor ID is required")
	}
	switch actor.Type {
	case "user", "agent", "mcp", "system":
	default:
		return Actor{}, errors.New("financial actor type is invalid")
	}
	return actor, nil
}

func normalBalance(accountType string) (string, error) {
	switch accountType {
	case "asset", "expense":
		return "debit", nil
	case "liability", "equity", "income":
		return "credit", nil
	default:
		return "", errors.New("account type must be asset, liability, equity, income, or expense")
	}
}

func validateCurrency(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		value = "USD"
	}
	if len(value) != 3 {
		return "", errors.New("currency must be a three-letter ISO code")
	}
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return "", errors.New("currency must be a three-letter ISO code")
		}
	}
	return value, nil
}

func (s *Service) CreateLedger(ctx context.Context, input CreateLedgerInput, actor Actor) (Ledger, error) {
	actor, err := normalizeActor(actor)
	if err != nil {
		return Ledger{}, err
	}
	input.Name, input.Code = strings.TrimSpace(input.Name), strings.ToUpper(strings.TrimSpace(input.Code))
	if input.Name == "" || len(input.Name) > 160 || input.Code == "" || len(input.Code) > 40 {
		return Ledger{}, errors.New("ledger name and code are required")
	}
	input.Currency, err = validateCurrency(input.Currency)
	if err != nil {
		return Ledger{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Ledger{}, err
	}
	defer tx.Rollback(ctx)
	var ledger Ledger
	err = tx.QueryRow(ctx, `INSERT INTO financial_ledgers(name,code,description,currency,created_by_type,created_by_id) VALUES($1,$2,$3,$4,$5,$6)
		RETURNING id::text,name,code,description,currency,status,created_at,updated_at`, input.Name, input.Code, strings.TrimSpace(input.Description), input.Currency, actor.Type, actor.ID).
		Scan(&ledger.ID, &ledger.Name, &ledger.Code, &ledger.Description, &ledger.Currency, &ledger.Status, &ledger.CreatedAt, &ledger.UpdatedAt)
	if err != nil {
		return Ledger{}, fmt.Errorf("create ledger: %w", err)
	}
	if input.CreateStandardAccounts {
		standard := []struct{ code, name, kind string }{{"1000", "Cash", "asset"}, {"1100", "Accounts Receivable", "asset"}, {"2000", "Accounts Payable", "liability"}, {"3000", "Owner's Equity", "equity"}, {"4000", "Revenue", "income"}, {"5000", "Operating Expenses", "expense"}}
		for _, a := range standard {
			normal, _ := normalBalance(a.kind)
			if _, err = tx.Exec(ctx, `INSERT INTO financial_accounts(ledger_id,code,name,account_type,normal_balance,created_by_type,created_by_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, ledger.ID, a.code, a.name, a.kind, normal, actor.Type, actor.ID); err != nil {
				return Ledger{}, err
			}
		}
	}
	if err = recordEvent(ctx, tx, ledger.ID, "ledger", ledger.ID, "created", actor, nil, ledger); err != nil {
		return Ledger{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Ledger{}, err
	}
	return ledger, nil
}

func (s *Service) UpdateLedger(ctx context.Context, id string, input UpdateLedgerInput, actor Actor) (Ledger, error) {
	actor, err := normalizeActor(actor)
	if err != nil {
		return Ledger{}, err
	}
	if _, err = uuid.Parse(id); err != nil {
		return Ledger{}, ErrNotFound
	}
	input.Name, input.Code = strings.TrimSpace(input.Name), strings.ToUpper(strings.TrimSpace(input.Code))
	if input.Name == "" || input.Code == "" {
		return Ledger{}, errors.New("ledger name and code are required")
	}
	input.Currency, err = validateCurrency(input.Currency)
	if err != nil {
		return Ledger{}, err
	}
	if input.Status != "active" && input.Status != "archived" {
		return Ledger{}, errors.New("ledger status must be active or archived")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Ledger{}, err
	}
	defer tx.Rollback(ctx)
	before, err := getLedger(ctx, tx, id)
	if err != nil {
		return Ledger{}, err
	}
	var after Ledger
	err = tx.QueryRow(ctx, `UPDATE financial_ledgers SET name=$2,code=$3,description=$4,currency=$5,status=$6,updated_at=now() WHERE id=$1 RETURNING id::text,name,code,description,currency,status,created_at,updated_at`, id, input.Name, input.Code, strings.TrimSpace(input.Description), input.Currency, input.Status).Scan(&after.ID, &after.Name, &after.Code, &after.Description, &after.Currency, &after.Status, &after.CreatedAt, &after.UpdatedAt)
	if err != nil {
		return Ledger{}, err
	}
	if err = recordEvent(ctx, tx, id, "ledger", id, "updated", actor, before, after); err != nil {
		return Ledger{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Ledger{}, err
	}
	return after, nil
}

func (s *Service) ListLedgers(ctx context.Context) ([]Ledger, error) {
	rows, err := s.pool.Query(ctx, `SELECT l.id::text,l.name,l.code,l.description,l.currency,l.status,l.created_at,l.updated_at,
		(SELECT count(*) FROM financial_accounts a WHERE a.ledger_id=l.id),
		(SELECT count(*) FROM financial_journal_entries e WHERE e.ledger_id=l.id AND e.status='draft'),
		COALESCE((SELECT sum(x.credit_minor-x.debit_minor) FROM financial_journal_lines x JOIN financial_journal_entries e ON e.id=x.journal_entry_id JOIN financial_accounts a ON a.id=x.account_id WHERE e.ledger_id=l.id AND e.status IN ('posted','voided') AND a.account_type='income'),0),
		COALESCE((SELECT sum(x.debit_minor-x.credit_minor) FROM financial_journal_lines x JOIN financial_journal_entries e ON e.id=x.journal_entry_id JOIN financial_accounts a ON a.id=x.account_id WHERE e.ledger_id=l.id AND e.status IN ('posted','voided') AND a.account_type='expense'),0)
		FROM financial_ledgers l ORDER BY l.status,l.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Ledger
	for rows.Next() {
		var l Ledger
		if err = rows.Scan(&l.ID, &l.Name, &l.Code, &l.Description, &l.Currency, &l.Status, &l.CreatedAt, &l.UpdatedAt, &l.AccountCount, &l.DraftCount, &l.IncomeMinor, &l.ExpenseMinor); err != nil {
			return nil, err
		}
		result = append(result, l)
	}
	return result, rows.Err()
}

func getLedger(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (Ledger, error) {
	var l Ledger
	err := q.QueryRow(ctx, `SELECT id::text,name,code,description,currency,status,created_at,updated_at FROM financial_ledgers WHERE id=$1`, id).Scan(&l.ID, &l.Name, &l.Code, &l.Description, &l.Currency, &l.Status, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Ledger{}, ErrNotFound
	}
	return l, err
}
func (s *Service) GetLedger(ctx context.Context, id string) (Ledger, error) {
	return getLedger(ctx, s.pool, id)
}

func (s *Service) CreateAccount(ctx context.Context, input CreateAccountInput, actor Actor) (Account, error) {
	actor, err := normalizeActor(actor)
	if err != nil {
		return Account{}, err
	}
	normal, err := normalBalance(strings.TrimSpace(input.Type))
	if err != nil {
		return Account{}, err
	}
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	if input.Code == "" || input.Name == "" {
		return Account{}, errors.New("account code and name are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = getLedger(ctx, tx, input.LedgerID); err != nil {
		return Account{}, err
	}
	if input.ParentAccountID != "" {
		var parentLedger string
		if err = tx.QueryRow(ctx, `SELECT ledger_id::text FROM financial_accounts WHERE id=$1`, input.ParentAccountID).Scan(&parentLedger); err != nil || parentLedger != input.LedgerID {
			return Account{}, errors.New("parent account must belong to the same ledger")
		}
	}
	var a Account
	err = tx.QueryRow(ctx, `INSERT INTO financial_accounts(ledger_id,parent_account_id,code,name,description,account_type,normal_balance,allow_posting,created_by_type,created_by_id) VALUES($1,NULLIF($2,'')::uuid,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text,ledger_id::text,COALESCE(parent_account_id::text,''),code,name,description,account_type,normal_balance,allow_posting,status,created_at,updated_at`, input.LedgerID, input.ParentAccountID, input.Code, input.Name, strings.TrimSpace(input.Description), input.Type, normal, input.AllowPosting, actor.Type, actor.ID).Scan(&a.ID, &a.LedgerID, &a.ParentAccountID, &a.Code, &a.Name, &a.Description, &a.Type, &a.NormalBalance, &a.AllowPosting, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return Account{}, err
	}
	if err = recordEvent(ctx, tx, input.LedgerID, "account", a.ID, "created", actor, nil, a); err != nil {
		return Account{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Account{}, err
	}
	return a, nil
}

func (s *Service) UpdateAccount(ctx context.Context, id string, input UpdateAccountInput, actor Actor) (Account, error) {
	actor, err := normalizeActor(actor)
	if err != nil {
		return Account{}, err
	}
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	if input.Code == "" || input.Name == "" {
		return Account{}, errors.New("account code and name are required")
	}
	if input.Status != "active" && input.Status != "archived" {
		return Account{}, errors.New("account status must be active or archived")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback(ctx)
	before, err := getAccount(ctx, tx, id)
	if err != nil {
		return Account{}, err
	}
	if input.ParentAccountID == id {
		return Account{}, errors.New("an account cannot be its own parent")
	}
	if input.ParentAccountID != "" {
		var ledger string
		if err = tx.QueryRow(ctx, `SELECT ledger_id::text FROM financial_accounts WHERE id=$1`, input.ParentAccountID).Scan(&ledger); err != nil || ledger != before.LedgerID {
			return Account{}, errors.New("parent account must belong to the same ledger")
		}
		var cycle bool
		if err = tx.QueryRow(ctx, `WITH RECURSIVE descendants AS (SELECT id FROM financial_accounts WHERE parent_account_id=$1 UNION ALL SELECT a.id FROM financial_accounts a JOIN descendants d ON a.parent_account_id=d.id) SELECT EXISTS(SELECT 1 FROM descendants WHERE id=$2)`, id, input.ParentAccountID).Scan(&cycle); err != nil {
			return Account{}, err
		}
		if cycle {
			return Account{}, errors.New("account hierarchy cannot contain a cycle")
		}
	}
	var a Account
	err = tx.QueryRow(ctx, `UPDATE financial_accounts SET parent_account_id=NULLIF($2,'')::uuid,code=$3,name=$4,description=$5,allow_posting=$6,status=$7,updated_at=now() WHERE id=$1 RETURNING id::text,ledger_id::text,COALESCE(parent_account_id::text,''),code,name,description,account_type,normal_balance,allow_posting,status,created_at,updated_at`, id, input.ParentAccountID, input.Code, input.Name, strings.TrimSpace(input.Description), input.AllowPosting, input.Status).Scan(&a.ID, &a.LedgerID, &a.ParentAccountID, &a.Code, &a.Name, &a.Description, &a.Type, &a.NormalBalance, &a.AllowPosting, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return Account{}, err
	}
	if err = recordEvent(ctx, tx, a.LedgerID, "account", a.ID, "updated", actor, before, a); err != nil {
		return Account{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Account{}, err
	}
	return a, nil
}

func getAccount(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (Account, error) {
	var a Account
	err := q.QueryRow(ctx, `SELECT id::text,ledger_id::text,COALESCE(parent_account_id::text,''),code,name,description,account_type,normal_balance,allow_posting,status,created_at,updated_at FROM financial_accounts WHERE id=$1`, id).Scan(&a.ID, &a.LedgerID, &a.ParentAccountID, &a.Code, &a.Name, &a.Description, &a.Type, &a.NormalBalance, &a.AllowPosting, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

func (s *Service) Accounts(ctx context.Context, ledgerID string) ([]Account, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.id::text,a.ledger_id::text,COALESCE(a.parent_account_id::text,''),a.code,a.name,a.description,a.account_type,a.normal_balance,a.allow_posting,a.status,a.created_at,a.updated_at,
	COALESCE(sum(CASE WHEN e.status IN ('posted','voided') THEN CASE WHEN a.normal_balance='debit' THEN x.debit_minor-x.credit_minor ELSE x.credit_minor-x.debit_minor END ELSE 0 END),0)
	FROM financial_accounts a LEFT JOIN financial_journal_lines x ON x.account_id=a.id LEFT JOIN financial_journal_entries e ON e.id=x.journal_entry_id WHERE a.ledger_id=$1 GROUP BY a.id ORDER BY a.code`, ledgerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err = rows.Scan(&a.ID, &a.LedgerID, &a.ParentAccountID, &a.Code, &a.Name, &a.Description, &a.Type, &a.NormalBalance, &a.AllowPosting, &a.Status, &a.CreatedAt, &a.UpdatedAt, &a.BalanceMinor); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func validateLines(lines []EntryLineInput) error {
	if len(lines) < 2 {
		return errors.New("journal entries require at least two lines")
	}
	var d, c int64
	for _, l := range lines {
		if l.AccountID == "" || l.DebitMinor < 0 || l.CreditMinor < 0 || (l.DebitMinor > 0) == (l.CreditMinor > 0) {
			return errors.New("each line needs one positive debit or credit")
		}
		d += l.DebitMinor
		c += l.CreditMinor
	}
	if d == 0 || d != c {
		return ErrUnbalanced
	}
	return nil
}

func (s *Service) CreateEntry(ctx context.Context, input CreateEntryInput, actor Actor) (JournalEntry, error) {
	actor, err := normalizeActor(actor)
	if err != nil {
		return JournalEntry{}, err
	}
	if err = validateLines(input.Lines); err != nil {
		return JournalEntry{}, err
	}
	input.Description = strings.TrimSpace(input.Description)
	if input.Description == "" {
		return JournalEntry{}, errors.New("entry description is required")
	}
	if input.EntryDate.IsZero() {
		input.EntryDate = time.Now()
	}
	if input.Source == "" {
		input.Source = "manual"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return JournalEntry{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = getLedger(ctx, tx, input.LedgerID); err != nil {
		return JournalEntry{}, err
	}
	var e JournalEntry
	err = tx.QueryRow(ctx, `INSERT INTO financial_journal_entries(ledger_id,entry_date,description,reference,source,work_item_id,run_id,invocation_id,created_by_type,created_by_id) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,NULLIF($8,'')::uuid,$9,$10) RETURNING id::text,entry_number`, input.LedgerID, input.EntryDate, input.Description, strings.TrimSpace(input.Reference), input.Source, input.WorkItemID, input.RunID, input.InvocationID, actor.Type, actor.ID).Scan(&e.ID, &e.EntryNumber)
	if err != nil {
		return JournalEntry{}, err
	}
	if err = replaceLines(ctx, tx, e.ID, input.LedgerID, input.Lines); err != nil {
		return JournalEntry{}, err
	}
	if err = recordEvent(ctx, tx, input.LedgerID, "entry", e.ID, "created", actor, nil, input); err != nil {
		return JournalEntry{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return JournalEntry{}, err
	}
	return s.GetEntry(ctx, e.ID)
}

func replaceLines(ctx context.Context, tx pgx.Tx, entryID, ledgerID string, lines []EntryLineInput) error {
	if _, err := tx.Exec(ctx, `DELETE FROM financial_journal_lines WHERE journal_entry_id=$1`, entryID); err != nil {
		return err
	}
	for i, l := range lines {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM financial_accounts WHERE id=$1 AND ledger_id=$2 AND status='active' AND allow_posting)`, l.AccountID, ledgerID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return ErrInvalidAccount
		}
		if _, err := tx.Exec(ctx, `INSERT INTO financial_journal_lines(journal_entry_id,account_id,line_number,memo,debit_minor,credit_minor) VALUES($1,$2,$3,$4,$5,$6)`, entryID, l.AccountID, i+1, strings.TrimSpace(l.Memo), l.DebitMinor, l.CreditMinor); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) UpdateEntry(ctx context.Context, id string, input UpdateEntryInput, actor Actor) (JournalEntry, error) {
	actor, err := normalizeActor(actor)
	if err != nil {
		return JournalEntry{}, err
	}
	if err = validateLines(input.Lines); err != nil {
		return JournalEntry{}, err
	}
	input.Description = strings.TrimSpace(input.Description)
	if input.Description == "" {
		return JournalEntry{}, errors.New("entry description is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return JournalEntry{}, err
	}
	defer tx.Rollback(ctx)
	before, err := getEntry(ctx, tx, id)
	if err != nil {
		return JournalEntry{}, err
	}
	if before.Status != "draft" {
		return JournalEntry{}, ErrPostedEntry
	}
	if input.EntryDate.IsZero() {
		input.EntryDate = before.EntryDate
	}
	if _, err = tx.Exec(ctx, `UPDATE financial_journal_entries SET entry_date=$2,description=$3,reference=$4,updated_at=now() WHERE id=$1`, id, input.EntryDate, input.Description, strings.TrimSpace(input.Reference)); err != nil {
		return JournalEntry{}, err
	}
	if err = replaceLines(ctx, tx, id, before.LedgerID, input.Lines); err != nil {
		return JournalEntry{}, err
	}
	if err = recordEvent(ctx, tx, before.LedgerID, "entry", id, "updated", actor, before, input); err != nil {
		return JournalEntry{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return JournalEntry{}, err
	}
	return s.GetEntry(ctx, id)
}

func (s *Service) PostEntry(ctx context.Context, id string, actor Actor) (JournalEntry, error) {
	actor, err := normalizeActor(actor)
	if err != nil {
		return JournalEntry{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return JournalEntry{}, err
	}
	defer tx.Rollback(ctx)
	before, err := getEntry(ctx, tx, id)
	if err != nil {
		return JournalEntry{}, err
	}
	if before.Status != "draft" {
		return JournalEntry{}, ErrPostedEntry
	}
	var d, c int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(debit_minor),0),COALESCE(sum(credit_minor),0) FROM financial_journal_lines WHERE journal_entry_id=$1`, id).Scan(&d, &c); err != nil {
		return JournalEntry{}, err
	}
	if d == 0 || d != c {
		return JournalEntry{}, ErrUnbalanced
	}
	if _, err = tx.Exec(ctx, `UPDATE financial_journal_entries SET status='posted',posted_by_type=$2,posted_by_id=$3,posted_at=now(),updated_at=now() WHERE id=$1`, id, actor.Type, actor.ID); err != nil {
		return JournalEntry{}, err
	}
	if err = recordEvent(ctx, tx, before.LedgerID, "entry", id, "posted", actor, before, map[string]any{"status": "posted"}); err != nil {
		return JournalEntry{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return JournalEntry{}, err
	}
	return s.GetEntry(ctx, id)
}

func (s *Service) VoidEntry(ctx context.Context, id string, actor Actor) (JournalEntry, error) {
	actor, err := normalizeActor(actor)
	if err != nil {
		return JournalEntry{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return JournalEntry{}, err
	}
	defer tx.Rollback(ctx)
	before, err := getEntry(ctx, tx, id)
	if err != nil {
		return JournalEntry{}, err
	}
	if before.Status != "posted" {
		return JournalEntry{}, errors.New("only a posted entry can be voided")
	}
	var reversalID string
	err = tx.QueryRow(ctx, `INSERT INTO financial_journal_entries(ledger_id,entry_date,description,reference,status,reversal_of_id,source,created_by_type,created_by_id,posted_by_type,posted_by_id,posted_at) VALUES($1,CURRENT_DATE,$2,$3,'posted',$4,'reversal',$5,$6,$5,$6,now()) RETURNING id::text`, before.LedgerID, "Reversal: "+before.Description, before.Reference, id, actor.Type, actor.ID).Scan(&reversalID)
	if err != nil {
		return JournalEntry{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO financial_journal_lines(journal_entry_id,account_id,line_number,memo,debit_minor,credit_minor) SELECT $1,account_id,line_number,'Reversal: '||memo,credit_minor,debit_minor FROM financial_journal_lines WHERE journal_entry_id=$2`, reversalID, id); err != nil {
		return JournalEntry{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE financial_journal_entries SET status='voided',voided_by_type=$2,voided_by_id=$3,voided_at=now(),updated_at=now() WHERE id=$1`, id, actor.Type, actor.ID); err != nil {
		return JournalEntry{}, err
	}
	if err = recordEvent(ctx, tx, before.LedgerID, "entry", id, "voided", actor, before, map[string]any{"status": "voided", "reversal_id": reversalID}); err != nil {
		return JournalEntry{}, err
	}
	if err = recordEvent(ctx, tx, before.LedgerID, "entry", reversalID, "created_reversal", actor, nil, map[string]any{"reversal_of_id": id}); err != nil {
		return JournalEntry{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return JournalEntry{}, err
	}
	return s.GetEntry(ctx, reversalID)
}

func getEntry(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (JournalEntry, error) {
	var e JournalEntry
	err := q.QueryRow(ctx, `SELECT id::text,ledger_id::text,entry_number,entry_date,description,reference,status,source,COALESCE(reversal_of_id::text,''),COALESCE(work_item_id::text,''),COALESCE(run_id::text,''),COALESCE(invocation_id::text,''),created_by_type,created_by_id,posted_at,voided_at,created_at,updated_at FROM financial_journal_entries WHERE id=$1`, id).Scan(&e.ID, &e.LedgerID, &e.EntryNumber, &e.EntryDate, &e.Description, &e.Reference, &e.Status, &e.Source, &e.ReversalOfID, &e.WorkItemID, &e.RunID, &e.InvocationID, &e.CreatedBy.Type, &e.CreatedBy.ID, &e.PostedAt, &e.VoidedAt, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return JournalEntry{}, ErrNotFound
	}
	return e, err
}
func (s *Service) GetEntry(ctx context.Context, id string) (JournalEntry, error) {
	e, err := getEntry(ctx, s.pool, id)
	if err != nil {
		return e, err
	}
	rows, err := s.pool.Query(ctx, `SELECT x.id::text,x.journal_entry_id::text,x.account_id::text,a.code,a.name,x.line_number,x.memo,x.debit_minor,x.credit_minor FROM financial_journal_lines x JOIN financial_accounts a ON a.id=x.account_id WHERE x.journal_entry_id=$1 ORDER BY x.line_number`, id)
	if err != nil {
		return e, err
	}
	defer rows.Close()
	for rows.Next() {
		var l JournalLine
		if err = rows.Scan(&l.ID, &l.EntryID, &l.AccountID, &l.AccountCode, &l.AccountName, &l.LineNumber, &l.Memo, &l.DebitMinor, &l.CreditMinor); err != nil {
			return e, err
		}
		e.Lines = append(e.Lines, l)
		e.TotalMinor += l.DebitMinor
	}
	return e, rows.Err()
}
func (s *Service) ListEntries(ctx context.Context, ledgerID string, limit int) ([]JournalEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM financial_journal_entries WHERE ledger_id=$1 ORDER BY entry_date DESC,entry_number DESC LIMIT $2`, ledgerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	var out []JournalEntry
	for _, id := range ids {
		e, err := s.GetEntry(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func recordEvent(ctx context.Context, tx pgx.Tx, ledgerID, entityType, entityID, action string, actor Actor, before, after any) error {
	var b, a []byte
	var err error
	if before != nil {
		b, err = json.Marshal(before)
		if err != nil {
			return err
		}
	}
	if after != nil {
		a, err = json.Marshal(after)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO financial_ledger_events(ledger_id,entity_type,entity_id,action,actor_type,actor_id,before_payload,after_payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, ledgerID, entityType, entityID, action, actor.Type, actor.ID, b, a)
	return err
}
