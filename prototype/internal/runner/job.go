package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/tinfoyle/mainspring-engine/internal/agent"
)

func RunJob(ctx context.Context, inputPath, outputPath, codexBinary string) error {
	body, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("read runner input: %w", err)
	}
	var request agent.RemoteInvocation
	if err := json.Unmarshal(body, &request); err != nil {
		return writeJobResult(outputPath, agent.RemoteResult{Error: "decode runner input: " + err.Error(), Category: agent.FailureInvalidOutput})
	}
	provider, err := agent.NewProvider(request.Provider, codexBinary)
	if err != nil {
		return writeJobResult(outputPath, agent.RemoteResult{Error: err.Error(), Category: agent.FailureAuthentication})
	}
	result, invokeErr := provider.Invoke(ctx, request.Invocation)
	if invokeErr != nil {
		category, retryable := agent.Failure(invokeErr)
		return writeJobResult(outputPath, agent.RemoteResult{Error: invokeErr.Error(), Category: category, Retryable: retryable})
	}
	return writeJobResult(outputPath, agent.RemoteResult{Result: result})
}

func writeJobResult(path string, result agent.RemoteResult) error {
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write runner result: %w", err)
	}
	return nil
}
