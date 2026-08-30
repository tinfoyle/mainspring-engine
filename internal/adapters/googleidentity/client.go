// Package googleidentity implements the narrow Google OpenID Connect boundary
// used for Spyglass login. It never requests or retains Google API access.
package googleidentity

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tinfoyle/spyglass-engine/internal/application/oidcauth"
)

const (
	Issuer                       = "https://accounts.google.com"
	productionAuthorizationURL   = "https://accounts.google.com/o/oauth2/v2/auth"
	productionTokenURL           = "https://oauth2.googleapis.com/token"
	productionJWKSetURL          = "https://www.googleapis.com/oauth2/v3/certs"
	maximumCredentialFileBytes   = 16 << 10
	maximumProviderResponseBytes = 64 << 10
)

var ErrConfiguration = errors.New("Google login configuration is invalid")

type Endpoints struct{ Authorization, Token, JWKSet string }

type Config struct {
	ClientID, ClientSecret string
	HTTPClient             *http.Client
}

type Client struct {
	client                                        *http.Client
	clientID, clientSecret                        string
	authorizationURL, tokenURL, jwkSetURL, issuer string
	allowHTTP                                     bool
	mu                                            sync.Mutex
	keys                                          map[string]*rsa.PublicKey
	keysExpiresAt                                 time.Time
}

