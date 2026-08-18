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

func TestCommaSeparatedEnvironmentParser(t *testing.T) {
	t.Setenv("SPYGLASS_TEST_CIDRS", " 10.0.0.0/8, ,192.0.2.0/24 ")
	values := csvEnv("SPYGLASS_TEST_CIDRS")
	if len(values) != 2 || values[0] != "10.0.0.0/8" || values[1] != "192.0.2.0/24" {
		t.Fatalf("parsed values = %#v", values)
	}
}

func TestPositiveUnsignedEnvironmentParser(t *testing.T) {
	t.Setenv("SPYGLASS_TEST_VERSION", "42")
	if value, err := uint64Env("SPYGLASS_TEST_VERSION"); err != nil || value != 42 {
		t.Fatalf("version=%d err=%v", value, err)
	}
	t.Setenv("SPYGLASS_TEST_VERSION", "0")
	if _, err := uint64Env("SPYGLASS_TEST_VERSION"); err == nil {
		t.Fatal("expected zero version to fail closed")
	}
}

func TestCellRouteAndVerificationKeyParsers(t *testing.T) {
	t.Setenv("SPYGLASS_TEST_ROUTES", "cell-a=https://app-api-a.internal,cell-b=https://app-api-b.internal")
	routes, err := cellRoutesEnv("SPYGLASS_TEST_ROUTES")
	if err != nil || routes["cell-a"] != "https://app-api-a.internal" || len(routes) != 2 {
		t.Fatalf("routes=%v err=%v", routes, err)
	}
	key := bytes.Repeat([]byte{0x31}, 32)
	t.Setenv("SPYGLASS_TEST_ROUTE_KEYS", "current="+base64.StdEncoding.EncodeToString(key)+",previous="+base64.StdEncoding.EncodeToString(key))
	keys, err := routeVerifyKeysEnv("SPYGLASS_TEST_ROUTE_KEYS")
	if err != nil || !bytes.Equal(keys["current"], key) || len(keys) != 2 {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	for name, value := range map[string]string{"missing_equals": "cell-a", "duplicate": "cell-a=https://one,cell-a=https://two", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SPYGLASS_TEST_ROUTES", value)
			if _, err := cellRoutesEnv("SPYGLASS_TEST_ROUTES"); err == nil {
				t.Fatal("expected invalid route map")
			}
		})
	}
}
