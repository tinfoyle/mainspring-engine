// Package webresearch implements the network-facing public-web retrieval
// boundary. It resolves and pins every hop independently and never delegates
// redirect, proxy, cookie, credential, or decompression policy to net/http.
package webresearch

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

const (
	DefaultMaximumBodyBytes       = int64(2 << 20)
	DefaultMaximumCompressedBytes = int64(1 << 20)
	DefaultMaximumHeaderBytes     = int64(64 << 10)
	DefaultMaximumRedirects       = 5
	DefaultTimeout                = 20 * time.Second
)

var (
	ErrInvalid     = errors.New("web research request is invalid")
	ErrDenied      = errors.New("web research destination is denied")
	ErrUnavailable = errors.New("web research destination is unavailable")
	ErrTooLarge    = errors.New("web research response is too large")
	ErrType        = errors.New("web research response type is unsupported")
)

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type Config struct {
	Resolver               Resolver
	Dialer                 Dialer
	RootCAs                *x509.CertPool
	Timeout                time.Duration
	MaximumBodyBytes       int64
	MaximumCompressedBytes int64
	MaximumHeaderBytes     int64
	MaximumRedirects       int
	UserAgent              string
	Now                    func() time.Time
}

type Policy struct {
	HTTPSOrigin string
	PathPrefix  string
}

type Result struct {
	CanonicalURL string
	MediaType    string
	Content      []byte
	SHA256       [sha256.Size]byte
	RetrievedAt  time.Time
	Redirects    int
}

type Client struct {
	resolver Resolver
	dialer   Dialer
	roots    *x509.CertPool
	timeout  time.Duration
	body     int64
	packed   int64
	headers  int64
	redirect int
	agent    string
	now      func() time.Time
}

