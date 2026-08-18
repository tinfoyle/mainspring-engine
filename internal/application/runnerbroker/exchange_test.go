package runnerbroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type exchangeClock struct{ now time.Time }

func (c exchangeClock) Now() time.Time { return c.now }

type exchangeVerifier struct {
	identity Identity
	err      error
	calls    int
}

func (v *exchangeVerifier) Verify(_ context.Context, token, invocationID string) (Identity, error) {
	v.calls++
	if v.err != nil {
		return Identity{}, v.err
	}
	if token != "bound-token" || invocationID != v.identity.InvocationID {
		return Identity{}, ErrIdentityDenied
	}
	return v.identity, nil
}

type exchangeRepository struct {
	provisioned StoredRequest
	claimed     StoredRequest
	submitted   StoredResult
	claimCalls  int
	created     bool
	err         error
}

func (r *exchangeRepository) Provision(_ context.Context, _ runnercontrol.Invocation, request StoredRequest) (bool, error) {
	r.provisioned = request
	return r.created, r.err
}
func (r *exchangeRepository) Claim(_ context.Context, _ Identity, _ time.Time) (StoredRequest, error) {
	r.claimCalls++
	return r.claimed, r.err
}
func (r *exchangeRepository) Submit(_ context.Context, _ Identity, result StoredResult, _ time.Time) (bool, error) {
	r.submitted = result
	return r.created, r.err
}

func testExchange(t *testing.T) (*Service, *exchangeRepository, *exchangeVerifier, *Cipher, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cipher, err := NewCipher(map[int][]byte{1: bytes.Repeat([]byte{0x11}, 32), 2: bytes.Repeat([]byte{0x22}, 32)}, 2)
	if err != nil {
		t.Fatal(err)
	}
	repository := &exchangeRepository{created: true}
	verifier := &exchangeVerifier{identity: Identity{
		InvocationID: "11000000-0000-4000-8000-000000000001", Profile: "agent-small",
		JobName: "spyglass-runner-11000000", JobUID: "21000000-0000-4000-8000-000000000001",
		PodName: "runner-pod", PodUID: "31000000-0000-4000-8000-000000000001",
	}}
	service, err := NewService(repository, verifier, cipher, exchangeClock{now})
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, verifier, cipher, now
}

func TestExchangeProvisionEncryptsCanonicalRequest(t *testing.T) {
	service, repository, _, cipher, now := testExchange(t)
	invocation := runnercontrol.Invocation{ID: "11000000-0000-4000-8000-000000000001", AccountID: ids.AccountID("41000000-0000-4000-8000-000000000001"), Profile: "agent-small", QueuedAt: now}
	created, err := service.Provision(context.Background(), ProvisionCommand{Invocation: invocation, Request: Request{
		SchemaVersion: 1, Kind: "agent.execute", Input: json.RawMessage(`{"prompt":"private ocean"}`),
		Capabilities: []string{"work:read", "agents:run"}, ExpiresAt: now.Add(time.Hour),
	}})
	if err != nil || !created {
		t.Fatalf("provision created=%v err=%v", created, err)
	}
	stored := repository.provisioned
	if stored.KeyVersion != 2 || stored.InvocationID != invocation.ID || stored.AccountID != invocation.AccountID || bytes.Contains(stored.Ciphertext, []byte("private ocean")) {
		t.Fatalf("unsafe or incorrect stored request: %+v", stored)
	}
	raw, err := cipher.open(stored.Ciphertext, stored.Nonce, stored.KeyVersion, requestAAD(invocation.ID, invocation.AccountID, invocation.Profile))
	if err != nil || !bytes.Contains(raw, []byte(`"capabilities":["agents:run","work:read"]`)) || stored.Digest != sha256Bytes(raw) {
		t.Fatalf("canonical encrypted request=%s digest=%x err=%v", raw, stored.Digest, err)
	}
}

func TestExchangeFetchBindsVerifiedIdentityAndSupportsKeyRotation(t *testing.T) {
	service, repository, verifier, _, now := testExchange(t)
	accountID := ids.AccountID("41000000-0000-4000-8000-000000000001")
	request := Request{SchemaVersion: 1, Kind: "work.execute", Input: json.RawMessage(`{"work_id":"abc"}`), Capabilities: []string{"work:read"}, ExpiresAt: now.Add(time.Hour)}
	canonical, raw, err := canonicalRequest(request, now, now)
	if err != nil {
		t.Fatal(err)
	}
	oldCipher, _ := NewCipher(map[int][]byte{1: bytes.Repeat([]byte{0x11}, 32)}, 1)
	ciphertext, nonce, version, _ := oldCipher.seal(raw, requestAAD(verifier.identity.InvocationID, accountID, verifier.identity.Profile))
	repository.claimed = StoredRequest{InvocationID: verifier.identity.InvocationID, AccountID: accountID, Profile: verifier.identity.Profile, Ciphertext: ciphertext, Nonce: nonce, KeyVersion: version, Digest: sha256Bytes(raw), CreatedAt: now, ExpiresAt: canonical.ExpiresAt}
	fetched, err := service.Fetch(context.Background(), "bound-token", verifier.identity.InvocationID)
	if err != nil || fetched.Kind != request.Kind || verifier.calls != 1 || repository.claimCalls != 1 {
		t.Fatalf("fetch=%+v verifier=%d claims=%d err=%v", fetched, verifier.calls, repository.claimCalls, err)
	}
	grant, err := service.AuthorizeCapability(context.Background(), "bound-token", verifier.identity.InvocationID, "work:read")
	if err != nil || grant.AccountID != accountID || grant.Identity.PodUID != verifier.identity.PodUID || grant.Capability != "work:read" {
		t.Fatalf("capability grant=%+v err=%v", grant, err)
	}
	if _, err := service.AuthorizeCapability(context.Background(), "bound-token", verifier.identity.InvocationID, "email:send"); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("ungranted capability=%v", err)
	}
	repository.claimed.Ciphertext[0] ^= 0xff
	if _, err := service.Fetch(context.Background(), "bound-token", verifier.identity.InvocationID); !errors.Is(err, ErrExchangeConflict) {
		t.Fatalf("tampered request=%v", err)
	}
}

