package toolcontext

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

type receiptStore struct{ seen map[string]struct{} }

func (s *receiptStore) Consume(_ context.Context, claims Claims, _ time.Time) error {
	if _, exists := s.seen[claims.Authority.RequestID]; exists {
		return ErrReplay
	}
	s.seen[claims.Authority.RequestID] = struct{}{}
	return nil
}

var testAuthority = Authority{
	RequestID: "10000000-0000-4000-8000-000000000001", AccountID: ids.AccountID("20000000-0000-4000-8000-000000000002"),
	InvocationID: "30000000-0000-4000-8000-000000000003", PodUID: "40000000-0000-4000-8000-000000000004",
	OperationID: "50000000-0000-4000-8000-000000000005", Capability: "work.summary.read",
}

func TestToolContextBindsOneCapabilityDispatch(t *testing.T) {
	clock := &fixedClock{now: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)}
	key := []byte("0123456789abcdef0123456789abcdef")
	signer, _ := NewSigner("spyglass-runner-broker", "current", key, 10*time.Second, clock)
	verifier, _ := NewVerifier("spyglass-runner-broker", map[string][]byte{"current": key}, MaximumLifetime, time.Second, clock)
	binding, _ := routecontext.Bind("POST", "/internal/v1/tools:invoke", []byte(`{"input":{}}`))
	token, err := signer.Issue(testAuthority, binding)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(token, binding)
	if err != nil || claims.Authority != testAuthority || claims.KeyID != "current" || claims.ExpiresAt-claims.IssuedAt != 10 {
		t.Fatalf("unexpected verification: %#v %v", claims, err)
	}
	wrongBody, _ := routecontext.Bind("POST", "/internal/v1/tools:invoke", []byte(`{"input":{"scope":"all"}}`))
	if _, err := verifier.Verify(token, wrongBody); !errors.Is(err, ErrRequest) {
		t.Fatalf("expected body binding denial, got %v", err)
	}
	wrongMethod, _ := routecontext.Bind("PUT", "/internal/v1/tools:invoke", []byte(`{"input":{}}`))
	if _, err := verifier.Verify(token, wrongMethod); !errors.Is(err, ErrRequest) {
		t.Fatalf("expected method binding denial, got %v", err)
	}
}

func TestToolContextAcceptorConsumesRequestOnce(t *testing.T) {
	clock := &fixedClock{now: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)}
	key := []byte("0123456789abcdef0123456789abcdef")
	signer, _ := NewSigner("broker", "current", key, 10*time.Second, clock)
	verifier, _ := NewVerifier("broker", map[string][]byte{"current": key}, MaximumLifetime, 0, clock)
	acceptor, _ := NewAcceptor(verifier, &receiptStore{seen: map[string]struct{}{}}, clock)
	binding, _ := routecontext.Bind("POST", "/internal/v1/tools:invoke", []byte(`{}`))
	token, _ := signer.Issue(testAuthority, binding)
	if _, err := acceptor.Accept(context.Background(), token, binding); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptor.Accept(context.Background(), token, binding); !errors.Is(err, ErrReplay) {
		t.Fatalf("expected replay denial, got %v", err)
	}
}

func TestToolContextRejectsExpiryTamperingAndCredentialConfusion(t *testing.T) {
	clock := &fixedClock{now: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)}
	key := []byte("0123456789abcdef0123456789abcdef")
	signer, _ := NewSigner("broker", "current", key, time.Second, clock)
	verifier, _ := NewVerifier("broker", map[string][]byte{"current": key}, MaximumLifetime, 0, clock)
	binding, _ := routecontext.Bind("POST", "/internal/v1/tools:invoke", []byte(`{}`))
	token, _ := signer.Issue(testAuthority, binding)

	clock.now = clock.now.Add(time.Second)
	if _, err := verifier.Verify(token, binding); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected expiry denial, got %v", err)
	}
	clock.now = clock.now.Add(-time.Second)
	parts := strings.Split(token, ".")
	first := "A"
	if parts[2][0] == 'A' {
		first = "B"
	}
	tampered := parts[0] + "." + parts[1] + "." + first + parts[2][1:]
	if _, err := verifier.Verify(tampered, binding); !errors.Is(err, ErrSignature) {
		t.Fatalf("expected signature denial, got %v", err)
	}
	if _, err := verifier.Verify("not.a.route-token", binding); err == nil {
		t.Fatal("accepted another credential family")
	}
}

func TestToolContextRequiresExactBoundIdentifiers(t *testing.T) {
	clock := &fixedClock{now: time.Now()}
	signer, _ := NewSigner("broker", "current", []byte("0123456789abcdef0123456789abcdef"), time.Second, clock)
	binding, _ := routecontext.Bind("POST", "/internal/v1/tools:invoke", []byte(`{}`))
	for name, mutate := range map[string]func(*Authority){
		"request":    func(value *Authority) { value.RequestID = "request" },
		"account":    func(value *Authority) { value.AccountID = "account" },
		"invocation": func(value *Authority) { value.InvocationID = "invocation" },
		"pod":        func(value *Authority) { value.PodUID = "pod" },
		"operation":  func(value *Authority) { value.OperationID = "operation" },
		"capability": func(value *Authority) { value.Capability = "Work Summary" },
	} {
		t.Run(name, func(t *testing.T) {
			value := testAuthority
			mutate(&value)
			if _, err := signer.Issue(value, binding); !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected invalid authority, got %v", err)
			}
		})
	}
}
