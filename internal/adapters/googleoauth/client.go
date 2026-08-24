// Package googleoauth implements the fixed-origin Google OAuth authorization,
// token-exchange and revocation boundary used by Integration authorization.
package googleoauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationauthorization"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

const (
	productionAuthorizationEndpoint = "https://accounts.google.com/o/oauth2/v2/auth"
	productionTokenEndpoint         = "https://oauth2.googleapis.com/token"
	productionRevocationEndpoint    = "https://oauth2.googleapis.com/revoke"
	maximumClientFileBytes          = 16 << 10
	maximumResponseBytes            = 32 << 10
	maximumProtocolValueBytes       = 32 << 10
)

var (
	ErrConfiguration = errors.New("Google OAuth client configuration is invalid")
	protocolValue    = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
)

type Config struct {
	Client       *http.Client
	ClientID     string
	ClientSecret string
}

type Client struct {
	client                                               *http.Client
	clientID, clientSecret                               string
	authorizationEndpoint, tokenEndpoint, revokeEndpoint string
}

type clientFile struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func New(config Config) (*Client, error) {
	return newClient(config, productionAuthorizationEndpoint, productionTokenEndpoint, productionRevocationEndpoint, false)
}

func NewFromClientFile(config Config, filename string) (*Client, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" || !filepath.IsAbs(filename) {
		return nil, ErrConfiguration
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(filename))
	if err != nil || resolved != filepath.Clean(filename) {
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
	raw, err := io.ReadAll(io.LimitReader(file, maximumClientFileBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maximumClientFileBytes {
		wipe(raw)
		return nil, fmt.Errorf("%w: client file size", ErrConfiguration)
	}
	defer wipe(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value clientFile
	if err := decoder.Decode(&value); err != nil || requireEnd(decoder) != nil || !validSecret(value.ClientID, 4096) || !validSecret(value.ClientSecret, 8192) {
		return nil, fmt.Errorf("%w: client file content", ErrConfiguration)
	}
	config.ClientID, config.ClientSecret = value.ClientID, value.ClientSecret
	return New(config)
}

func newClient(config Config, authorizationEndpoint, tokenEndpoint, revokeEndpoint string, allowHTTP bool) (*Client, error) {
	config.ClientID, config.ClientSecret = strings.TrimSpace(config.ClientID), strings.TrimSpace(config.ClientSecret)
	if !validSecret(config.ClientID, 4096) || !validSecret(config.ClientSecret, 8192) {
		return nil, ErrConfiguration
	}
	for _, endpoint := range []string{authorizationEndpoint, tokenEndpoint, revokeEndpoint} {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
			(parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http")) {
			return nil, ErrConfiguration
		}
	}
	client := config.Client
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{client: &copyClient, clientID: config.ClientID, clientSecret: config.ClientSecret,
		authorizationEndpoint: authorizationEndpoint, tokenEndpoint: tokenEndpoint, revokeEndpoint: revokeEndpoint}, nil
}

func (client *Client) AuthorizationURL(state, pkceChallenge []byte, redirectURI string) (string, error) {
	if client == nil || client.client == nil || !validPKCEValue(state) || !validPKCEValue(pkceChallenge) || !validRedirect(redirectURI) {
		return "", integrationauthorization.ErrInvalid
	}
	query := url.Values{
		"access_type":            {"offline"},
		"client_id":              {client.clientID},
		"code_challenge":         {string(pkceChallenge)},
		"code_challenge_method":  {"S256"},
		"include_granted_scopes": {"false"},
		"prompt":                 {"consent"},
		"redirect_uri":           {redirectURI},
		"response_type":          {"code"},
		"scope":                  {domain.GoogleDriveReadScope},
		"state":                  {string(state)},
	}
	return client.authorizationEndpoint + "?" + query.Encode(), nil
}

func (client *Client) Exchange(ctx context.Context, exchange integrationauthorization.ExchangeRequest) (integrationauthorization.RefreshCredential, error) {
	if client == nil || client.client == nil || ctx == nil || ctx.Err() != nil || !validAuthorizationCode(exchange.Code) ||
		!validPKCEValue(exchange.PKCEVerifier) || !validRedirect(exchange.RedirectURI) {
		return integrationauthorization.RefreshCredential{}, integrationauthorization.ErrInvalid
	}
	form := url.Values{"client_id": {client.clientID}, "client_secret": {client.clientSecret}, "code": {string(exchange.Code)},
		"code_verifier": {string(exchange.PKCEVerifier)}, "grant_type": {"authorization_code"}, "redirect_uri": {exchange.RedirectURI}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return integrationauthorization.RefreshCredential{}, integrationauthorization.ErrInvalid
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.client.Do(request)
	if err != nil {
		return integrationauthorization.RefreshCredential{}, integrationauthorization.ErrProviderUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		drain(response.Body)
		if response.StatusCode >= 400 && response.StatusCode < 500 {
			return integrationauthorization.RefreshCredential{}, integrationauthorization.ErrProviderRejected
		}
		return integrationauthorization.RefreshCredential{}, integrationauthorization.ErrProviderUnavailable
	}
	var result struct {
		AccessToken  string `json:"access_token"`
		ExpiresIn    int64  `json:"expires_in"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
	}
	if err := decodeJSON(response.Body, &result); err != nil || !validSecret(result.AccessToken, maximumProtocolValueBytes) ||
		!validSecret(result.RefreshToken, maximumProtocolValueBytes) || result.ExpiresIn <= 0 || !strings.EqualFold(result.TokenType, "Bearer") {
		wipeString(&result.AccessToken)
		wipeString(&result.RefreshToken)
		wipeString(&result.IDToken)
		return integrationauthorization.RefreshCredential{}, integrationauthorization.ErrProviderUnavailable
	}
	defer wipeString(&result.AccessToken)
	defer wipeString(&result.IDToken)
	scopes := strings.Fields(result.Scope)
	if len(scopes) != 1 || scopes[0] != domain.GoogleDriveReadScope {
		wipeString(&result.RefreshToken)
		return integrationauthorization.RefreshCredential{}, integrationauthorization.ErrScopeMismatch
	}
	credential := integrationauthorization.RefreshCredential{RefreshToken: []byte(result.RefreshToken)}
	wipeString(&result.RefreshToken)
	return credential, nil
}

func (client *Client) Revoke(ctx context.Context, refreshToken []byte) error {
	if client == nil || client.client == nil || ctx == nil || ctx.Err() != nil || !validSecretBytes(refreshToken, maximumProtocolValueBytes) {
		return integrationauthorization.ErrInvalid
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.revokeEndpoint, strings.NewReader(url.Values{"token": {string(refreshToken)}}.Encode()))
	if err != nil {
		return integrationauthorization.ErrInvalid
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.client.Do(request)
	if err != nil {
		return integrationauthorization.ErrProviderUnavailable
	}
	defer response.Body.Close()
	drain(response.Body)
	if response.StatusCode >= 200 && response.StatusCode < 300 || response.StatusCode == http.StatusBadRequest {
		return nil
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		return integrationauthorization.ErrProviderRejected
	}
	return integrationauthorization.ErrProviderUnavailable
}

func validRedirect(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" || redirectPathTraversal(parsed.EscapedPath()) {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" {
		return false
	}
	host := parsed.Hostname()
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func redirectPathTraversal(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." || strings.EqualFold(segment, "%2e") || strings.EqualFold(segment, "%2e%2e") {
			return true
		}
	}
	return false
}

func validPKCEValue(value []byte) bool {
	return len(value) >= 43 && len(value) <= 128 && protocolValue.Match(value)
}

func validAuthorizationCode(value []byte) bool {
	if len(value) < 16 || len(value) > 2048 {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func validSecret(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func validSecretBytes(value []byte, maximum int) bool {
	return len(value) > 0 && len(value) <= maximum && !slices.Contains(value, byte(0)) && !slices.Contains(value, byte('\r')) && !slices.Contains(value, byte('\n'))
}

func decodeJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maximumResponseBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return requireEnd(decoder)
}

func requireEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return integrationauthorization.ErrProviderUnavailable
	}
	return nil
}

func drain(reader io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(reader, maximumResponseBytes))
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func wipeString(value *string) {
	if value == nil {
		return
	}
	material := []byte(*value)
	wipe(material)
	*value = ""
}

var _ integrationauthorization.Provider = (*Client)(nil)