func TestExchangeFetchFailsClosedBeforeRepositoryAndOnExpiry(t *testing.T) {
	service, repository, verifier, cipher, now := testExchange(t)
	verifier.err = ErrIdentityDenied
	if _, err := service.Fetch(context.Background(), "copied-token", verifier.identity.InvocationID); !errors.Is(err, ErrIdentityDenied) || repository.claimCalls != 0 {
		t.Fatalf("identity denial err=%v claims=%d", err, repository.claimCalls)
	}
	verifier.err = nil
	accountID := ids.AccountID("41000000-0000-4000-8000-000000000001")
	request := Request{SchemaVersion: 1, Kind: "agent.execute", Input: json.RawMessage(`{}`), ExpiresAt: now.Add(-time.Second)}
	_, raw, _ := canonicalRequest(request, now.Add(-time.Hour), time.Time{})
	ciphertext, nonce, version, _ := cipher.seal(raw, requestAAD(verifier.identity.InvocationID, accountID, verifier.identity.Profile))
	repository.claimed = StoredRequest{InvocationID: verifier.identity.InvocationID, AccountID: accountID, Profile: verifier.identity.Profile, Ciphertext: ciphertext, Nonce: nonce, KeyVersion: version, Digest: sha256Bytes(raw), CreatedAt: now.Add(-time.Hour), ExpiresAt: request.ExpiresAt}
	if _, err := service.Fetch(context.Background(), "bound-token", verifier.identity.InvocationID); !errors.Is(err, ErrExchangeExpired) {
		t.Fatalf("expired fetch=%v", err)
	}
}

func TestExchangeSubmitEncryptsOnePodBoundResult(t *testing.T) {
	service, repository, verifier, cipher, now := testExchange(t)
	created, err := service.Submit(context.Background(), "bound-token", verifier.identity.InvocationID, Result{
		SchemaVersion: 1, Outcome: "execution_failed", Output: json.RawMessage(`{"retryable":false}`), ErrorCode: "provider_denied",
	})
	if err != nil || !created {
		t.Fatalf("submit created=%v err=%v", created, err)
	}
	stored := repository.submitted
	if stored.PodUID != verifier.identity.PodUID || stored.SubmittedAt != now || bytes.Contains(stored.Ciphertext, []byte("retryable")) {
		t.Fatalf("unsafe or incorrect stored result: %+v", stored)
	}
	raw, err := cipher.open(stored.Ciphertext, stored.Nonce, stored.KeyVersion, resultAAD(verifier.identity.InvocationID, verifier.identity.PodUID))
	if err != nil || stored.Digest != sha256Bytes(raw) {
		t.Fatalf("decrypt result=%s err=%v", raw, err)
	}
	if _, err := service.Submit(context.Background(), "bound-token", verifier.identity.InvocationID, Result{SchemaVersion: 1, Outcome: "completed", Output: json.RawMessage(`{}`), ErrorCode: "bad"}); !errors.Is(err, ErrInvalidExchange) {
		t.Fatalf("completed result with error code=%v", err)
	}
}

func TestExchangeRejectsInvalidAndOversizedRequests(t *testing.T) {
	service, _, _, _, now := testExchange(t)
	base := ProvisionCommand{Invocation: runnercontrol.Invocation{ID: "11000000-0000-4000-8000-000000000001", AccountID: ids.AccountID("41000000-0000-4000-8000-000000000001"), Profile: "agent-small", QueuedAt: now}, Request: Request{SchemaVersion: 1, Kind: "agent.execute", Input: json.RawMessage(`{}`), ExpiresAt: now.Add(time.Hour)}}
	tests := []Request{
		{SchemaVersion: 1, Kind: "agent.execute", Input: json.RawMessage(`[]`), ExpiresAt: now.Add(time.Hour)},
		{SchemaVersion: 1, Kind: "agent.execute", Input: json.RawMessage(`{}`), Capabilities: []string{"work:read", "work:read"}, ExpiresAt: now.Add(time.Hour)},
		{SchemaVersion: 1, Kind: "agent.execute", Input: json.RawMessage(`{}`), ExpiresAt: now.Add(25 * time.Hour)},
		{SchemaVersion: 1, Kind: "agent.execute", Input: append(json.RawMessage(`{"v":"`), append(bytes.Repeat([]byte{'x'}, MaximumInputBytes), []byte(`"}`)...)...), ExpiresAt: now.Add(time.Hour)},
	}
	for index, request := range tests {
		command := base
		command.Request = request
		if _, err := service.Provision(context.Background(), command); !errors.Is(err, ErrInvalidExchange) {
			t.Errorf("case %d err=%v", index, err)
		}
	}
}

func sha256Bytes(raw []byte) (digest [32]byte) {
	return sha256.Sum256(raw)
}
