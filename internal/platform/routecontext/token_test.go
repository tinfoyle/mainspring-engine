package routecontext

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	keyOne   = []byte("0123456789abcdef0123456789abcdef")
	keyTwo   = []byte("abcdef0123456789abcdef0123456789")
	baseTime = time.Date(2026, 8, 18, 3, 0, 0, 0, time.UTC)
)

func TestRoundTripAndRequestBinding(t *testing.T) {
	clock := &testClock{now: baseTime}
	signer, _ := NewSigner("spyglass-app-router", "current", keyOne, 20*time.Second, clock)
	verifier, _ := NewVerifier("spyglass-app-router", Audience("cell-us-east-01"), map[string][]byte{"current": keyOne}, MaximumLifetime, DefaultClockSkew, clock)
	binding, _ := Bind("post", "/api/v1/accounts/10000000-0000-4000-8000-000000000001/work-items?view=queue", []byte(`{"title":"Close books"}`))
	authority := validTestAuthority()
	token, err := signer.Issue(Audience(authority.CellID), authority, binding)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(token, binding)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Authority.AccountID != authority.AccountID || claims.Authority.PackageAccess == nil || claims.Authority.PackageAccess.Code != "work" || claims.ExpiresAt-claims.IssuedAt != 20 {
		t.Fatalf("claims = %+v", claims)
	}
	for name, changed := range map[string]Binding{
		"method": mustBind(t, "GET", binding.Target, []byte(`{"title":"Close books"}`)),
		"target": mustBind(t, "POST", strings.Replace(binding.Target, "work-items", "finance", 1), []byte(`{"title":"Close books"}`)),
		"body":   mustBind(t, "POST", binding.Target, []byte(`{"title":"Changed"}`)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.Verify(token, changed); !errors.Is(err, ErrRequest) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestRequestBindingProtectsCommandHeaders(t *testing.T) {
	request, _ := http.NewRequest("POST", "https://cell.test/api/v1/accounts/10000000-0000-4000-8000-000000000001/work-items", strings.NewReader(`{"title":"Close books"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "50000000-0000-4000-8000-000000000005")
	binding, err := BindRequest(request, []byte(`{"title":"Close books"}`))
	if err != nil || binding.HeadersSHA256 == "" {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
	request.Header.Set("Idempotency-Key", "60000000-0000-4000-8000-000000000006")
	changed, _ := BindRequest(request, []byte(`{"title":"Close books"}`))
	if changed == binding {
		t.Fatal("changed command header retained the same binding")
	}
}

func TestExpirySignatureAndKeyRotation(t *testing.T) {
	clock := &testClock{now: baseTime}
	signer, _ := NewSigner("router", "old", keyOne, 10*time.Second, clock)
	binding := mustBind(t, "GET", "/api/v1/accounts/10000000-0000-4000-8000-000000000001/context", nil)
	token, _ := signer.Issue(Audience("cell-us-east-01"), validTestAuthority(), binding)
	verifier, _ := NewVerifier("router", Audience("cell-us-east-01"), map[string][]byte{"old": keyOne, "new": keyTwo}, MaximumLifetime, 0, clock)
	if _, err := verifier.Verify(token, binding); err != nil {
		t.Fatal(err)
	}
	clock.now = baseTime.Add(10 * time.Second)
	if _, err := verifier.Verify(token, binding); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired error = %v", err)
	}
	clock.now = baseTime
	tampered := token[:len(token)-1] + "A"
	if _, err := verifier.Verify(tampered, binding); !errors.Is(err, ErrSignature) {
		t.Fatalf("signature error = %v", err)
	}
	unknown, _ := NewVerifier("router", Audience("cell-us-east-01"), map[string][]byte{"new": keyTwo}, MaximumLifetime, 0, clock)
	if _, err := unknown.Verify(token, binding); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown key error = %v", err)
	}
}

func TestInvalidAuthorityAndConfigurationFailClosed(t *testing.T) {
	clock := &testClock{now: baseTime}
	if _, err := NewSigner("router", "key", keyOne[:16], 10*time.Second, clock); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short key = %v", err)
	}
	if _, err := NewSigner("router", "key", keyOne, MaximumLifetime+time.Second, clock); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long lifetime = %v", err)
	}
	signer, _ := NewSigner("router", "key", keyOne, 10*time.Second, clock)
	binding := mustBind(t, "GET", "/context", nil)
	base := validTestAuthority()
	if _, err := signer.Issue(Audience("another-cell"), base, binding); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched cell audience = %v", err)
	}
	cases := map[string]func(*Authority){
		"account":    func(a *Authority) { a.AccountID = "wrong" },
		"request":    func(a *Authority) { a.RequestID = "wrong" },
		"generation": func(a *Authority) { a.PlacementGeneration = 0 },
		"role":       func(a *Authority) { a.Role = "superuser" },
		"package":    func(a *Authority) { a.PackageAccess.Code = "Work!" },
		"limit":      func(a *Authority) { a.PackageAccess.Limits["active_items"] = -1 },
		"operation":  func(a *Authority) { a.OperationID = "wrong" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			value := base
			packageCopy := *base.PackageAccess
			packageCopy.Limits = map[string]int64{"active_items": 100}
			value.PackageAccess = &packageCopy
			mutate(&value)
			if _, err := signer.Issue(Audience(value.CellID), value, binding); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func validTestAuthority() Authority {
	return Authority{RequestID: "30000000-0000-4000-8000-000000000003", AccountID: ids.AccountID("10000000-0000-4000-8000-000000000001"), ActorKind: "user", ActorID: "20000000-0000-4000-8000-000000000002", Role: "owner", CellID: "cell-us-east-01", PlacementGeneration: 7, EntitlementVersion: 4, PackageAccess: &PackageAccess{Code: "work", Version: 1, Mode: "enabled", Limits: map[string]int64{"active_items": 100}, LimitPolicies: map[string]LimitPolicy{"active_items": {Kind: "capacity", Combine: "maximum"}}}}
}

func mustBind(t *testing.T, method, target string, body []byte) Binding {
	t.Helper()
	value, err := Bind(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }
