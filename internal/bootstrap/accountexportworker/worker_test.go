package accountexportworker

import (
	"context"
	"log/slog"
	"testing"
)

func TestWorkersRejectIncompleteIdentity(t *testing.T) {
	logger := slog.Default()
	if _, err := NewBuild(context.Background(), BuildConfig{}, logger); err == nil {
		t.Fatal("build worker accepted incomplete identity")
	}
	if _, err := NewExpiry(context.Background(), ExpiryConfig{}, logger); err == nil {
		t.Fatal("expiry worker accepted incomplete identity")
	}
}
