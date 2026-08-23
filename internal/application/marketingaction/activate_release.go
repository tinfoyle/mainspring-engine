// Package marketingaction implements approved, idempotent Marketing effects
// for the broker's consequential-action boundary.
package marketingaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const ReleaseActivateCapability = runnercapability.MarketingReleaseActivateCapability

var (
	ErrInvalid    = errors.New("marketing release activation action is invalid")
	ErrConflict   = errors.New("marketing release activation action conflicts with durable state")
	ErrRepository = errors.New("marketing release activation action repository is unavailable")
)

type Store interface {
	ActivateRelease(context.Context, ids.AccountID, ids.MarketingCampaignID, uint64, ids.MarketingReleaseID, uint64, string, ids.UserID) error
	ReleaseActivatedByEvent(context.Context, ids.AccountID, ids.MarketingCampaignID, uint64, ids.MarketingReleaseID, uint64, string) (bool, error)
}

type ReleaseActivateHandler struct{ store Store }

func NewReleaseActivateHandler(store Store) (*ReleaseActivateHandler, error) {
	if store == nil {
		return nil, ErrInvalid
	}
	return &ReleaseActivateHandler{store: store}, nil
}

type releaseActivateInput struct {
	CampaignID      ids.MarketingCampaignID `json:"campaign_id"`
	CampaignVersion uint64                  `json:"campaign_version"`
	ReleaseID       ids.MarketingReleaseID  `json:"release_id"`
	ReleaseVersion  uint64                  `json:"release_version"`
}

func (handler *ReleaseActivateHandler) Execute(ctx context.Context, call runnercapability.AuthorizedCall) (json.RawMessage, error) {
	input, err := decodeReleaseActivateInput(call.Input)
	if err != nil || call.Action == nil || call.Action.IdempotencyKey != call.OperationID ||
		call.Action.Capability != ReleaseActivateCapability || ids.Validate(string(call.ApprovedByUserID)) != nil {
		return nil, actionError{code: "marketing_release_activation_input_rejected", cause: ErrInvalid, definitive: true}
	}
	if err := handler.store.ActivateRelease(ctx, call.Grant.AccountID, input.CampaignID, input.CampaignVersion, input.ReleaseID, input.ReleaseVersion, call.OperationID, call.ApprovedByUserID); err != nil {
		return nil, classify(err)
	}
	return releaseActivateOutput(input), nil
}

func (handler *ReleaseActivateHandler) Reconcile(ctx context.Context, call runnercapability.AuthorizedCall) (json.RawMessage, runnercapability.ActionOutcome, error) {
	input, err := decodeReleaseActivateInput(call.Input)
	if err != nil || call.Action == nil || call.Action.IdempotencyKey != call.OperationID || call.Action.Capability != ReleaseActivateCapability {
		failure := actionError{code: "marketing_release_activation_binding_invalid", cause: ErrInvalid, definitive: true}
		return nil, runnercapability.ActionFailed, failure
	}
	activated, err := handler.store.ReleaseActivatedByEvent(ctx, call.Grant.AccountID, input.CampaignID, input.CampaignVersion, input.ReleaseID, input.ReleaseVersion, call.OperationID)
	if err != nil {
		return nil, runnercapability.ActionUnknown, classify(err)
	}
	if !activated {
		failure := actionError{code: "marketing_release_activation_not_applied", cause: ErrConflict, definitive: true}
		return nil, runnercapability.ActionFailed, failure
	}
	return releaseActivateOutput(input), runnercapability.ActionSucceeded, nil
}

func decodeReleaseActivateInput(raw json.RawMessage) (releaseActivateInput, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input releaseActivateInput
	if err := decoder.Decode(&input); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) ||
		ids.Validate(string(input.CampaignID)) != nil || input.CampaignVersion == 0 ||
		ids.Validate(string(input.ReleaseID)) != nil || input.ReleaseVersion == 0 {
		return releaseActivateInput{}, ErrInvalid
	}
	return input, nil
}

func releaseActivateOutput(input releaseActivateInput) json.RawMessage {
	encoded, _ := json.Marshal(struct {
		CampaignID ids.MarketingCampaignID `json:"campaign_id"`
		ReleaseID  ids.MarketingReleaseID  `json:"release_id"`
		State      string                  `json:"state"`
	}{CampaignID: input.CampaignID, ReleaseID: input.ReleaseID, State: "active"})
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
		return actionError{code: "marketing_release_activation_input_rejected", cause: err, definitive: true}
	case errors.Is(err, ErrConflict):
		return actionError{code: "marketing_release_activation_state_conflict", cause: err, definitive: true}
	default:
		return actionError{code: "marketing_release_activation_repository_unknown", cause: err}
	}
}

var _ runnercapability.ConsequentialHandler = (*ReleaseActivateHandler)(nil)
