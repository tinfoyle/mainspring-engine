package finance

import "time"

type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Ledger struct {
	ID, Name, Code, Description, Currency, Status string
	AccountCount, DraftCount                      int
	IncomeMinor, ExpenseMinor                     int64
	CreatedAt, UpdatedAt                          time.Time
}

type Account struct {
	ID, LedgerID, ParentAccountID, Code, Name, Description string
	Type, NormalBalance, Status                            string
	AllowPosting                                           bool
	BalanceMinor                                           int64
	CreatedAt, UpdatedAt                                   time.Time
}

type JournalLine struct {
	ID, EntryID, AccountID, AccountCode, AccountName, Memo string
	LineNumber                                             int
	DebitMinor, CreditMinor                                int64
}

type JournalEntry struct {
	ID, LedgerID, Description, Reference, Status, Source string
	ReversalOfID, WorkItemID, RunID, InvocationID        string
	EntryNumber                                          int64
	EntryDate                                            time.Time
	Lines                                                []JournalLine
	TotalMinor                                           int64
	CreatedBy                                            Actor
	PostedAt, VoidedAt                                   *time.Time
	CreatedAt, UpdatedAt                                 time.Time
}

type CreateLedgerInput struct {
	Name, Code, Description, Currency string
	CreateStandardAccounts            bool
}

type UpdateLedgerInput struct{ Name, Code, Description, Currency, Status string }

type CreateAccountInput struct {
	LedgerID, ParentAccountID, Code, Name, Description, Type string
	AllowPosting                                             bool
}

type UpdateAccountInput struct {
	ParentAccountID, Code, Name, Description, Status string
	AllowPosting                                     bool
}

type EntryLineInput struct {
	AccountID   string `json:"account_id"`
	Memo        string `json:"memo,omitempty"`
	DebitMinor  int64  `json:"debit_minor,omitempty"`
	CreditMinor int64  `json:"credit_minor,omitempty"`
}

type CreateEntryInput struct {
	LedgerID, Description, Reference, Source string
	EntryDate                                time.Time
	WorkItemID, RunID, InvocationID          string
	Lines                                    []EntryLineInput
}

type UpdateEntryInput struct {
	Description, Reference string
	EntryDate              time.Time
	Lines                  []EntryLineInput
}

type TrialBalance struct {
	Accounts                            []Account
	TotalDebitsMinor, TotalCreditsMinor int64
	IncomeMinor, ExpenseMinor, NetMinor int64
}
