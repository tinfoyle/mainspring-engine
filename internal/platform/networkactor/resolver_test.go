package networkactor_test

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/platform/networkactor"
)

func TestResolverTrustsForwardingOnlyFromConfiguredPeers(t *testing.T) {
	resolver, err := networkactor.New(bytes.Repeat([]byte{0x41}, 32), []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	direct := httptest.NewRequest("GET", "https://spyglass.test", nil)
	direct.RemoteAddr = "203.0.113.8:443"
	direct.Header.Set("X-Forwarded-For", "198.51.100.9")
	directActor, _ := resolver.Actor(direct)

	spoofVariant := direct.Clone(direct.Context())
	spoofVariant.Header.Set("X-Forwarded-For", "192.0.2.44")
	spoofActor, _ := resolver.Actor(spoofVariant)
	if directActor != spoofActor {
		t.Fatal("untrusted peer changed actor through a forwarding header")
	}

	proxied := httptest.NewRequest("GET", "https://spyglass.test", nil)
	proxied.RemoteAddr = "10.0.0.7:8443"
	proxied.Header.Set("X-Forwarded-For", "192.0.2.4, 10.1.0.8")
	proxiedActor, _ := resolver.Actor(proxied)
	proxied.Header.Set("X-Forwarded-For", "192.0.2.5, 10.1.0.8")
	otherActor, _ := resolver.Actor(proxied)
	if proxiedActor == otherActor || proxiedActor == directActor {
		t.Fatal("trusted forwarding chain did not identify the first untrusted hop")
	}
}

func TestResolverRejectsInvalidConfigurationAndMalformedTrustedHeader(t *testing.T) {
	if _, err := networkactor.New(make([]byte, 31), nil); err == nil {
		t.Fatal("expected short key rejection")
	}
	if _, err := networkactor.New(make([]byte, 32), []string{"not-a-network"}); err == nil {
		t.Fatal("expected invalid trusted proxy rejection")
	}
	resolver, _ := networkactor.New(bytes.Repeat([]byte{0x42}, 32), []string{"10.0.0.0/8"})
	request := httptest.NewRequest("GET", "https://spyglass.test", nil)
	request.RemoteAddr = "10.0.0.7:8443"
	request.Header.Set("X-Forwarded-For", "spoofed, values")
	if _, err := resolver.Actor(request); err == nil {
		t.Fatal("expected malformed forwarding header to fail closed")
	}
}
