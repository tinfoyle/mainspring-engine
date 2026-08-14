package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
	"github.com/tinfoyle/mainspring-engine/internal/finance"
)

const (
	FinanceQueryTool  = "finance.query"
	FinanceManageTool = "finance.manage"
)

var financeQuerySchema = json.RawMessage(`{
  "type":"object","additionalProperties":false,"required":["action"],
  "properties":{
    "action":{"type":"string","enum":["list_ledgers","get_ledger","list_entries","get_entry"]},
    "ledger_id":{"type":"string","format":"uuid"},"entry_id":{"type":"string","format":"uuid"},
    "limit":{"type":"integer","minimum":1,"maximum":200}
  }
}`)

var financeManageSchema = json.RawMessage(`{
  "type":"object","additionalProperties":false,"required":["action"],
  "properties":{
    "action":{"type":"string","enum":["create_ledger","update_ledger","create_account","update_account","create_entry","update_entry","post_entry","void_entry"]},
    "ledger_id":{"type":"string","format":"uuid"},"account_id":{"type":"string","format":"uuid"},"entry_id":{"type":"string","format":"uuid"},
    "name":{"type":"string","maxLength":160},"code":{"type":"string","maxLength":40},"description":{"type":"string","maxLength":500},
    "currency":{"type":"string","minLength":3,"maxLength":3},"status":{"type":"string"},"parent_account_id":{"type":"string"},
    "account_type":{"type":"string","enum":["asset","liability","equity","income","expense"]},"allow_posting":{"type":"boolean"},
    "create_standard_accounts":{"type":"boolean"},"entry_date":{"type":"string","format":"date"},"reference":{"type":"string","maxLength":200},
    "work_item_id":{"type":"string"},"lines":{"type":"array","minItems":2,"maxItems":100,"items":{"type":"object","additionalProperties":false,"required":["account_id"],"properties":{"account_id":{"type":"string","format":"uuid"},"memo":{"type":"string","maxLength":500},"debit_minor":{"type":"integer","minimum":0},"credit_minor":{"type":"integer","minimum":0}}}}
  }
}`)

type financeToolInput struct {
	Action                 string                   `json:"action"`
	LedgerID               string                   `json:"ledger_id"`
	AccountID              string                   `json:"account_id"`
	EntryID                string                   `json:"entry_id"`
	Name                   string                   `json:"name"`
	Code                   string                   `json:"code"`
	Description            string                   `json:"description"`
	Currency               string                   `json:"currency"`
	Status                 string                   `json:"status"`
	ParentAccountID        string                   `json:"parent_account_id"`
	AccountType            string                   `json:"account_type"`
	EntryDate              string                   `json:"entry_date"`
	Reference              string                   `json:"reference"`
	WorkItemID             string                   `json:"work_item_id"`
	Limit                  int                      `json:"limit"`
	AllowPosting           bool                     `json:"allow_posting"`
	CreateStandardAccounts bool                     `json:"create_standard_accounts"`
	Lines                  []finance.EntryLineInput `json:"lines"`
}

func RegisterFinance(broker *Broker, service *finance.Service) error {
	if broker == nil || service == nil {
		return errors.New("finance broker and service are required")
	}
	if err := broker.RegisterDefinition(Definition{Name: FinanceQueryTool, Capability: domain.CapabilityFinanceRead,
		Description: "Inspect financial ledgers, hierarchical accounts, posted balances, and journal entries. Amounts are integer minor currency units.", InputSchema: financeQuerySchema}, financeQueryHandler(service)); err != nil {
		return err
	}
	return broker.RegisterDefinition(Definition{Name: FinanceManageTool, Capability: domain.CapabilityFinanceManage,
		Description: "Create or update ledgers and sub-accounts; create or edit balanced draft journal entries; post entries; or void posted entries with an automatic reversal. Use integer minor units (for example, $12.34 is 1234). Posted history is immutable and every action is audited.", InputSchema: financeManageSchema}, financeManageHandler(service))
}

