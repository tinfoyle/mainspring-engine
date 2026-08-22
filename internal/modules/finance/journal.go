package finance

import (
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type EntryState string

const (
	EntryStateDraft    EntryState = "draft"
	EntryStatePosted   EntryState = "posted"
	EntryStateReversed EntryState = "reversed"
)

type EntrySource string

const (
	SourceManual   EntrySource = "manual"
	SourceAgent    EntrySource = "agent"
	SourceMCP      EntrySource = "mcp"
	SourceImport   EntrySource = "import"
	SourceSystem   EntrySource = "system"
	SourceReversal EntrySource = "reversal"
)

type Provenance struct {
	Source       EntrySource           `json:"source"`
	WorkItemID   ids.WorkItemID        `json:"work_item_id,omitempty"`
	RunID        ids.RunID             `json:"run_id,omitempty"`
	InvocationID ids.AgentInvocationID `json:"invocation_id,omitempty"`
}

func (value Provenance) valid(actor Actor) bool {
	switch value.Source {
	case SourceManual, SourceMCP, SourceImport, SourceSystem:
	case SourceAgent:
		if actor.Kind != ActorWorkload || ids.Validate(string(value.RunID)) != nil || ids.Validate(string(value.InvocationID)) != nil {
			return false
		}
	case SourceReversal:
		if actor.Kind != ActorUser {
			return false
		}
	default:
		return false
	}
	return (value.WorkItemID == "" || ids.Validate(string(value.WorkItemID)) == nil) &&
		(value.RunID == "" || ids.Validate(string(value.RunID)) == nil) &&
		(value.InvocationID == "" || ids.Validate(string(value.InvocationID)) == nil)
}

type JournalLine struct {
	AccountID   ids.FinanceAccountID `json:"account_id"`
	Memo        string               `json:"memo"`
	DebitMinor  int64                `json:"debit_minor"`
	CreditMinor int64                `json:"credit_minor"`
}

func normalizeLines(values []JournalLine) ([]JournalLine, int64, error) {
	if len(values) < 2 || len(values) > MaximumEntryLines {
		return nil, 0, ErrUnbalanced
	}
	values = append([]JournalLine(nil), values...)
	var debits, credits int64
	for index := range values {
		values[index].Memo = strings.TrimSpace(values[index].Memo)
		line := values[index]
		if ids.Validate(string(line.AccountID)) != nil || !validText(line.Memo, MaximumMemoBytes, false) || line.DebitMinor < 0 || line.CreditMinor < 0 || (line.DebitMinor > 0) == (line.CreditMinor > 0) {
			return nil, 0, ErrUnbalanced
		}
		var err error
		debits, err = add(debits, line.DebitMinor)
		if err != nil {
			return nil, 0, err
		}
		credits, err = add(credits, line.CreditMinor)
		if err != nil {
			return nil, 0, err
		}
	}
	if debits == 0 || debits != credits {
		return nil, 0, ErrUnbalanced
	}
	return values, debits, nil
}

type JournalEntry struct {
	ID           ids.FinanceEntryID        `json:"id"`
	AccountID    ids.AccountID             `json:"account_id"`
	LedgerID     ids.FinanceLedgerID       `json:"ledger_id"`
	Number       uint64                    `json:"number"`
	EntryDate    time.Time                 `json:"entry_date"`
	Description  string                    `json:"description"`
	Reference    string                    `json:"reference"`
	Currency     Currency                  `json:"currency"`
	Lines        []JournalLine             `json:"lines"`
	TotalMinor   int64                     `json:"total_minor"`
	Evidence     []ids.KnowledgeEvidenceID `json:"evidence"`
	Provenance   Provenance                `json:"provenance"`
	State        EntryState                `json:"state"`
	ReversalOfID ids.FinanceEntryID        `json:"reversal_of_id,omitempty"`
	ReversedByID ids.FinanceEntryID        `json:"reversed_by_id,omitempty"`
	Version      uint64                    `json:"version"`
	CreatedBy    Actor                     `json:"created_by"`
	PostedBy     *Actor                    `json:"posted_by,omitempty"`
	ReversedBy   *Actor                    `json:"reversed_by,omitempty"`
	CreatedAt    time.Time                 `json:"created_at"`
	UpdatedAt    time.Time                 `json:"updated_at"`
	PostedAt     *time.Time                `json:"posted_at,omitempty"`
	ReversedAt   *time.Time                `json:"reversed_at,omitempty"`
}

type EntryDraft struct {
	ID          ids.FinanceEntryID
	AccountID   ids.AccountID
	LedgerID    ids.FinanceLedgerID
	Number      uint64
	EntryDate   time.Time
	Description string
	Reference   string
	Currency    string
	Lines       []JournalLine
	Evidence    []ids.KnowledgeEvidenceID
	Provenance  Provenance
	CreatedBy   Actor
	CreatedAt   time.Time
}

func NewJournalEntry(draft EntryDraft, role accounts.MembershipRole) (JournalEntry, error) {
	if draft.CreatedBy.Kind == ActorUser && !canParticipate(role) {
		return JournalEntry{}, ErrRole
	}
	if draft.CreatedBy.Kind == ActorWorkload && draft.Provenance.Source != SourceAgent {
		return JournalEntry{}, ErrRole
	}
	currency, err := NewCurrency(draft.Currency)
	if err != nil {
		return JournalEntry{}, err
	}
	date, err := normalizeDate(draft.EntryDate)
	if err != nil {
		return JournalEntry{}, err
	}
	lines, total, err := normalizeLines(draft.Lines)
	if err != nil {
		return JournalEntry{}, err
	}
	evidence, err := normalizeEvidence(draft.Evidence)
	if err != nil {
		return JournalEntry{}, err
	}
	entry := JournalEntry{ID: draft.ID, AccountID: draft.AccountID, LedgerID: draft.LedgerID, Number: draft.Number, EntryDate: date,
		Description: strings.TrimSpace(draft.Description), Reference: strings.TrimSpace(draft.Reference), Currency: currency, Lines: lines,
		TotalMinor: total, Evidence: evidence, Provenance: draft.Provenance, State: EntryStateDraft, Version: 1, CreatedBy: draft.CreatedBy,
		CreatedAt: draft.CreatedAt.UTC(), UpdatedAt: draft.CreatedAt.UTC()}
	return RestoreJournalEntry(entry)
}

func RestoreJournalEntry(entry JournalEntry) (JournalEntry, error) {
	entry.Description, entry.Reference = strings.TrimSpace(entry.Description), strings.TrimSpace(entry.Reference)
	date, dateErr := normalizeDate(entry.EntryDate)
	currency, currencyErr := NewCurrency(string(entry.Currency))
	lines, total, linesErr := normalizeLines(entry.Lines)
	evidence, evidenceErr := normalizeEvidence(entry.Evidence)
	if ids.Validate(string(entry.ID)) != nil || ids.Validate(string(entry.AccountID)) != nil || ids.Validate(string(entry.LedgerID)) != nil || entry.Number == 0 ||
		dateErr != nil || currencyErr != nil || linesErr != nil || evidenceErr != nil || total != entry.TotalMinor || !validText(entry.Description, MaximumDescriptionBytes, true) ||
		!validText(entry.Reference, MaximumReferenceBytes, false) || !entry.CreatedBy.valid() || !entry.Provenance.valid(entry.CreatedBy) || entry.Version == 0 ||
		entry.CreatedAt.IsZero() || entry.UpdatedAt.Before(entry.CreatedAt) || (entry.State != EntryStateDraft && entry.State != EntryStatePosted && entry.State != EntryStateReversed) {
		return JournalEntry{}, ErrInvalid
	}
	entry.EntryDate, entry.Currency, entry.Lines, entry.Evidence = date, currency, lines, evidence
	entry.CreatedAt, entry.UpdatedAt = entry.CreatedAt.UTC(), entry.UpdatedAt.UTC()
	if !validEntryState(entry) {
		return JournalEntry{}, ErrInvalid
	}
	return entry, nil
}

func validEntryState(entry JournalEntry) bool {
	if entry.ReversalOfID != "" && (ids.Validate(string(entry.ReversalOfID)) != nil || entry.Provenance.Source != SourceReversal || entry.State != EntryStatePosted || entry.ReversedByID != "") {
		return false
	}
	if entry.ReversedByID != "" && (ids.Validate(string(entry.ReversedByID)) != nil || entry.State != EntryStateReversed || entry.ReversalOfID != "") {
		return false
	}
	switch entry.State {
	case EntryStateDraft:
		return entry.PostedBy == nil && entry.ReversedBy == nil && entry.PostedAt == nil && entry.ReversedAt == nil && entry.ReversalOfID == "" && entry.ReversedByID == ""
	case EntryStatePosted:
		return entry.PostedBy != nil && entry.PostedBy.Kind == ActorUser && entry.PostedBy.valid() && entry.PostedAt != nil && !entry.PostedAt.Before(entry.CreatedAt) && entry.ReversedBy == nil && entry.ReversedAt == nil
	case EntryStateReversed:
		return entry.PostedBy != nil && entry.PostedBy.Kind == ActorUser && entry.PostedBy.valid() && entry.PostedAt != nil && entry.ReversedBy != nil && entry.ReversedBy.Kind == ActorUser && entry.ReversedBy.valid() && entry.ReversedAt != nil && !entry.ReversedAt.Before(*entry.PostedAt)
	default:
		return false
	}
}

type EntryRevision struct {
	EntryDate       time.Time
	Description     string
	Reference       string
	Lines           []JournalLine
	Evidence        []ids.KnowledgeEvidenceID
	ExpectedVersion uint64
	Actor           Actor
	Role            accounts.MembershipRole
	At              time.Time
}

func (entry JournalEntry) Revise(command EntryRevision, closedThrough *time.Time) (JournalEntry, error) {
	if command.ExpectedVersion != entry.Version {
		return JournalEntry{}, ErrConflict
	}
	if entry.State != EntryStateDraft || command.Actor.Kind != ActorUser && command.Actor.Kind != ActorWorkload || !command.Actor.valid() || command.At.IsZero() || command.At.UTC().Before(entry.UpdatedAt) {
		return JournalEntry{}, ErrState
	}
	if command.Actor.Kind == ActorUser && !canParticipate(command.Role) || command.Actor.Kind == ActorWorkload && entry.Provenance.Source != SourceAgent {
		return JournalEntry{}, ErrRole
	}
	date, err := normalizeDate(command.EntryDate)
	if err != nil {
		return JournalEntry{}, err
	}
	if closedThrough != nil && !date.After(*closedThrough) {
		return JournalEntry{}, ErrPeriodClosed
	}
	lines, total, err := normalizeLines(command.Lines)
	if err != nil {
		return JournalEntry{}, err
	}
	evidence, err := normalizeEvidence(command.Evidence)
	if err != nil {
		return JournalEntry{}, err
	}
	entry.EntryDate, entry.Description, entry.Reference = date, strings.TrimSpace(command.Description), strings.TrimSpace(command.Reference)
	entry.Lines, entry.TotalMinor, entry.Evidence, entry.Version, entry.UpdatedAt = lines, total, evidence, entry.Version+1, command.At.UTC()
	return RestoreJournalEntry(entry)
}

type PostCommand struct {
	Actor           Actor
	Role            accounts.MembershipRole
	ExpectedVersion uint64
	At              time.Time
}

func (entry JournalEntry) Post(command PostCommand, closedThrough *time.Time) (JournalEntry, error) {
	if command.ExpectedVersion != entry.Version {
		return JournalEntry{}, ErrConflict
	}
	if entry.State != EntryStateDraft {
		return JournalEntry{}, ErrState
	}
	if command.Actor.Kind != ActorUser || !command.Actor.valid() || !canManage(command.Role) {
		return JournalEntry{}, ErrRole
	}
	if len(entry.Evidence) == 0 {
		return JournalEntry{}, ErrEvidence
	}
	if closedThrough != nil && !entry.EntryDate.After(*closedThrough) {
		return JournalEntry{}, ErrPeriodClosed
	}
	if command.At.IsZero() || command.At.UTC().Before(entry.UpdatedAt) {
		return JournalEntry{}, ErrInvalid
	}
	at := command.At.UTC()
	entry.State, entry.PostedBy, entry.PostedAt, entry.Version, entry.UpdatedAt = EntryStatePosted, &command.Actor, &at, entry.Version+1, at
	return RestoreJournalEntry(entry)
}

type ReverseCommand struct {
	ReversalID      ids.FinanceEntryID
	ReversalNumber  uint64
	EntryDate       time.Time
	Description     string
	Reference       string
	Evidence        []ids.KnowledgeEvidenceID
	Actor           Actor
	Role            accounts.MembershipRole
	ExpectedVersion uint64
	At              time.Time
}

func (entry JournalEntry) Reverse(command ReverseCommand, closedThrough *time.Time) (JournalEntry, JournalEntry, error) {
	if command.ExpectedVersion != entry.Version {
		return JournalEntry{}, JournalEntry{}, ErrConflict
	}
	if entry.State != EntryStatePosted || entry.ReversalOfID != "" {
		return JournalEntry{}, JournalEntry{}, ErrState
	}
	if command.Actor.Kind != ActorUser || !command.Actor.valid() || !canManage(command.Role) {
		return JournalEntry{}, JournalEntry{}, ErrRole
	}
	date, err := normalizeDate(command.EntryDate)
	if err != nil || command.At.IsZero() || command.At.UTC().Before(entry.UpdatedAt) {
		return JournalEntry{}, JournalEntry{}, ErrInvalid
	}
	if closedThrough != nil && !date.After(*closedThrough) {
		return JournalEntry{}, JournalEntry{}, ErrPeriodClosed
	}
	evidence, err := normalizeEvidence(command.Evidence)
	if err != nil || len(evidence) == 0 {
		return JournalEntry{}, JournalEntry{}, ErrEvidence
	}
	lines := make([]JournalLine, len(entry.Lines))
	for index, line := range entry.Lines {
		lines[index] = JournalLine{AccountID: line.AccountID, Memo: strings.TrimSpace(line.Memo), DebitMinor: line.CreditMinor, CreditMinor: line.DebitMinor}
	}
	reversal, err := NewJournalEntry(EntryDraft{ID: command.ReversalID, AccountID: entry.AccountID, LedgerID: entry.LedgerID, Number: command.ReversalNumber,
		EntryDate: date, Description: strings.TrimSpace(command.Description), Reference: strings.TrimSpace(command.Reference), Currency: string(entry.Currency), Lines: lines,
		Evidence: evidence, Provenance: Provenance{Source: SourceReversal}, CreatedBy: command.Actor, CreatedAt: command.At.UTC()}, command.Role)
	if err != nil {
		return JournalEntry{}, JournalEntry{}, err
	}
	reversal.ReversalOfID = entry.ID
	reversal, err = reversal.Post(PostCommand{Actor: command.Actor, Role: command.Role, ExpectedVersion: reversal.Version, At: command.At}, closedThrough)
	if err != nil {
		return JournalEntry{}, JournalEntry{}, err
	}
	at := command.At.UTC()
	entry.State, entry.ReversedByID, entry.ReversedBy, entry.ReversedAt, entry.Version, entry.UpdatedAt = EntryStateReversed, reversal.ID, &command.Actor, &at, entry.Version+1, at
	entry, err = RestoreJournalEntry(entry)
	return entry, reversal, err
}
