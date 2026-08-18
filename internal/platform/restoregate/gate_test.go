package restoregate_test

import (
	"errors"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/platform/restoregate"
)

func TestCheckpointRequiresCanonicalOriginAndDigest(t *testing.T) {
	if checkpoint, err := restoregate.NewCheckpoint(0, make([]byte, 32)); err != nil || checkpoint != restoregate.InitialCheckpoint() {
		t.Fatalf("initial checkpoint=%+v err=%v", checkpoint, err)
	}
	nonzero := make([]byte, 32)
	nonzero[0] = 1
	if _, err := restoregate.NewCheckpoint(0, nonzero); !errors.Is(err, restoregate.ErrInvalidCheckpoint) {
		t.Fatalf("noncanonical origin=%v", err)
	}
	if _, err := restoregate.NewCheckpoint(1, make([]byte, 31)); !errors.Is(err, restoregate.ErrInvalidCheckpoint) {
		t.Fatalf("short digest=%v", err)
	}
	if _, err := restoregate.NewCheckpoint(1, make([]byte, 32)); !errors.Is(err, restoregate.ErrInvalidCheckpoint) {
		t.Fatalf("non-origin zero digest=%v", err)
	}
}
