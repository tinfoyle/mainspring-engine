package mcpoauth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/mcpauth"
)

const maximumClientMetadataBytes = 64 << 10

type ClientMetadataLoader interface {
	Load(context.Context, string) (mcpauth.Client, error)
}

type DNSResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type HTTPMetadataLoader struct {
	resolver DNSResolver
	dialer   net.Dialer
}

func NewHTTPMetadataLoader(resolver DNSResolver) (*HTTPMetadataLoader, error) {
	if resolver == nil {
		return nil, errors.New("client metadata DNS resolver is required")
	}
	return &HTTPMetadataLoader{resolver: resolver, dialer: net.Dialer{Timeout: 5 * time.Second, KeepAlive: -1}}, nil
}

type clientMetadataDocument struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

func (l *HTTPMetadataLoader) Load(ctx context.Context, clientID string) (mcpauth.Client, error) {
	if len(clientID) == 0 || len(clientID) > 2048 {
		return mcpauth.Client{}, mcpauth.ErrInvalid
	}
	parsed, err := url.Parse(clientID)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Path == "" || parsed.Path == "/") || parsed.String() != clientID {
		return mcpauth.Client{}, mcpauth.ErrInvalid
	}
	addresses, err := l.resolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return mcpauth.Client{}, mcpauth.ErrInvalid
	}
	for _, address := range addresses {
		if !publicMetadataAddress(address.IP) {
			return mcpauth.Client{}, mcpauth.ErrInvalid
		}
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	transport := &http.Transport{
		Proxy:               nil,
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 5 * time.Second,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext: func(dialContext context.Context, network, _ string) (net.Conn, error) {
			var dialErr error
			for _, address := range addresses {
				connection, candidateErr := l.dialer.DialContext(dialContext, network, net.JoinHostPort(address.IP.String(), port))
				if candidateErr == nil {
					return connection, nil
				}
				dialErr = candidateErr
			}
			return nil, dialErr
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("client metadata redirects are not accepted")
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil)
	if err != nil {
		return mcpauth.Client{}, mcpauth.ErrInvalid
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return mcpauth.Client{}, fmt.Errorf("load client metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return mcpauth.Client{}, mcpauth.ErrInvalid
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return mcpauth.Client{}, mcpauth.ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximumClientMetadataBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maximumClientMetadataBytes {
		return mcpauth.Client{}, mcpauth.ErrInvalid
	}
	var document clientMetadataDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return mcpauth.Client{}, mcpauth.ErrInvalid
	}
	document.ClientName = strings.TrimSpace(document.ClientName)
	if document.ClientID != clientID || document.ClientName == "" || len(document.ClientName) > 160 || len(document.RedirectURIs) == 0 || len(document.RedirectURIs) > 20 || !slices.Contains(document.GrantTypes, "authorization_code") || (len(document.ResponseTypes) > 0 && !slices.Contains(document.ResponseTypes, "code")) || document.TokenEndpointAuthMethod != "none" {
		return mcpauth.Client{}, mcpauth.ErrInvalid
	}
	return mcpauth.Client{ID: document.ClientID, Name: document.ClientName, RedirectURIs: document.RedirectURIs}, nil
}

func publicMetadataAddress(ip net.IP) bool {
	return ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
}
