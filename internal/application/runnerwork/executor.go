// Package runnerwork contains compiled runner invocation kinds owned by the
// Work package. Executors receive no database or provider credentials and can
// reach Work only through the invocation-bound capability gateway.
package runnerwork

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerexecution"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const SummarySnapshotKind = "work.summary.snapshot"

var (
	ErrInvalidInput      = errors.New("Work summary runner input is invalid")
	ErrCapabilityDenied  = errors.New("Work summary capability was not granted")
	ErrCapabilityFailed  = errors.New("Work summary capability failed")
	ErrInvalidToolOutput = errors.New("Work summary capability output is invalid")
)

type SummarySnapshotInput struct {
	OperationID string `json:"operation_id"`
}

type SummarySnapshotOutput struct {
	Active     uint64 `json:"active"`
	InProgress uint64 `json:"in_progress"`
	Waiting    uint64 `json:"waiting"`
	Urgent     uint64 `json:"urgent"`
	Done       uint64 `json:"done"`
}

// SummarySnapshotExecutor produces a point-in-time Work summary using one
// caller-selected, invocation-bound operation ID. It cannot widen the grant or
// select an Account; both remain broker authority.
type SummarySnapshotExecutor struct{}

func (SummarySnapshotExecutor) Execute(ctx context.Context, execution runnerexecution.Execution) (json.RawMessage, error) {
	if execution.Kind != SummarySnapshotKind || execution.Gateway == nil {
		return nil, coded("invalid_input", ErrInvalidInput)
	}
	if !slices.Contains(execution.Capabilities, runnercapability.WorkSummaryCapability) {
		return nil, coded("capability_not_granted", ErrCapabilityDenied)
	}
	input, err := decodeExact[SummarySnapshotInput](execution.Input)
	if err != nil || ids.Validate(input.OperationID) != nil {
		return nil, coded("invalid_input", ErrInvalidInput)
	}
	result, err := execution.Gateway.Invoke(ctx, runnercapability.Call{
		SchemaVersion: runnercapability.SchemaVersion,
		OperationID:   input.OperationID,
		Capability:    runnercapability.WorkSummaryCapability,
		Input:         json.RawMessage(`{}`),
	})
	if err != nil {
		return nil, coded("capability_failed", ErrCapabilityFailed)
	}
	if result.SchemaVersion != runnercapability.SchemaVersion {
		return nil, coded("capability_output_invalid", ErrInvalidToolOutput)
	}
	output, err := decodeExact[SummarySnapshotOutput](result.Output)
	if err != nil {
		return nil, coded("capability_output_invalid", ErrInvalidToolOutput)
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return nil, coded("capability_output_invalid", ErrInvalidToolOutput)
	}
	return raw, nil
}

type executorError struct {
	code  string
	cause error
}

func coded(code string, cause error) error { return &executorError{code: code, cause: cause} }
func (e *executorError) Error() string     { return e.code }
func (e *executorError) Code() string      { return e.code }
func (e *executorError) Unwrap() error     { return e.cause }

func decodeExact[T any](raw json.RawMessage) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || raw[0] != '{' || decoder.Decode(&value) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return value, ErrInvalidInput
	}
	return value, nil
}

var _ runnerexecution.Executor = SummarySnapshotExecutor{}
