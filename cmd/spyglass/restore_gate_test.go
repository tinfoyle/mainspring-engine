package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/platform/restoregate"
)

func TestRestoreCheckpointEnvironmentIsStrict(t *testing.T) {
	t.Setenv("SPYGLASS_ENV", "production")
	t.Setenv("TEST_ERASURE_CHECKPOINT_SEQUENCE", "0")
	t.Setenv("TEST_ERASURE_CHECKPOINT_ROOT", string(bytes.Repeat([]byte{'0'}, 64)))
	checkpoint, err := restoreCheckpointEnv("TEST_")
	if err != nil || checkpoint != restoregate.InitialCheckpoint() {
		t.Fatalf("checkpoint=%+v err=%v", checkpoint, err)
	}
	t.Setenv("TEST_ERASURE_CHECKPOINT_ROOT", "01")
	if _, err := restoreCheckpointEnv("TEST_"); err == nil {
		t.Fatal("short restore root was accepted")
	}
}

func TestRestoreGatePreservesLivenessAndFailsClosed(t *testing.T) {
	missing, _ := restoregate.New(nil, restoregate.Global, restoregate.InitialCheckpoint())
	// A nil gate represents a broken dependency and must fail every endpoint
	// except process liveness.
	handler := withRestoreGate([]*restoregate.Gate{missing}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	live := httptest.NewRecorder()
	handler.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if live.Code != http.StatusNoContent {
		t.Fatalf("liveness status=%d", live.Code)
	}
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if ready.Code != http.StatusServiceUnavailable || !bytes.Contains(ready.Body.Bytes(), []byte("restore_replay_required")) {
		t.Fatalf("readiness status=%d body=%s", ready.Code, ready.Body.String())
	}
	if !errors.Is((*restoregate.Gate)(nil).Ready(context.Background()), restoregate.ErrInvalidCheckpoint) {
		t.Fatal("nil gate did not fail closed")
	}
}
