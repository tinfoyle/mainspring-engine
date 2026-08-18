package routecanary

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	admissiontransport "github.com/tinfoyle/spyglass-engine/internal/transport/admissionapi"
	"github.com/tinfoyle/spyglass-engine/internal/transport/cellapi"
)

var (
	canaryNow       = time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	canaryCandidate = []byte("abcdef0123456789abcdef0123456789")
	canaryPrevious  = []byte("0123456789abcdef0123456789abcdef")
)

func TestProbeUsesCandidateKeyAgainstProtectedCellPath(t *testing.T) {
	receipts := &capturingReceipts{}
	transport := canaryTransport(t, map[string][]byte{"previous": canaryPrevious, "candidate": canaryCandidate}, receipts)
	result, err := Probe(context.Background(), canaryConfig(transport, canaryCandidate, "candidate"))
	if err != nil {
		t.Fatal(err)
	}
	if result.CellID != "cell-us-east-01" || result.KeyID != "candidate" || result.PlacementGeneration != 7 {
		t.Fatalf("result=%+v", result)
	}
	if receipts.claims.Authority.ActorKind != "workload" || receipts.claims.Authority.ActorID != ActorID || receipts.claims.Authority.AccountID != "10000000-0000-4000-8000-000000000001" {
		t.Fatalf("receipt claims=%+v", receipts.claims.Authority)
	}
}

func TestProbeRejectsCandidateMissingFromCellKeyring(t *testing.T) {
	transport := canaryTransport(t, map[string][]byte{"previous": canaryPrevious}, &capturingReceipts{})
	_, err := Probe(context.Background(), canaryConfig(transport, canaryCandidate, "candidate"))
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("error=%v", err)
	}
}

func TestProbeAdmissionVerifiesCandidateWithoutMutatingUsage(t *testing.T) {
	usage := &unusedUsage{}
	transport := admissionCanaryTransport(t, map[string][]byte{"previous": canaryPrevious, "candidate": canaryCandidate}, usage)
	result, err := ProbeAdmission(context.Background(), canaryConfig(transport, canaryCandidate, "candidate"))
	if err != nil {
		t.Fatal(err)
	}
	if result.KeyID != "candidate" || usage.calls != 0 {
		t.Fatalf("result=%+v usage_calls=%d", result, usage.calls)
	}
}

func TestProbeAdmissionRejectsUnknownCandidateKey(t *testing.T) {
	transport := admissionCanaryTransport(t, map[string][]byte{"previous": canaryPrevious}, &unusedUsage{})
	if _, err := ProbeAdmission(context.Background(), canaryConfig(transport, canaryCandidate, "candidate")); !errors.Is(err, ErrRejected) {
		t.Fatalf("error=%v", err)
	}
}

func TestProbeConfigurationFailsClosed(t *testing.T) {
	config := canaryConfig(roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") }), canaryCandidate, "candidate")
	config.Origin = "http://cell.test"
	if _, err := Probe(context.Background(), config); err == nil {
		t.Fatal("plain HTTP canary origin was accepted")
	}
	config = canaryConfig(nil, canaryCandidate, "candidate")
	if _, err := Probe(context.Background(), config); err == nil {
		t.Fatal("canary without explicit workload transport was accepted")
	}
	config = canaryConfig(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network failure for 10000000-0000-4000-8000-000000000001")
	}), canaryCandidate, "candidate")
	if _, err := Probe(context.Background(), config); err == nil || strings.Contains(err.Error(), string(config.AccountID)) {
		t.Fatalf("transport error leaked canary Account: %v", err)
	}
}

func canaryConfig(transport http.RoundTripper, key []byte, keyID string) Config {
	return Config{
		Origin: "https://cell.test", CellID: "cell-us-east-01",
		AccountID: "10000000-0000-4000-8000-000000000001", PlacementGeneration: 7, EntitlementVersion: 4,
		Issuer: "spyglass-app-router", KeyID: keyID, SigningKey: key, Timeout: 2 * time.Second,
		Transport: transport, Clock: fixedClock{now: canaryNow}, IDs: fixedIDs{"30000000-0000-4000-8000-000000000003"},
	}
}

func canaryTransport(t *testing.T, keys map[string][]byte, receipts *capturingReceipts) http.RoundTripper {
	t.Helper()
	verifier, err := routecontext.NewVerifier("spyglass-app-router", routecontext.Audience("cell-us-east-01"), keys, routecontext.MaximumLifetime, 0, fixedClock{now: canaryNow})
	if err != nil {
		t.Fatal(err)
	}
	acceptor, err := routecontext.NewAcceptor(verifier, receipts, fixedClock{now: canaryNow})
	if err != nil {
		t.Fatal(err)
	}
	server, err := cellapi.New(acceptor, slog.New(slog.NewTextHandler(io.Discard, nil)), cellapi.DefaultMaxBody)
	if err != nil {
		t.Fatal(err)
	}
	return roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response.Result(), nil
	})
}

func admissionCanaryTransport(t *testing.T, keys map[string][]byte, usage *unusedUsage) http.RoundTripper {
	t.Helper()
	verifier, err := routecontext.NewVerifier("spyglass-app-router", routecontext.Audience("cell-us-east-01"), keys, routecontext.MaximumLifetime, 0, fixedClock{now: canaryNow})
	if err != nil {
		t.Fatal(err)
	}
	server, err := admissiontransport.New(usage, map[ids.CellID]admissiontransport.Verifier{"cell-us-east-01": verifier}, slog.New(slog.NewTextHandler(io.Discard, nil)), admissiontransport.DefaultMaxBody)
	if err != nil {
		t.Fatal(err)
	}
	return roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response.Result(), nil
	})
}

type capturingReceipts struct{ claims routecontext.Claims }

func (store *capturingReceipts) Consume(_ context.Context, claims routecontext.Claims, _ time.Time) error {
	store.claims = claims
	return nil
}

type unusedUsage struct{ calls int }

func (usage *unusedUsage) Reserve(context.Context, usageadmission.ReserveCommand) (usageadmission.Reservation, error) {
	usage.calls++
	return usageadmission.Reservation{}, errors.New("usage must not be called by a route canary")
}

func (usage *unusedUsage) Release(context.Context, usageadmission.ReleaseCommand) (usageadmission.Reservation, error) {
	usage.calls++
	return usageadmission.Reservation{}, errors.New("usage must not be called by a route canary")
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type fixedIDs struct{ value string }

func (generator fixedIDs) New() string { return generator.value }

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
