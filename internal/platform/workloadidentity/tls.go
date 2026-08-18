// Package workloadidentity provides mutually authenticated TLS plumbing for
// Spyglass service-to-service boundaries. TLS establishes workload identity;
// application route proofs still carry Account-scoped authority.
package workloadidentity

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Files struct {
	Certificate string
	PrivateKey  string
	TrustBundle string
}

// NewClientTransport returns a pooled transport that reloads its certificate,
// private key, and trust bundle for each new TLS connection. Existing safe
// connections may drain naturally during credential rotation.
func NewClientTransport(files Files) (*http.Transport, error) {
	loader, err := newLoader(files)
	if err != nil {
		return nil, err
	}
	if _, err := loader.clientConfig("initial.invalid"); err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Cluster service traffic must never be redirected through ambient proxy
	// environment variables, where the workload certificate boundary changes.
	transport.Proxy = nil
	transport.ForceAttemptHTTP2 = true
	transport.TLSClientConfig = nil
	transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("parse workload TLS address: %w", err)
		}
		config, err := loader.clientConfig(host)
		if err != nil {
			return nil, err
		}
		connection, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		tlsConnection := tls.Client(connection, config)
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			_ = connection.Close()
			return nil, err
		}
		return tlsConnection, nil
	}
	return transport, nil
}

// NewServerConfig returns a TLS 1.3 configuration that reloads server
// credentials and the client trust bundle on every new handshake. Client
// certificates are verified when supplied; RequireClientIdentity enforces them
// on private application paths while allowing certificate-free health probes.
func NewServerConfig(files Files) (*tls.Config, error) {
	loader, err := newLoader(files)
	if err != nil {
		return nil, err
	}
	config, err := loader.serverConfig()
	if err != nil {
		return nil, err
	}
	config.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
		return loader.serverConfig()
	}
	return config, nil
}

// RequireClientIdentity admits health probes without a certificate and
// requires every other request to present a verified certificate containing
// one exact allowed URI identity.
func RequireClientIdentity(next http.Handler, identities []string, logger *slog.Logger) (http.Handler, error) {
	if next == nil || logger == nil || len(identities) == 0 {
		return nil, errors.New("workload identity handler, logger, and identities are required")
	}
	allowed := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		normalized, err := normalizeIdentity(identity)
		if err != nil {
			return nil, err
		}
		if _, exists := allowed[normalized]; exists {
			return nil, errors.New("workload client identities must be unique")
		}
		allowed[normalized] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicHealthRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !verifiedIdentity(r.TLS, allowed) {
			reason := "missing_or_unverified_certificate"
			if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 && len(r.TLS.PeerCertificates) > 0 {
				reason = "unexpected_identity"
			}
			logger.Warn("workload identity denied", "method", r.Method, "reason", reason)
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"status":403,"code":"workload_identity_denied"}`))
			return
		}
		next.ServeHTTP(w, r)
	}), nil
}

func publicHealthRequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	return r.URL.Path == "/health/live" || r.URL.Path == "/health/ready" || r.URL.Path == "/health/status"
}

type loader struct{ files Files }

func newLoader(files Files) (*loader, error) {
	if strings.TrimSpace(files.Certificate) == "" || strings.TrimSpace(files.PrivateKey) == "" || strings.TrimSpace(files.TrustBundle) == "" {
		return nil, errors.New("workload certificate, private key, and trust bundle files are required")
	}
	return &loader{files: files}, nil
}

func (l *loader) material() (tls.Certificate, *x509.CertPool, error) {
	certificatePEM, err := os.ReadFile(l.files.Certificate)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("read workload certificate: %w", err)
	}
	privateKeyPEM, err := os.ReadFile(l.files.PrivateKey)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("read workload private key: %w", err)
	}
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("load workload key pair: %w", err)
	}
	trustPEM, err := os.ReadFile(l.files.TrustBundle)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("read workload trust bundle: %w", err)
	}
	trust := x509.NewCertPool()
	if !trust.AppendCertsFromPEM(trustPEM) {
		return tls.Certificate{}, nil, errors.New("workload trust bundle contains no certificates")
	}
	return certificate, trust, nil
}

func (l *loader) clientConfig(serverName string) (*tls.Config, error) {
	certificate, trust, err := l.material()
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{certificate}, RootCAs: trust, ServerName: serverName, MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}}, nil
}

func (l *loader) serverConfig() (*tls.Config, error) {
	certificate, trust, err := l.material()
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{certificate}, ClientCAs: trust, ClientAuth: tls.VerifyClientCertIfGiven, MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}, SessionTicketsDisabled: true}, nil
}

func normalizeIdentity(raw string) (string, error) {
	identity, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || identity.Scheme != "spiffe" || identity.Host == "" || identity.User != nil || identity.Path == "" || identity.Path == "/" || identity.RawQuery != "" || identity.Fragment != "" {
		return "", errors.New("workload identities must be absolute spiffe URI identities")
	}
	return identity.String(), nil
}

func verifiedIdentity(state *tls.ConnectionState, allowed map[string]struct{}) bool {
	if state == nil || len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
		return false
	}
	for _, identity := range state.PeerCertificates[0].URIs {
		if _, ok := allowed[identity.String()]; ok {
			return true
		}
	}
	return false
}
