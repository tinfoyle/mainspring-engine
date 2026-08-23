// Package webpublishconnector publishes one immutable Marketing delivery to a
// customer-controlled HTTPS endpoint. It uses a deterministic resource path,
// create-only PUT, and content-digest HEAD reconciliation so an ambiguous
// network result is never blindly replayed.
package webpublishconnector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

const (
	ProviderCode        = "web_https"
	maximumResponseBody = 4 << 10
	mediaType           = "application/vnd.infiniteocean.marketing-delivery+json; version=1"
)

type credentialDocument struct {
	Version     uint64 `json:"version"`
	HTTPSOrigin string `json:"https_origin"`
	PathPrefix  string `json:"path_prefix"`
	HealthPath  string `json:"health_path"`
	BearerToken string `json:"bearer_token"`
	RootCAPEM   string `json:"root_ca_pem,omitempty"`
}

type credential struct {
	credentialDocument
	origin *url.URL
	roots  *x509.CertPool
}

type clientFactory func(context.Context, credential) (*http.Client, error)

type Connector struct{ clients clientFactory }

func New() *Connector { return &Connector{clients: publicPinnedClient} }

func (connector *Connector) Probe(ctx context.Context, call integrationhealth.ProbeCall) integrationhealth.ProbeResult {
	if connector == nil || connector.clients == nil || call.Claim.ConnectorKind != domain.ConnectorWebPublish || call.Claim.CredentialProvider != ProviderCode {
		return healthUnavailable("web_publish_health_connector_mismatch")
	}
	config, err := parseCredential(call.Credential)
	if err != nil || call.Claim.Scope.HTTPSOrigin != config.HTTPSOrigin || call.Claim.Scope.PathPrefix != config.PathPrefix ||
		call.Claim.Scope.EmailAddress != "" || call.Claim.Scope.AudienceReference != "" {
		return healthUnavailable("web_publish_health_configuration_invalid")
	}
	client, err := connector.clients(ctx, config)
	if err != nil {
		return healthUnavailable("web_publish_health_unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, config.HTTPSOrigin+config.HealthPath, nil)
	if err != nil {
		return healthUnavailable("web_publish_health_configuration_invalid")
	}
	request.Header.Set("Authorization", "Bearer "+config.BearerToken)
	request.Header.Set("Accept", mediaType)
	response, err := client.Do(request)
	if err != nil {
		return healthUnavailable("web_publish_health_unavailable")
	}
	defer response.Body.Close()
	if !boundedResponse(response.Body) {
		return healthUnavailable("web_publish_health_response_invalid")
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return integrationhealth.ProbeResult{State: domain.HealthHealthy}
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return integrationhealth.ProbeResult{State: domain.HealthDegraded, ErrorCode: "web_publish_health_degraded"}
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return healthUnavailable("web_publish_health_authentication_failed")
	}
	return healthUnavailable("web_publish_health_rejected")
}

func (connector *Connector) Execute(ctx context.Context, call integrationexecution.ConnectorCall) integrationexecution.ConnectorResult {
	config, envelope, body, digest, result := connector.prepare(call)
	if result != nil {
		return *result
	}
	client, err := connector.clients(ctx, config)
	if err != nil {
		return failed("web_publish_configuration_invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, resourceURL(config, envelope.IdempotencyKey), bytes.NewReader(body))
	if err != nil {
		return failed("web_publish_configuration_invalid")
	}
	setHeaders(request, config, envelope.IdempotencyKey, digest)
	request.Header.Set("If-None-Match", "*")
	response, err := client.Do(request)
	if err != nil {
		return unknown("web_publish_uncertain")
	}
	defer response.Body.Close()
	if !boundedResponse(response.Body) {
		return unknown("web_publish_response_invalid")
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if response.Header.Get("Content-Digest") != contentDigest(digest) {
			return unknown("web_publish_response_invalid")
		}
		return integrationexecution.ConnectorResult{Outcome: domain.AttemptSucceeded}
	}
	if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusConflict ||
		response.StatusCode == http.StatusPreconditionFailed || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return unknown("web_publish_uncertain")
	}
	return failed("web_publish_rejected")
}

func (connector *Connector) Reconcile(ctx context.Context, call integrationexecution.ConnectorCall) integrationexecution.ConnectorResult {
	config, envelope, _, digest, result := connector.prepare(call)
	if result != nil {
		return *result
	}
	client, err := connector.clients(ctx, config)
	if err != nil {
		return failed("web_publish_configuration_invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, resourceURL(config, envelope.IdempotencyKey), nil)
	if err != nil {
		return failed("web_publish_configuration_invalid")
	}
	setHeaders(request, config, envelope.IdempotencyKey, digest)
	response, err := client.Do(request)
	if err != nil {
		return unknown("web_publish_reconciliation_unavailable")
	}
	defer response.Body.Close()
	if !boundedResponse(response.Body) {
		return unknown("web_publish_response_invalid")
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if response.Header.Get("Content-Digest") == contentDigest(digest) {
			return integrationexecution.ConnectorResult{Outcome: domain.AttemptSucceeded}
		}
		return unknown("web_publish_digest_mismatch")
	}
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		retry := time.Now().UTC().Add(30 * time.Second)
		return integrationexecution.ConnectorResult{Outcome: domain.AttemptNotApplied, ErrorCode: "web_publish_not_applied", RetryAt: &retry}
	}
	return unknown("web_publish_reconciliation_unavailable")
}

func (connector *Connector) prepare(call integrationexecution.ConnectorCall) (credential, integrationexecution.ProviderEnvelope, []byte, [sha256.Size]byte, *integrationexecution.ConnectorResult) {
	if connector == nil || connector.clients == nil || call.Claim.Capability != domain.CapabilityWebPublish || call.Claim.CredentialProvider != ProviderCode {
		result := failed("web_publish_connector_mismatch")
		return credential{}, integrationexecution.ProviderEnvelope{}, nil, [sha256.Size]byte{}, &result
	}
	config, err := parseCredential(call.Credential)
	if err != nil {
		result := failed("web_publish_configuration_invalid")
		return credential{}, integrationexecution.ProviderEnvelope{}, nil, [sha256.Size]byte{}, &result
	}
	envelope, err := integrationexecution.DecodeProviderEnvelope(call.Payload.ProviderPayload, call.Claim)
	if err != nil {
		result := failed("web_publish_payload_invalid")
		return credential{}, integrationexecution.ProviderEnvelope{}, nil, [sha256.Size]byte{}, &result
	}
	if envelope.Scope.HTTPSOrigin != config.HTTPSOrigin || envelope.Scope.PathPrefix != config.PathPrefix ||
		envelope.Scope.EmailAddress != "" || envelope.Scope.AudienceReference != "" {
		result := failed("web_publish_scope_mismatch")
		return credential{}, integrationexecution.ProviderEnvelope{}, nil, [sha256.Size]byte{}, &result
	}
	body := append([]byte(nil), call.Payload.ProviderPayload...)
	return config, envelope, body, sha256.Sum256(body), nil
}

func parseCredential(raw []byte) (credential, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return credential{}, errors.New("credential size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document credentialDocument
	if err := decoder.Decode(&document); err != nil {
		return credential{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return credential{}, errors.New("credential has trailing JSON")
	}
	parsed, err := url.Parse(document.HTTPSOrigin)
	cleanPrefix := path.Clean(document.PathPrefix)
	cleanHealth := path.Clean(document.HealthPath)
	if document.Version != 1 || err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || document.HTTPSOrigin != strings.TrimSuffix(parsed.String(), "/") ||
		!strings.HasPrefix(document.PathPrefix, "/") || cleanPrefix != document.PathPrefix || document.PathPrefix == "/" || strings.Contains(document.PathPrefix, "//") ||
		!strings.HasPrefix(document.HealthPath, document.PathPrefix+"/") || cleanHealth != document.HealthPath || strings.Contains(document.HealthPath, "//") ||
		document.BearerToken == "" || len(document.BearerToken) > 16<<10 || strings.ContainsAny(document.BearerToken, " \t\r\n") {
		return credential{}, errors.New("web publication credential is invalid")
	}
	parsed.Path, parsed.RawPath = "", ""
	var roots *x509.CertPool
	if document.RootCAPEM != "" {
		roots, err = x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM([]byte(document.RootCAPEM)) {
			return credential{}, errors.New("web publication root CA is invalid")
		}
	}
	return credential{credentialDocument: document, origin: parsed, roots: roots}, nil
}

func publicPinnedClient(ctx context.Context, config credential) (*http.Client, error) {
	host := config.origin.Hostname()
	port := config.origin.Port()
	if port == "" {
		port = "443"
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 || len(addresses) > 32 {
		return nil, errors.New("web publication DNS is unavailable")
	}
	pinned := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		if !publicIP(address.IP) {
			return nil, errors.New("web publication DNS contains a non-public address")
		}
		pinned = append(pinned, append(net.IP(nil), address.IP...))
	}
	transport := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: config.roots}}
	transport.DialContext = func(dialContext context.Context, network, _ string) (net.Conn, error) {
		var failures []error
		for _, address := range pinned {
			connection, dialErr := (&net.Dialer{}).DialContext(dialContext, network, net.JoinHostPort(address.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			failures = append(failures, dialErr)
		}
		return nil, errors.Join(failures...)
	}
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("web publication redirects are forbidden")
	}}, nil
}

func publicIP(value net.IP) bool {
	address, ok := netip.AddrFromSlice(value)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
}

func resourceURL(config credential, idempotencyKey string) string {
	return config.HTTPSOrigin + config.PathPrefix + "/" + url.PathEscape(idempotencyKey) + ".json"
}

func setHeaders(request *http.Request, config credential, idempotencyKey string, digest [sha256.Size]byte) {
	request.Header.Set("Authorization", "Bearer "+config.BearerToken)
	request.Header.Set("Accept", mediaType)
	request.Header.Set("Content-Type", mediaType)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.Header.Set("Content-Digest", contentDigest(digest))
}

func contentDigest(digest [sha256.Size]byte) string {
	return "sha-256=:" + base64.StdEncoding.EncodeToString(digest[:]) + ":"
}

func boundedResponse(body io.Reader) bool {
	if body == nil {
		return true
	}
	value, err := io.ReadAll(io.LimitReader(body, maximumResponseBody+1))
	return err == nil && len(value) <= maximumResponseBody
}

func failed(code string) integrationexecution.ConnectorResult {
	return integrationexecution.ConnectorResult{Outcome: domain.AttemptFailed, ErrorCode: code}
}

func unknown(code string) integrationexecution.ConnectorResult {
	return integrationexecution.ConnectorResult{Outcome: domain.AttemptUnknown, ErrorCode: code}
}

var _ integrationexecution.Connector = (*Connector)(nil)
var _ integrationhealth.Probe = (*Connector)(nil)

func healthUnavailable(code string) integrationhealth.ProbeResult {
	return integrationhealth.ProbeResult{State: domain.HealthUnavailable, ErrorCode: code}
}
