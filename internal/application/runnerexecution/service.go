// Package runnerexecution orchestrates one bounded runner invocation while
// keeping all external capabilities behind the broker client.
package runnerexecution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
)

const MaximumExecutors = 32

var (
	ErrInvalidConfiguration = errors.New("runner execution configuration is invalid")
	ErrExecutionFailed      = errors.New("runner invocation execution failed")
	validErrorCode          = regexp.MustCompile(`^[a-z][a-z0-9_]{0,99}$`)
)

type Broker interface {
	Fetch(context.Context) (runnerbroker.Request, error)
	Submit(context.Context, runnerbroker.Result) (bool, error)
}

type Capabilities interface {
	Invoke(context.Context, runnercapability.Call) (runnercapability.Result, error)
}

type Execution struct {
	Kind         string
	Input        json.RawMessage
	Capabilities []string
	Gateway      Capabilities
	ExpiresAt    time.Time
}

type Executor interface {
	Execute(context.Context, Execution) (json.RawMessage, error)
}

type ExecutorFunc func(context.Context, Execution) (json.RawMessage, error)

func (f ExecutorFunc) Execute(ctx context.Context, execution Execution) (json.RawMessage, error) {
	return f(ctx, execution)
}

type Definition struct {
	Kind     string
	Executor Executor
}

type Service struct {
	broker    Broker
	gateway   Capabilities
	executors map[string]Executor
}

func New(broker Broker, gateway Capabilities, definitions []Definition) (*Service, error) {
	if broker == nil || gateway == nil || len(definitions) == 0 || len(definitions) > MaximumExecutors {
		return nil, ErrInvalidConfiguration
	}
	executors := make(map[string]Executor, len(definitions))
	for _, definition := range definitions {
		if definition.Executor == nil || !validKind(definition.Kind) {
			return nil, ErrInvalidConfiguration
		}
		if _, exists := executors[definition.Kind]; exists {
			return nil, ErrInvalidConfiguration
		}
		executors[definition.Kind] = definition.Executor
	}
	return &Service{broker: broker, gateway: gateway, executors: executors}, nil
}

func (s *Service) Run(ctx context.Context) error {
	request, err := s.broker.Fetch(ctx)
	if err != nil {
		return err
	}
	executor, exists := s.executors[request.Kind]
	if !exists {
		return s.submitFailure(ctx, "unsupported_kind")
	}
	executionContext, cancel := context.WithDeadline(ctx, request.ExpiresAt)
	defer cancel()
	output, executionErr := executor.Execute(executionContext, Execution{Kind: request.Kind, Input: request.Input, Capabilities: append([]string(nil), request.Capabilities...), Gateway: s.gateway, ExpiresAt: request.ExpiresAt})
	if executionErr != nil {
		return s.submitFailure(ctx, machineCode(executionErr))
	}
	canonical, err := canonicalOutput(output)
	if err != nil {
		return s.submitFailure(ctx, "invalid_output")
	}
	if _, err := s.broker.Submit(ctx, runnerbroker.Result{SchemaVersion: runnerbroker.SchemaVersion, Outcome: "completed", Output: canonical}); err != nil {
		return err
	}
	return nil
}

func (s *Service) submitFailure(ctx context.Context, code string) error {
	_, err := s.broker.Submit(ctx, runnerbroker.Result{SchemaVersion: runnerbroker.SchemaVersion, Outcome: "execution_failed", Output: json.RawMessage(`{}`), ErrorCode: code})
	if err != nil {
		return err
	}
	return ErrExecutionFailed
}

func canonicalOutput(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > runnerbroker.MaximumOutputBytes {
		return nil, ErrExecutionFailed
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return nil, ErrExecutionFailed
	}
	canonical, err := json.Marshal(value)
	if err != nil || len(canonical) > runnerbroker.MaximumOutputBytes {
		return nil, ErrExecutionFailed
	}
	return canonical, nil
}

type codedError interface{ Code() string }

func machineCode(err error) string {
	var coded codedError
	if errors.As(err, &coded) && validErrorCode.MatchString(coded.Code()) {
		return coded.Code()
	}
	return "execution_failed"
}

func validKind(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '.' && character != '-' {
			return false
		}
	}
	return true
}
