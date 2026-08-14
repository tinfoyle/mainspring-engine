package agent

import (
	"errors"
	"testing"
)

func TestClassifyCodexErrorRejectsUnsupportedModelWithoutRetry(t *testing.T) {
	err := classifyCodexError(errors.New("exit status 1"), `{"error":{"type":"invalid_request_error","message":"The 'custom-smoke-model' model is not supported when using Codex with a ChatGPT account."}}`)

	category, retryable := Failure(err)
	if category != FailureInvalidRequest {
		t.Fatalf("category = %q, want %q", category, FailureInvalidRequest)
	}
	if retryable {
		t.Fatal("retryable = true, want false")
	}
}