func financeActor(call AuthorizedCall) finance.Actor {
	actorType, actorID := call.Claims.ActorType, call.Claims.ActorID
	if actorType == "persona" || actorType == "" {
		actorType, actorID = "agent", call.Claims.PersonaID
	}
	return finance.Actor{Type: actorType, ID: actorID}
}

func financeQueryHandler(service *finance.Service) Handler {
	return func(ctx context.Context, call AuthorizedCall) (json.RawMessage, error) {
		var input financeToolInput
		if err := decodeStrict(call.Input, &input); err != nil {
			return nil, fmt.Errorf("decode finance.query input: %w", err)
		}
		var value any
		var err error
		switch input.Action {
		case "list_ledgers":
			value, err = service.ListLedgers(ctx)
		case "get_ledger":
			var ledger finance.Ledger
			ledger, err = service.GetLedger(ctx, input.LedgerID)
			if err == nil {
				var accounts []finance.Account
				accounts, err = service.Accounts(ctx, input.LedgerID)
				value = map[string]any{"ledger": ledger, "accounts": accounts}
			}
		case "list_entries":
			value, err = service.ListEntries(ctx, input.LedgerID, input.Limit)
		case "get_entry":
			value, err = service.GetEntry(ctx, input.EntryID)
		default:
			return nil, errors.New("finance.query action is invalid")
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"result": value})
	}
}

func financeManageHandler(service *finance.Service) Handler {
	return func(ctx context.Context, call AuthorizedCall) (json.RawMessage, error) {
		var input financeToolInput
		if err := decodeStrict(call.Input, &input); err != nil {
			return nil, fmt.Errorf("decode finance.manage input: %w", err)
		}
		actor := financeActor(call)
		var value any
		var err error
		parseDate := func() (time.Time, error) {
			if input.EntryDate == "" {
				return time.Now(), nil
			}
			return time.Parse("2006-01-02", input.EntryDate)
		}
		switch input.Action {
		case "create_ledger":
			value, err = service.CreateLedger(ctx, finance.CreateLedgerInput{Name: input.Name, Code: input.Code, Description: input.Description, Currency: input.Currency, CreateStandardAccounts: input.CreateStandardAccounts}, actor)
		case "update_ledger":
			value, err = service.UpdateLedger(ctx, input.LedgerID, finance.UpdateLedgerInput{Name: input.Name, Code: input.Code, Description: input.Description, Currency: input.Currency, Status: input.Status}, actor)
		case "create_account":
			value, err = service.CreateAccount(ctx, finance.CreateAccountInput{LedgerID: input.LedgerID, ParentAccountID: input.ParentAccountID, Code: input.Code, Name: input.Name, Description: input.Description, Type: input.AccountType, AllowPosting: input.AllowPosting}, actor)
		case "update_account":
			value, err = service.UpdateAccount(ctx, input.AccountID, finance.UpdateAccountInput{ParentAccountID: input.ParentAccountID, Code: input.Code, Name: input.Name, Description: input.Description, Status: input.Status, AllowPosting: input.AllowPosting}, actor)
		case "create_entry":
			var date time.Time
			date, err = parseDate()
			if err == nil {
				value, err = service.CreateEntry(ctx, finance.CreateEntryInput{LedgerID: input.LedgerID, Description: input.Description, Reference: input.Reference, Source: "agent", EntryDate: date, WorkItemID: input.WorkItemID, RunID: call.Claims.RunID, InvocationID: call.Claims.InvocationID, Lines: input.Lines}, actor)
			}
		case "update_entry":
			var date time.Time
			date, err = parseDate()
			if err == nil {
				value, err = service.UpdateEntry(ctx, input.EntryID, finance.UpdateEntryInput{Description: input.Description, Reference: input.Reference, EntryDate: date, Lines: input.Lines}, actor)
			}
		case "post_entry":
			value, err = service.PostEntry(ctx, input.EntryID, actor)
		case "void_entry":
			value, err = service.VoidEntry(ctx, input.EntryID, actor)
		default:
			return nil, errors.New("finance.manage action is invalid")
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"result": value})
	}
}