func NewFromClientFile(config Config, filename string) (*Client, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" || !filepath.IsAbs(filename) {
		return nil, ErrConfiguration
	}
	clean := filepath.Clean(filename)
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil || resolved != clean {
		return nil, ErrConfiguration
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("%w: open client file", ErrConfiguration)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o037 != 0 {
		return nil, fmt.Errorf("%w: client file permissions", ErrConfiguration)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumCredentialFileBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maximumCredentialFileBytes {
		return nil, fmt.Errorf("%w: client file size", ErrConfiguration)
	}
	defer clear(raw)
	line := strings.TrimSuffix(string(raw), "\n")
	if strings.ContainsAny(line, "\r\n\t ") || strings.Count(line, ":") != 1 {
		return nil, fmt.Errorf("%w: client file content", ErrConfiguration)
	}
	clientID, clientSecret, ok := strings.Cut(line, ":")
	if !ok || !validCredential(clientID, 4096) || !validCredential(clientSecret, 8192) {
		return nil, fmt.Errorf("%w: client file content", ErrConfiguration)
	}
	config.ClientID, config.ClientSecret = clientID, clientSecret
	return New(config)
}

func New(config Config) (*Client, error) {
	return newClient(config, Endpoints{Authorization: productionAuthorizationURL, Token: productionTokenURL, JWKSet: productionJWKSetURL}, Issuer, false)
}

func NewFixture(config Config, endpoints Endpoints, issuer string) (*Client, error) {
	return newClient(config, endpoints, issuer, true)
}

func newClient(config Config, endpoints Endpoints, issuer string, allowHTTP bool) (*Client, error) {
	config.ClientID, config.ClientSecret = strings.TrimSpace(config.ClientID), strings.TrimSpace(config.ClientSecret)
	issuer = strings.TrimSpace(issuer)
	if !validCredential(config.ClientID, 4096) || !validCredential(config.ClientSecret, 8192) || issuer == "" {
		return nil, ErrConfiguration
	}
	for _, raw := range []string{endpoints.Authorization, endpoints.Token, endpoints.JWKSet, issuer} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
			(parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http")) {
			return nil, ErrConfiguration
		}
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{client: &copyClient, clientID: config.ClientID, clientSecret: config.ClientSecret,
		authorizationURL: endpoints.Authorization, tokenURL: endpoints.Token, jwkSetURL: endpoints.JWKSet,
		issuer: issuer, allowHTTP: allowHTTP}, nil
}

func (c *Client) AuthorizationURL(state, nonce, challenge, redirectURI string) (string, error) {
	if c == nil || !validProtocol(state) || !validProtocol(nonce) || !validProtocol(challenge) || !c.validRedirect(redirectURI) {
		return "", oidcauth.ErrInvalidAssertion
	}
	query := url.Values{
		"client_id":             {c.clientID},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"nonce":                 {nonce},
		"prompt":                {"select_account"},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {state},
	}
	return c.authorizationURL + "?" + query.Encode(), nil
}

func (c *Client) Exchange(ctx context.Context, code, verifier, redirectURI, nonce string) (oidcauth.Assertion, error) {
	if c == nil || ctx == nil || ctx.Err() != nil || !validProtocol(code) || !validProtocol(verifier) || !validProtocol(nonce) || !c.validRedirect(redirectURI) {
		return oidcauth.Assertion{}, oidcauth.ErrInvalidAssertion
	}
	form := url.Values{"client_id": {c.clientID}, "client_secret": {c.clientSecret}, "code": {code},
		"code_verifier": {verifier}, "grant_type": {"authorization_code"}, "redirect_uri": {redirectURI}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oidcauth.Assertion{}, oidcauth.ErrInvalidAssertion
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.client.Do(request)
	if err != nil {
		return oidcauth.Assertion{}, fmt.Errorf("exchange Google authorization: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumProviderResponseBytes))
		return oidcauth.Assertion{}, oidcauth.ErrInvalidAssertion
	}
	var tokenResponse struct {
		IDToken string `json:"id_token"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maximumProviderResponseBytes+1))
	if decoder.Decode(&tokenResponse) != nil || !validCredential(tokenResponse.IDToken, maximumProviderResponseBytes) {
		return oidcauth.Assertion{}, oidcauth.ErrInvalidAssertion
	}
	defer func() { tokenResponse.IDToken = "" }()
	return c.verify(ctx, tokenResponse.IDToken, nonce)
}

type claims struct {
	jwt.RegisteredClaims
	Nonce         string `json:"nonce"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

func (c *Client) verify(ctx context.Context, raw, nonce string) (oidcauth.Assertion, error) {
	parsedClaims := &claims{}
	token, err := jwt.ParseWithClaims(raw, parsedClaims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if token.Method.Alg() != "RS256" || kid == "" {
			return nil, oidcauth.ErrInvalidAssertion
		}
		return c.key(ctx, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(c.issuer), jwt.WithAudience(c.clientID),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(30*time.Second))
	if err != nil || token == nil || !token.Valid || parsedClaims.Subject == "" || parsedClaims.Nonce != nonce || !parsedClaims.EmailVerified || strings.TrimSpace(parsedClaims.Email) == "" {
		return oidcauth.Assertion{}, oidcauth.ErrInvalidAssertion
	}
	return oidcauth.Assertion{Issuer: c.issuer, Subject: parsedClaims.Subject, Email: parsedClaims.Email,
		DisplayName: parsedClaims.Name, EmailVerified: parsedClaims.EmailVerified}, nil
}

func (c *Client) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.keysExpiresAt) {
		if key := c.keys[kid]; key != nil {
			return key, nil
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.jwkSetURL, nil)
	if err != nil {
		return nil, oidcauth.ErrInvalidAssertion
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, oidcauth.ErrInvalidAssertion
	}
	var set struct {
		Keys []struct{ Kty, Kid, Use, Alg, N, E string } `json:"keys"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, maximumProviderResponseBytes+1)).Decode(&set) != nil {
		return nil, oidcauth.ErrInvalidAssertion
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, value := range set.Keys {
		if value.Kty != "RSA" || value.Use != "sig" || value.Alg != "RS256" || value.Kid == "" {
			continue
		}
		nBytes, nErr := base64.RawURLEncoding.DecodeString(value.N)
		eBytes, eErr := base64.RawURLEncoding.DecodeString(value.E)
		exponent := 0
		for _, part := range eBytes {
			exponent = exponent<<8 | int(part)
		}
		if nErr != nil || eErr != nil || len(nBytes) < 256 || exponent < 3 {
			continue
		}
		keys[value.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: exponent}
	}
	if len(keys) == 0 {
		return nil, oidcauth.ErrInvalidAssertion
	}
	ttl := 30 * time.Minute
	for _, directive := range strings.Split(response.Header.Get("Cache-Control"), ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(directive), "=")
		if ok && strings.EqualFold(name, "max-age") {
			if seconds, parseErr := strconv.Atoi(value); parseErr == nil && seconds > 0 && seconds <= 24*60*60 {
				ttl = time.Duration(seconds) * time.Second
			}
		}
	}
	c.keys, c.keysExpiresAt = keys, time.Now().Add(ttl)
	if key := keys[kid]; key != nil {
		return key, nil
	}
	return nil, oidcauth.ErrInvalidAssertion
}

func (c *Client) validRedirect(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" {
		return false
	}
	return parsed.Scheme == "https" || c.allowHTTP && parsed.Scheme == "http"
}

func validProtocol(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 16 && len(value) <= 8192 && !strings.ContainsAny(value, "\r\n\t ")
}

func validCredential(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}
