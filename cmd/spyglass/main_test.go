package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type readinessStub struct{ err error }

func (r readinessStub) Ready(context.Context) error { return r.err }

func TestWorkerHealthReflectsDependencyReadiness(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	response := httptest.NewRecorder()
	workerHealth(readinessStub{}).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("ready status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	workerHealth(readinessStub{err: errors.New("database unavailable")}).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unready status=%d", response.Code)
	}
}

func TestEnvironmentParsersFailClosed(t *testing.T) {
	t.Setenv("SPYGLASS_TEST_REQUIRED", "")
	if _, err := requiredEnv("SPYGLASS_TEST_REQUIRED"); err == nil {
		t.Fatal("expected missing environment failure")
	}
	t.Setenv("SPYGLASS_TEST_INT", "0")
	if _, err := int32Env("SPYGLASS_TEST_INT", 2); err == nil {
		t.Fatal("expected invalid integer failure")
	}
	t.Setenv("SPYGLASS_TEST_DURATION", "later")
	if _, err := durationEnv("SPYGLASS_TEST_DURATION", time.Second); err == nil {
		t.Fatal("expected invalid duration failure")
	}
}

func TestNotificationEncryptionKeyParser(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	t.Setenv("SPYGLASS_TEST_KEY", base64.StdEncoding.EncodeToString(key))
	parsed, err := base64KeyEnv("SPYGLASS_TEST_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(parsed, key) {
		t.Fatal("parsed notification key differs from configured key")
	}

	for name, value := range map[string]string{
		"not_base64":   "%%%",
		"short_key":    base64.StdEncoding.EncodeToString(key[:31]),
		"url_encoding": base64.RawURLEncoding.EncodeToString(key),
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SPYGLASS_TEST_KEY", value)
			if _, err := base64KeyEnv("SPYGLASS_TEST_KEY"); err == nil {
				t.Fatal("expected invalid notification key to fail closed")
			}
		})
	}
}
