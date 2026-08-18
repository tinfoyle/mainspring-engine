package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type readinessStub struct{ err error }

func (r readinessStub) Ready(context.Context) error { return r.err }

type statusStub struct{ readinessStub }

func (statusStub) Status(context.Context) (any, error) {
	return map[string]any{"pending": 2, "dead_letter": 1}, nil
}

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

func TestWorkerHealthExposesOptionalOperationalStatus(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health/status", nil)
	response := httptest.NewRecorder()
	workerHealth(statusStub{}).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"pending":2`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"dead_letter":1`)) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	workerHealth(readinessStub{}).ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unsupported status=%d", response.Code)
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

func TestVersionedEncryptionKeyParserRequiresExactActiveKey(t *testing.T) {
	oldKey := bytes.Repeat([]byte{0x31}, 32)
	activeKey := bytes.Repeat([]byte{0x42}, 32)
	t.Setenv("SPYGLASS_TEST_KEYS", "1="+base64.StdEncoding.EncodeToString(oldKey)+",2="+base64.StdEncoding.EncodeToString(activeKey))
	t.Setenv("SPYGLASS_TEST_ACTIVE", "2")
	keys, active, err := versionedEncryptionKeysEnv("SPYGLASS_TEST_KEYS", "SPYGLASS_TEST_ACTIVE")
	if err != nil || active != 2 || !bytes.Equal(keys[1], oldKey) || !bytes.Equal(keys[2], activeKey) {
		t.Fatalf("keys=%v active=%d err=%v", keys, active, err)
	}
	for name, value := range map[string][2]string{
		"missing active":    {"1=" + base64.StdEncoding.EncodeToString(oldKey), "2"},
		"bad version":       {"old=" + base64.StdEncoding.EncodeToString(oldKey), "1"},
		"duplicate version": {"1=" + base64.StdEncoding.EncodeToString(oldKey) + ",01=" + base64.StdEncoding.EncodeToString(activeKey), "1"},
		"short key":         {"1=" + base64.StdEncoding.EncodeToString(oldKey[:31]), "1"},
		"zero active":       {"1=" + base64.StdEncoding.EncodeToString(oldKey), "0"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SPYGLASS_TEST_KEYS", value[0])
			t.Setenv("SPYGLASS_TEST_ACTIVE", value[1])
			if _, _, err := versionedEncryptionKeysEnv("SPYGLASS_TEST_KEYS", "SPYGLASS_TEST_ACTIVE"); err == nil {
				t.Fatal("invalid encryption keyring was accepted")
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

func TestVerificationKeyParser(t *testing.T) {
	key := bytes.Repeat([]byte{0x31}, 32)
	t.Setenv("SPYGLASS_TEST_ROUTE_KEYS", "current="+base64.StdEncoding.EncodeToString(key)+",previous="+base64.StdEncoding.EncodeToString(key))
	keys, err := routeVerifyKeysEnv("SPYGLASS_TEST_ROUTE_KEYS")
	if err != nil || !bytes.Equal(keys["current"], key) || len(keys) != 2 {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	for name, value := range map[string]string{"missing_equals": "current", "duplicate": "current=one,current=two", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SPYGLASS_TEST_ROUTE_KEYS", value)
			if _, err := routeVerifyKeysEnv("SPYGLASS_TEST_ROUTE_KEYS"); err == nil {
				t.Fatal("expected invalid verification key map")
			}
		})
	}
}

func TestRouteCanaryRejectsUnknownTargetBeforeLoadingSecrets(t *testing.T) {
	t.Setenv("SPYGLASS_ROUTE_CANARY_TARGET", "everything")
	err := runRouteCanary(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || err.Error() != "SPYGLASS_ROUTE_CANARY_TARGET must be cell or admission" {
		t.Fatalf("error=%v", err)
	}
}