func New(config Config) (*Client, error) {
	if config.Resolver == nil {
		config.Resolver = net.DefaultResolver
	}
	if config.Dialer == nil {
		config.Dialer = &net.Dialer{Timeout: 5 * time.Second, KeepAlive: -1}
	}
	if config.Timeout == 0 {
		config.Timeout = DefaultTimeout
	}
	if config.MaximumBodyBytes == 0 {
		config.MaximumBodyBytes = DefaultMaximumBodyBytes
	}
	if config.MaximumCompressedBytes == 0 {
		config.MaximumCompressedBytes = DefaultMaximumCompressedBytes
	}
	if config.MaximumHeaderBytes == 0 {
		config.MaximumHeaderBytes = DefaultMaximumHeaderBytes
	}
	if config.MaximumRedirects == 0 {
		config.MaximumRedirects = DefaultMaximumRedirects
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	config.UserAgent = strings.TrimSpace(config.UserAgent)
	if config.Timeout < 100*time.Millisecond || config.Timeout > time.Minute || config.MaximumBodyBytes < 1024 || config.MaximumBodyBytes > 16<<20 ||
		config.MaximumCompressedBytes < 1024 || config.MaximumCompressedBytes > config.MaximumBodyBytes || config.MaximumHeaderBytes < 4096 ||
		config.MaximumHeaderBytes > 256<<10 || config.MaximumRedirects < 1 || config.MaximumRedirects > 10 || len(config.UserAgent) > 200 ||
		strings.ContainsAny(config.UserAgent, "\r\n") {
		return nil, ErrInvalid
	}
	return &Client{resolver: config.Resolver, dialer: config.Dialer, roots: config.RootCAs, timeout: config.Timeout,
		body: config.MaximumBodyBytes, packed: config.MaximumCompressedBytes, headers: config.MaximumHeaderBytes,
		redirect: config.MaximumRedirects, agent: config.UserAgent, now: config.Now}, nil
}

func (client *Client) Fetch(ctx context.Context, rawURL string, policy Policy) (Result, error) {
	if client == nil || ctx == nil || ctx.Err() != nil {
		return Result{}, ErrInvalid
	}
	origin, prefix, err := normalizePolicy(policy)
	if err != nil {
		return Result{}, err
	}
	current, err := canonicalURL(rawURL)
	if err != nil || !withinPolicy(current, origin, prefix) {
		return Result{}, errors.Join(ErrDenied, err)
	}
	requestContext, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()
	for redirects := 0; ; redirects++ {
		response, transport, err := client.getPinned(requestContext, current)
		if err != nil {
			return Result{}, err
		}
		if response.StatusCode >= 300 && response.StatusCode <= 399 {
			location := response.Header.Get("Location")
			_ = response.Body.Close()
			transport.CloseIdleConnections()
			if redirects >= client.redirect || location == "" {
				return Result{}, errors.Join(ErrDenied, errors.New("web research redirect limit exceeded"))
			}
			nextReference, parseErr := url.Parse(location)
			if parseErr != nil {
				return Result{}, errors.Join(ErrDenied, parseErr)
			}
			next, canonicalErr := canonicalURL(current.ResolveReference(nextReference).String())
			if canonicalErr != nil || !withinPolicy(next, origin, prefix) {
				return Result{}, errors.Join(ErrDenied, canonicalErr)
			}
			current = next
			continue
		}
		if response.StatusCode < 200 || response.StatusCode > 299 {
			_ = response.Body.Close()
			transport.CloseIdleConnections()
			return Result{}, fmt.Errorf("%w: HTTP %d", ErrUnavailable, response.StatusCode)
		}
		content, mediaType, readErr := client.readBody(response)
		_ = response.Body.Close()
		transport.CloseIdleConnections()
		if readErr != nil {
			return Result{}, readErr
		}
		return Result{CanonicalURL: current.String(), MediaType: mediaType, Content: content, SHA256: sha256.Sum256(content),
			RetrievedAt: client.now().UTC(), Redirects: redirects}, nil
	}
}

// Validate performs the same canonicalization, scope, DNS and public-address
// checks as Fetch without opening a connection. Search adapters use it before
// returning provider-supplied result metadata.
func (client *Client) Validate(ctx context.Context, rawURL string, policy Policy) (string, error) {
	if client == nil || ctx == nil || ctx.Err() != nil {
		return "", ErrInvalid
	}
	origin, prefix, err := normalizePolicy(policy)
	if err != nil {
		return "", err
	}
	target, err := canonicalURL(rawURL)
	if err != nil || !withinPolicy(target, origin, prefix) {
		return "", errors.Join(ErrDenied, err)
	}
	requestContext, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()
	if _, err := client.resolvePublic(requestContext, target.Hostname()); err != nil {
		return "", err
	}
	return target.String(), nil
}

func (client *Client) getPinned(ctx context.Context, target *url.URL) (*http.Response, *http.Transport, error) {
	addresses, err := client.resolvePublic(ctx, target.Hostname())
	if err != nil {
		return nil, nil, err
	}
	pinned := addresses[0]
	port := target.Port()
	if port == "" {
		port = "443"
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DisableCompression:     true,
		DisableKeepAlives:      true,
		ForceAttemptHTTP2:      false,
		MaxResponseHeaderBytes: client.headers,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, ServerName: target.Hostname(), RootCAs: client.roots},
		DialContext: func(dialContext context.Context, network, _ string) (net.Conn, error) {
			return client.dialer.DialContext(dialContext, network, net.JoinHostPort(pinned.String(), port))
		},
	}
	httpClient := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, nil, errors.Join(ErrInvalid, err)
	}
	request.Header.Set("Accept", "text/html, text/plain, application/pdf")
	request.Header.Set("Accept-Encoding", "gzip")
	if client.agent != "" {
		request.Header.Set("User-Agent", client.agent)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, nil, errors.Join(ErrUnavailable, err)
	}
	return response, transport, nil
}

func (client *Client) resolvePublic(ctx context.Context, hostname string) ([]netip.Addr, error) {
	addresses, err := client.resolver.LookupNetIP(ctx, "ip", hostname)
	if err != nil || len(addresses) == 0 {
		return nil, errors.Join(ErrUnavailable, errors.New("web research host did not resolve"))
	}
	addresses = append([]netip.Addr(nil), addresses...)
	for index := range addresses {
		addresses[index] = addresses[index].Unmap()
		if !publicAddress(addresses[index]) {
			return nil, errors.Join(ErrDenied, errors.New("web research host resolved outside the public Internet"))
		}
	}
	slices.SortFunc(addresses, func(left, right netip.Addr) int { return left.Compare(right) })
	return addresses, nil
}

