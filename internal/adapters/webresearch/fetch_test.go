package webresearch

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type fixedResolver struct {
	values map[string][]netip.Addr
	calls  []string
}

func (resolver *fixedResolver) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	resolver.calls = append(resolver.calls, network+":"+host)
	values := resolver.values[host]
	if len(values) == 0 {
		return nil, errors.New("not found")
	}
	return append([]netip.Addr(nil), values...), nil
}

type mappedDialer struct {
	target string
	calls  []string
}

func (dialer *mappedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer.calls = append(dialer.calls, address)
	return (&net.Dialer{}).DialContext(ctx, network, dialer.target)
}

func TestFetchPinsPublicAddressVerifiesTLSAndBoundsPolicy(t *testing.T) {
	server, roots := newTLSServer(t, "research.example", http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Host != "research.example" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Errorf("unsafe request headers/authority: host=%q headers=%v", request.Host, request.Header)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>Evidence</title><p>bounded public evidence</p>"))
	}))
	resolver := &fixedResolver{values: map[string][]netip.Addr{"research.example": {netip.MustParseAddr("93.184.216.34")}}}
	dialer := &mappedDialer{target: server}
	client, err := New(Config{Resolver: resolver, Dialer: dialer, RootCAs: roots, UserAgent: "Spyglass-Web-Research/1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Fetch(context.Background(), "https://RESEARCH.example/reports/current#section",
		Policy{HTTPSOrigin: "https://research.example", PathPrefix: "/reports"})
	if err != nil {
		t.Fatal(err)
	}
	if result.CanonicalURL != "https://research.example/reports/current" || result.MediaType != "text/html" ||
		!bytes.Contains(result.Content, []byte("bounded public evidence")) || result.SHA256 == ([32]byte{}) || result.Redirects != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(dialer.calls) != 1 || dialer.calls[0] != "93.184.216.34:443" {
		t.Fatalf("connection was not pinned: %v", dialer.calls)
	}
	if _, err := client.Fetch(context.Background(), "https://research.example/other", Policy{HTTPSOrigin: "https://research.example", PathPrefix: "/reports"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("outside path error=%v", err)
	}
}

func TestFetchRejectsPrivateMixedAndMetadataResolutionBeforeDial(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.2.3.4", "169.254.169.254", "100.64.0.1", "192.0.2.1", "::1", "fc00::1", "fe80::1"} {
		t.Run(address, func(t *testing.T) {
			resolver := &fixedResolver{values: map[string][]netip.Addr{"research.example": {netip.MustParseAddr(address)}}}
			dialer := &mappedDialer{target: "127.0.0.1:1"}
			client, _ := New(Config{Resolver: resolver, Dialer: dialer})
			_, err := client.Fetch(context.Background(), "https://research.example/", Policy{HTTPSOrigin: "https://research.example", PathPrefix: "/"})
			if !errors.Is(err, ErrDenied) || len(dialer.calls) != 0 {
				t.Fatalf("address=%s err=%v dial=%v", address, err, dialer.calls)
			}
		})
	}
	resolver := &fixedResolver{values: map[string][]netip.Addr{"research.example": {
		netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.1"),
	}}}
	client, _ := New(Config{Resolver: resolver, Dialer: &mappedDialer{target: "127.0.0.1:1"}})
	if _, err := client.Fetch(context.Background(), "https://research.example/", Policy{HTTPSOrigin: "https://research.example"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("mixed resolution error=%v", err)
	}
}

func TestFetchValidatesEveryRedirectAndNeverCrossesAuthority(t *testing.T) {
	server, roots := newTLSServer(t, "research.example", http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/start" {
			w.Header().Set("Location", "/reports/final")
			w.WriteHeader(http.StatusFound)
			return
		}
		if request.URL.Path == "/cross" {
			w.Header().Set("Location", "https://other.example/report")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("final evidence"))
	}))
	resolver := &fixedResolver{values: map[string][]netip.Addr{"research.example": {netip.MustParseAddr("93.184.216.34")}}}
	client, _ := New(Config{Resolver: resolver, Dialer: &mappedDialer{target: server}, RootCAs: roots})
	result, err := client.Fetch(context.Background(), "https://research.example/start", Policy{HTTPSOrigin: "https://research.example"})
	if err != nil || result.Redirects != 1 || result.CanonicalURL != "https://research.example/reports/final" {
		t.Fatalf("same-origin redirect result=%+v err=%v", result, err)
	}
	if _, err := client.Fetch(context.Background(), "https://research.example/cross", Policy{HTTPSOrigin: "https://research.example"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("cross-origin redirect error=%v", err)
	}
}

func TestFetchRejectsOversizedDecompressionAndTypeConfusion(t *testing.T) {
	server, roots := newTLSServer(t, "research.example", http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/bomb":
			var packed bytes.Buffer
			writer := gzip.NewWriter(&packed)
			_, _ = writer.Write(bytes.Repeat([]byte("x"), 4097))
			_ = writer.Close()
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(packed.Bytes())
		case "/confused":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("not a pdf"))
		default:
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("bytes"))
		}
	}))
	resolver := &fixedResolver{values: map[string][]netip.Addr{"research.example": {netip.MustParseAddr("93.184.216.34")}}}
	client, _ := New(Config{Resolver: resolver, Dialer: &mappedDialer{target: server}, RootCAs: roots,
		MaximumBodyBytes: 4096, MaximumCompressedBytes: 2048})
	for _, test := range []struct {
		path string
		want error
	}{{"/bomb", ErrTooLarge}, {"/confused", ErrType}, {"/unsupported", ErrType}} {
		_, err := client.Fetch(context.Background(), "https://research.example"+test.path, Policy{HTTPSOrigin: "https://research.example"})
		if !errors.Is(err, test.want) {
			t.Fatalf("path=%s err=%v want=%v", test.path, err, test.want)
		}
	}
}

func TestCanonicalURLRejectsUnsafeForms(t *testing.T) {
	for _, value := range []string{
		"http://research.example/", "https://user:secret@research.example/", "https://127.0.0.1/",
		"https://research.example:8443/", "https://localhost/", "https://research.example/a/../b",
		"https://research.example/a%2fb", "https://research.example/a%5cb",
	} {
		if _, err := canonicalURL(value); err == nil {
			t.Fatalf("unsafe URL accepted: %s", value)
		}
	}
	value, err := canonicalURL("https://Research.Example.:443/path?q=1#fragment")
	if err != nil || value.String() != "https://research.example:443/path?q=1" {
		t.Fatalf("canonical=%v err=%v", value, err)
	}
}

func newTLSServer(t *testing.T, hostname string, handler http.Handler) (string, *x509.CertPool) {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "web research test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: hostname}, DNSNames: []string{hostname},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)}))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsListener := tls.NewListener(listener, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(tlsListener) }()
	t.Cleanup(func() { _ = server.Close() })
	parsedCA, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsedCA)
	return listener.Addr().String(), roots
}

func TestNoSensitiveHeadersAreSynthesized(t *testing.T) {
	for _, value := range []string{"good-agent", "", strings.Repeat("a", 200)} {
		if _, err := New(Config{UserAgent: value}); err != nil {
			t.Fatalf("valid user agent %q: %v", value, err)
		}
	}
	for _, value := range []string{"bad\r\nAuthorization: secret", strings.Repeat("a", 201)} {
		if _, err := New(Config{UserAgent: value}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsafe user agent %q error=%v", value, err)
		}
	}
}
