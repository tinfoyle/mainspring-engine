// Package financeaction implements approved, idempotent Finance effects for
// the broker's consequential-action boundary.
package financeaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const EntryPostCapability = runnercapability.FinanceEntryPostCapability

var (
	ErrInvalid    = errors.New("finance post action is invalid")
	ErrConflict   = errors.New("finance post action conflicts with durable state")
	ErrRepository = errors.New("finance post action repository is unavailable")
)

type Store interface {
	PostEntry(context.Context, ids.AccountID, ids.FinanceEntryID, uint64, string, ids.UserID) error
	EntryPostedByEvent(context.Context, ids.AccountID, ids.FinanceEntryID, uint64, string) (bool, error)
}

type EntryPostHandler struct{ store Store }

func NewEntryPostHandler(store Store) (*EntryPostHandler, error) {
	if store == nil {
		return nil, ErrInvalid
	}
	return &EntryPostHandler{store: store}, nil
}

type entryPostInput struct {
	EntryID         ids.FinanceEntryID `json:"entry_id"`
	ExpectedVersion uint64             `json:"expected_version"`
}

func (handler *EntryPostHandler) Execute(ctx context.Context, call runnercapability.AuthorizedCall) (json.RawMessage, error) {
	input, err := decodeEntryPostInput(call.Input)
	if err != nil || call.Action == nil || call.Action.IdempotencyKey != call.OperationID || ids.Validate(string(call.ApprovedByUserID)) != nil {
		return nil, actionError{code: "finance_post_input_rejected", cause: ErrInvalid, definitive: true}
	}
	if err := handler.store.PostEntry(ctx, call.Grant.AccountID, input.EntryID, input.ExpectedVersion, call.OperationID, call.ApprovedByUserID); err != nil {
		return nil, classify(err)
	}
	return entryPostOutput(input.EntryID), nil
}

func (handler *EntryPostHandler) Reconcile(ctx context.Context, call runnercapability.AuthorizedCall) (json.RawMessage, runnercapability.ActionOutcome, error) {
	input, err := decodeEntryPostInput(call.Input)
	if err != nil || call.Action == nil || call.Action.IdempotencyKey != call.OperationID {
		failure := actionError{code: "finance_post_binding_invalid", cause: ErrInvalid, definitive: true}
		return nil, runnercapability.ActionFailed, failure
	}
	posted, err := handler.store.EntryPostedByEvent(ctx, call.Grant.AccountID, input.EntryID, input.ExpectedVersion, call.OperationID)
	if err != nil {
		return nil, runnercapability.ActionUnknown, classify(err)
	}
	if !posted {
		failure := actionError{code: "finance_post_not_applied", cause: ErrConflict, definitive: true}
		return nil, runnercapability.ActionFailed, failure
	}
	return entryPostOutput(input.EntryID), runnercapability.ActionSucceeded, nil
}

func decodeEntryPostInput(raw json.RawMessage) (entryPostInput, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input entryPostInput
	if err := decoder.Decode(&input); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) ||
		ids.Validate(string(input.EntryID)) != nil || input.ExpectedVersion == 0 {
		return entryPostInput{}, ErrInvalid
	}
	return input, nil
}

func entryPostOutput(entryID ids.FinanceEntryID) json.RawMessage {
	encoded, _ := json.Marshal(struct {
		EntryID ids.FinanceEntryID `json:"entry_id"`
		State   string             `json:"state"`
	}{EntryID: entryID, State: "posted"})
	return encoded
}

type actionError struct {
	code       string
	cause      error
	definitive bool
}

func (failure actionError) Error() string    { return failure.code }
func (failure actionError) Unwrap() error    { return failure.cause }
func (failure actionError) Code() string     { return failure.code }
func (failure actionError) Definitive() bool { return failure.definitive }

func classify(err error) error {
	switch {
	case errors.Is(err, ErrInvalid):
		return actionError{code: "finance_post_input_rejected", cause: err, definitive: true}
	case errors.Is(err, ErrConflict):
		return actionError{code: "finance_post_state_conflict", cause: err, definitive: true}
	default:
		return actionError{code: "finance_post_repository_unknown", cause: err}
	}
}

var _ runnercapability.ConsequentialHandler = (*EntryPostHandler)(nil)
