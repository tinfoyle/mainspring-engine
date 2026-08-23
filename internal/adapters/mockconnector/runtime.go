package mockconnector

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const maximumRuntimeConfigBytes = 24 << 20

var validRuntimeErrorCode = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,99}$`)

type runtimeCredential struct {
	ID             ids.IntegrationCredentialID `json:"id"`
	Generation     uint64                      `json:"generation"`
	MaterialBase64 []byte                      `json:"material_base64"`
}

type runtimeStep struct {
	Mode       domain.AttemptMode    `json:"mode"`
	Outcome    domain.AttemptOutcome `json:"outcome"`
	ErrorCode  string                `json:"error_code,omitempty"`
	RetryAfter string                `json:"retry_after,omitempty"`
}

type runtimeScript struct {
	ExecutionID ids.IntegrationExecutionID `json:"execution_id"`
	Steps       []runtimeStep              `json:"steps"`
}

type runtimeConfig struct {
	Credentials      []runtimeCredential `json:"credentials"`
	Objects          map[string][]byte   `json:"objects"`
	Scripts          []runtimeScript     `json:"scripts"`
	ConnectorTimeout string              `json:"connector_timeout,omitempty"`
}

type Runtime struct {
	Broker      *Broker
	Contents    *ContentSource
	Definitions []integrationexecution.Definition
}

func LoadRuntime(filename string) (Runtime, error) {
	if filename == "" {
		return Runtime{}, errors.New("mock connector runtime file is required")
	}
	file, err := os.Open(filename)
	if err != nil {
		return Runtime{}, err
	}
	defer file.Close()
	return ParseRuntime(file)
}

func ParseRuntime(reader io.Reader) (Runtime, error) {
	if reader == nil {
		return Runtime{}, errors.New("mock connector runtime is required")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, maximumRuntimeConfigBytes+1))
	if err != nil {
		return Runtime{}, fmt.Errorf("read mock connector runtime: %w", err)
	}
	if len(raw) > maximumRuntimeConfigBytes {
		return Runtime{}, errors.New("mock connector runtime is too large")
	}
	defer wipeBytes(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config runtimeConfig
	if err := decoder.Decode(&config); err != nil {
		return Runtime{}, fmt.Errorf("decode mock connector runtime: %w", err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return Runtime{}, err
	}
	credentials := make([]Credential, 0, len(config.Credentials))
	for index := range config.Credentials {
		value := &config.Credentials[index]
		credentials = append(credentials, Credential{ID: value.ID, Generation: value.Generation, Material: value.MaterialBase64})
	}
	broker, err := NewBroker(credentials)
	for index := range config.Credentials {
		wipeBytes(config.Credentials[index].MaterialBase64)
	}
	if err != nil {
		return Runtime{}, err
	}
	contents, err := NewContentSource(config.Objects)
	for _, body := range config.Objects {
		wipeBytes(body)
	}
	if err != nil {
		return Runtime{}, err
	}
	scripts := make(map[ids.IntegrationExecutionID][]Step, len(config.Scripts))
	for _, script := range config.Scripts {
		if ids.Validate(string(script.ExecutionID)) != nil || len(script.Steps) == 0 || scripts[script.ExecutionID] != nil {
			return Runtime{}, errors.New("mock connector script is invalid")
		}
		steps := make([]Step, 0, len(script.Steps))
		for _, value := range script.Steps {
			var retryAfter time.Duration
			if value.RetryAfter != "" {
				retryAfter, err = time.ParseDuration(value.RetryAfter)
				if err != nil {
					return Runtime{}, errors.New("mock connector retry delay is invalid")
				}
			}
			result := integrationexecution.ConnectorResult{Outcome: value.Outcome, ErrorCode: value.ErrorCode}
			if !validRuntimeStep(value.Mode, result, retryAfter) {
				return Runtime{}, errors.New("mock connector step is invalid")
			}
			steps = append(steps, Step{Mode: value.Mode, Result: result, RetryAfter: retryAfter})
		}
		scripts[script.ExecutionID] = steps
	}
	if len(scripts) == 0 {
		return Runtime{}, errors.New("mock connector scripts are required")
	}
	timeout := 30 * time.Second
	if config.ConnectorTimeout != "" {
		timeout, err = time.ParseDuration(config.ConnectorTimeout)
		if err != nil {
			return Runtime{}, errors.New("mock connector timeout is invalid")
		}
	}
	if timeout < 100*time.Millisecond || timeout > integrationexecution.MaximumLease {
		return Runtime{}, errors.New("mock connector timeout is out of bounds")
	}
	connector := New(scripts)
	return Runtime{Broker: broker, Contents: contents, Definitions: []integrationexecution.Definition{
		{Capability: domain.CapabilityEmailSend, Timeout: timeout, Connector: connector},
		{Capability: domain.CapabilityWebPublish, Timeout: timeout, Connector: connector},
	}}, nil
}

func wipeBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("mock connector runtime contains trailing JSON")
		}
		return fmt.Errorf("decode mock connector runtime: %w", err)
	}
	return nil
}

func validRuntimeStep(mode domain.AttemptMode, result integrationexecution.ConnectorResult, retryAfter time.Duration) bool {
	if mode != domain.AttemptExecute && mode != domain.AttemptReconcile {
		return false
	}
	switch result.Outcome {
	case domain.AttemptSucceeded:
		return result.ErrorCode == "" && retryAfter == 0
	case domain.AttemptFailed, domain.AttemptUnknown:
		return validRuntimeErrorCode.MatchString(result.ErrorCode) && retryAfter == 0
	case domain.AttemptNotApplied:
		return mode == domain.AttemptReconcile && validRuntimeErrorCode.MatchString(result.ErrorCode) && retryAfter >= time.Second && retryAfter <= time.Hour
	default:
		return false
	}
}