func (client *Client) readBody(response *http.Response) ([]byte, string, error) {
	declared, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !acceptedType(declared) {
		return nil, "", ErrType
	}
	var source io.Reader = io.LimitReader(response.Body, client.body+1)
	encoding := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding")))
	if response.ContentLength > client.body && (encoding == "" || encoding == "identity") {
		return nil, "", ErrTooLarge
	}
	if encoding != "" && encoding != "identity" {
		if encoding != "gzip" {
			return nil, "", ErrType
		}
		if response.ContentLength > client.packed {
			return nil, "", ErrTooLarge
		}
		compressed, readErr := io.ReadAll(io.LimitReader(response.Body, client.packed+1))
		if readErr != nil {
			return nil, "", errors.Join(ErrUnavailable, readErr)
		}
		if int64(len(compressed)) > client.packed {
			return nil, "", ErrTooLarge
		}
		reader, gzipErr := gzip.NewReader(bytes.NewReader(compressed))
		if gzipErr != nil {
			return nil, "", ErrType
		}
		defer reader.Close()
		source = io.LimitReader(reader, client.body+1)
	}
	content, err := io.ReadAll(source)
	if err != nil {
		return nil, "", errors.Join(ErrUnavailable, err)
	}
	if int64(len(content)) > client.body {
		return nil, "", ErrTooLarge
	}
	if len(content) == 0 {
		return nil, "", ErrType
	}
	verified := http.DetectContentType(content)
	verified, _, _ = mime.ParseMediaType(verified)
	if !compatibleType(declared, verified) {
		return nil, "", ErrType
	}
	return content, declared, nil
}

func normalizePolicy(policy Policy) (*url.URL, string, error) {
	origin, err := canonicalURL(policy.HTTPSOrigin)
	if err != nil || origin.RawQuery != "" || origin.Path != "/" {
		return nil, "", ErrInvalid
	}
	prefix := strings.TrimSpace(policy.PathPrefix)
	if prefix == "" {
		prefix = "/"
	}
	if !strings.HasPrefix(prefix, "/") || path.Clean(prefix) != prefix || strings.Contains(prefix, "//") || strings.Contains(prefix, "\\") {
		return nil, "", ErrInvalid
	}
	return origin, prefix, nil
}

func canonicalURL(raw string) (*url.URL, error) {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || value.Scheme != "https" || value.Hostname() == "" || value.User != nil || value.Opaque != "" {
		return nil, ErrInvalid
	}
	host, err := idna.Lookup.ToASCII(strings.TrimSuffix(strings.ToLower(value.Hostname()), "."))
	if err != nil || host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".home.arpa") || net.ParseIP(host) != nil {
		return nil, ErrDenied
	}
	port := value.Port()
	if port != "" && port != "443" {
		return nil, ErrDenied
	}
	value.Scheme, value.Host = "https", host
	if port != "" {
		value.Host = net.JoinHostPort(host, port)
	}
	value.Fragment, value.RawFragment = "", ""
	if value.Path == "" {
		value.Path = "/"
	}
	if value.RawPath != "" {
		escaped, unescapeErr := url.PathUnescape(value.RawPath)
		if unescapeErr != nil || escaped != value.Path {
			return nil, ErrInvalid
		}
	}
	clean := path.Clean(value.Path)
	if clean != value.Path || strings.Contains(value.EscapedPath(), "%2f") || strings.Contains(value.EscapedPath(), "%2F") || strings.Contains(value.EscapedPath(), "%5c") || strings.Contains(value.EscapedPath(), "%5C") {
		return nil, ErrDenied
	}
	return value, nil
}

func withinPolicy(target, origin *url.URL, prefix string) bool {
	if target.Scheme != origin.Scheme || target.Host != origin.Host {
		return false
	}
	return prefix == "/" || target.Path == prefix || strings.HasPrefix(target.Path, strings.TrimSuffix(prefix, "/")+"/")
}

func acceptedType(value string) bool {
	return value == "text/html" || value == "text/plain" || value == "application/pdf"
}

func compatibleType(declared, verified string) bool {
	if declared == "application/pdf" {
		return verified == "application/pdf"
	}
	if declared == "text/html" {
		return verified == "text/html" || verified == "text/plain"
	}
	return declared == "text/plain" && strings.HasPrefix(verified, "text/plain")
}

func publicAddress(address netip.Addr) bool {
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range deniedPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

var deniedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2001:10::/28"), netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"), netip.MustParsePrefix("ff00::/8"),
}
