package workloadidentity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	routerIdentity = "spiffe://infiniteocean.net/spyglass/workloads/app-router"
	wrongIdentity  = "spiffe://infiniteocean.net/spyglass/workloads/other"
)

func TestMutualTLSRequiresExactWorkloadIdentity(t *testing.T) {
	fixture := newPKIFixture(t)
	serverFiles := fixture.issue(t, "server", "", true)
	routerFiles := fixture.issue(t, "router", routerIdentity, false)
	wrongFiles := fixture.issue(t, "wrong", wrongIdentity, false)

	handler, err := RequireClientIdentity(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), []string{routerIdentity}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	serverConfig, err := NewServerConfig(serverFiles)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = serverConfig
	server.StartTLS()
	defer server.Close()

	routerTransport, err := NewClientTransport(routerFiles)
	if err != nil {
		t.Fatal(err)
	}
	if routerTransport.Proxy != nil {
		t.Fatal("workload traffic must ignore ambient proxy configuration")
	}
	defer routerTransport.CloseIdleConnections()
	if response := request(t, &http.Client{Transport: routerTransport}, server.URL+"/internal"); response.StatusCode != http.StatusNoContent {
		t.Fatalf("router identity status=%d", response.StatusCode)
	}

	wrongTransport, _ := NewClientTransport(wrongFiles)
	defer wrongTransport.CloseIdleConnections()
	if response := request(t, &http.Client{Transport: wrongTransport}, server.URL+"/internal"); response.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong identity status=%d", response.StatusCode)
	}
}

func TestHealthProbeMayOmitClientCertificate(t *testing.T) {
	fixture := newPKIFixture(t)
	serverFiles := fixture.issue(t, "server", "", true)
	handler, _ := RequireClientIdentity(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), []string{routerIdentity}, discardLogger())
	serverConfig, _ := NewServerConfig(serverFiles)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = serverConfig
	server.StartTLS()
	defer server.Close()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: fixture.roots, MinVersion: tls.VersionTLS13}}}
	if response := request(t, client, server.URL+"/health/ready"); response.StatusCode != http.StatusOK {
		t.Fatalf("health status=%d", response.StatusCode)
	}
	if response := request(t, client, server.URL+"/api/v1/private"); response.StatusCode != http.StatusForbidden {
		t.Fatalf("private status=%d", response.StatusCode)
	}
	if response := request(t, client, server.URL+"/health/../api/v1/private"); response.StatusCode != http.StatusForbidden {
		t.Fatalf("non-canonical health status=%d", response.StatusCode)
	}
	legacyClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: fixture.roots, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12}}}
	if response, err := legacyClient.Get(server.URL + "/health/live"); err == nil {
		_ = response.Body.Close()
		t.Fatal("expected TLS 1.2 health probe to fail")
	}
}

func TestClientCredentialsReloadOnNewConnection(t *testing.T) {
	fixture := newPKIFixture(t)
	serverFiles := fixture.issue(t, "server", "", true)
	activeFiles := fixture.issue(t, "active", routerIdentity, false)
	replacement := fixture.issue(t, "replacement", wrongIdentity, false)
	handler, _ := RequireClientIdentity(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), []string{routerIdentity}, discardLogger())
	serverConfig, _ := NewServerConfig(serverFiles)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = serverConfig
	server.StartTLS()
	defer server.Close()

	transport, err := NewClientTransport(activeFiles)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport}
	if response := request(t, client, server.URL+"/private"); response.StatusCode != http.StatusNoContent {
		t.Fatalf("initial status=%d", response.StatusCode)
	}
	copyFile(t, replacement.Certificate, activeFiles.Certificate)
	copyFile(t, replacement.PrivateKey, activeFiles.PrivateKey)
	transport.CloseIdleConnections()
	if response := request(t, client, server.URL+"/private"); response.StatusCode != http.StatusForbidden {
		t.Fatalf("rotated status=%d", response.StatusCode)
	}
}

func TestServerCredentialsReloadOnNewHandshake(t *testing.T) {
	fixture := newPKIFixture(t)
	serverFiles := fixture.issue(t, "server", "", true)
	routerFiles := fixture.issue(t, "router", routerIdentity, false)
	invalidServerFiles := fixture.issue(t, "not-server", wrongIdentity, false)
	handler, _ := RequireClientIdentity(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), []string{routerIdentity}, discardLogger())
	serverConfig, _ := NewServerConfig(serverFiles)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = serverConfig
	server.StartTLS()
	defer server.Close()

	transport, _ := NewClientTransport(routerFiles)
	client := &http.Client{Transport: transport}
	if response := request(t, client, server.URL+"/private"); response.StatusCode != http.StatusNoContent {
		t.Fatalf("initial status=%d", response.StatusCode)
	}
	copyFile(t, invalidServerFiles.Certificate, serverFiles.Certificate)
	copyFile(t, invalidServerFiles.PrivateKey, serverFiles.PrivateKey)
	transport.CloseIdleConnections()
	if response, err := client.Get(server.URL + "/private"); err == nil {
		_ = response.Body.Close()
		t.Fatal("expected rotated invalid server identity to fail TLS verification")
	}
}

func TestConfigurationFailsClosed(t *testing.T) {
	if _, err := NewClientTransport(Files{}); err == nil {
		t.Fatal("expected missing workload material to fail")
	}
	if _, err := RequireClientIdentity(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), []string{"https://not-spiffe.example"}, discardLogger()); err == nil {
		t.Fatal("expected non-SPIFFE identity to fail")
	}
	fixture := newPKIFixture(t)
	files := fixture.issue(t, "client", routerIdentity, false)
	if err := os.WriteFile(files.TrustBundle, []byte("not a trust bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClientTransport(files); err == nil {
		t.Fatal("expected invalid trust bundle to fail")
	}
}

func request(t *testing.T, client *http.Client, target string) *http.Response {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return response
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type pkiFixture struct {
	directory string
	ca        *x509.Certificate
	key       *ecdsa.PrivateKey
	caPEM     []byte
	roots     *x509.CertPool
}

func newPKIFixture(t *testing.T) *pkiFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Spyglass test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("append test CA")
	}
	return &pkiFixture{directory: t.TempDir(), ca: certificate, key: key, caPEM: caPEM, roots: roots}
}

func (f *pkiFixture) issue(t *testing.T, name, identity string, server bool) Files {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: randomSerial(t), Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature}
	if server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		parsed, err := url.Parse(identity)
		if err != nil {
			t.Fatal(err)
		}
		template.URIs = []*url.URL{parsed}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, f.ca, &key.PublicKey, f.key)
	if err != nil {
		t.Fatal(err)
	}
	certificatePath := filepath.Join(f.directory, name+".crt")
	keyPath := filepath.Join(f.directory, name+".key")
	trustPath := filepath.Join(f.directory, name+"-ca.crt")
	writeFile(t, certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	encodedKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}))
	writeFile(t, trustPath, f.caPEM)
	return Files{Certificate: certificatePath, PrivateKey: keyPath, TrustBundle: trustPath}
}

func randomSerial(t *testing.T) *big.Int {
	t.Helper()
	value, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, destination, body)
}
