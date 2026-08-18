package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/restoregate"
)

func openRequiredRestoreGate(ctx context.Context, databaseURL string, target restoregate.Target, environmentPrefix string) (*restoregate.Gate, error) {
	checkpoint, err := restoreCheckpointEnv(environmentPrefix)
	if err != nil {
		return nil, err
	}
	checkContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return restoregate.Open(checkContext, databaseURL, target, checkpoint)
}

func restoreCheckpointEnv(prefix string) (restoregate.Checkpoint, error) {
	sequenceName := prefix + "ERASURE_CHECKPOINT_SEQUENCE"
	rootName := prefix + "ERASURE_CHECKPOINT_ROOT"
	sequenceValue, sequencePresent := os.LookupEnv(sequenceName)
	rootValue, rootPresent := os.LookupEnv(rootName)
	if os.Getenv("SPYGLASS_ENV") == "development" && !sequencePresent && !rootPresent {
		return restoregate.InitialCheckpoint(), nil
	}
	if !sequencePresent || sequenceValue == "" || !rootPresent || rootValue == "" {
		return restoregate.Checkpoint{}, fmt.Errorf("%s and %s are required", sequenceName, rootName)
	}
	sequence, err := strconv.ParseUint(sequenceValue, 10, 64)
	if err != nil {
		return restoregate.Checkpoint{}, fmt.Errorf("%s must be an unsigned integer", sequenceName)
	}
	root, err := hex.DecodeString(rootValue)
	if err != nil || len(root) != 32 {
		return restoregate.Checkpoint{}, fmt.Errorf("%s must be exactly 64 hexadecimal characters", rootName)
	}
	checkpoint, err := restoregate.NewCheckpoint(sequence, root)
	if err != nil {
		return restoregate.Checkpoint{}, fmt.Errorf("%s and %s do not form a valid checkpoint: %w", sequenceName, rootName, err)
	}
	return checkpoint, nil
}

func withRestoreGate(gates []*restoregate.Gate, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/health/live" {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		for _, gate := range gates {
			if err := gate.Ready(ctx); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"restore_replay_required"}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type restoreGatedWorker struct {
	worker runnableWorker
	gates  []*restoregate.Gate
}

func (w *restoreGatedWorker) Ready(ctx context.Context) error {
	for _, gate := range w.gates {
		if err := gate.Ready(ctx); err != nil {
			return err
		}
	}
	return w.worker.Ready(ctx)
}

func (w *restoreGatedWorker) Run(ctx context.Context) error {
	if err := w.Ready(ctx); err != nil {
		return err
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- w.worker.Run(runContext) }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			return err
		case <-ctx.Done():
			cancel()
			return <-result
		case <-ticker.C:
			for _, gate := range w.gates {
				checkContext, checkCancel := context.WithTimeout(ctx, 2*time.Second)
				err := gate.Ready(checkContext)
				checkCancel()
				if err != nil {
					cancel()
					workerErr := <-result
					if workerErr != nil && !errors.Is(workerErr, context.Canceled) {
						return fmt.Errorf("Account erasure restore gate failed (%v); worker shutdown also failed: %w", err, workerErr)
					}
					return err
				}
			}
		}
	}
}

func closeRestoreGates(gates ...*restoregate.Gate) {
	for _, gate := range gates {
		gate.Close()
	}
}
