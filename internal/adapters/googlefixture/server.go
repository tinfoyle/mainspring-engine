// Package googlefixture provides a deterministic, local-only Google OAuth and
// Drive protocol boundary for Docker certification. It is not composed by any
// production process.
package googlefixture

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

const maximumFormBytes = 32 << 10

var (
	protocolValue = regexp.MustCompile(`^[A-Za-z0-9._~-]{32,256}$`)
	callbackPath  = regexp.MustCompile(`^/api/v1/accounts/[0-9a-f-]{36}/integrations/google/authorization-callback$`)
)

type Config struct {
	ClientID       string
	ClientSecret   string
	RedirectOrigin string
}

type authorizationCode struct {
	challenge, redirectURI string
}

type accessCredential struct{ refreshToken string }

type Server struct {
	clientID, clientSecret, redirectOrigin string
	mu                                     sync.Mutex
	codes                                  map[string]authorizationCode
	refresh                                map[string]bool
	access                                 map[string]accessCredential
	handler                                http.Handler
}

func New(config Config) (*Server, error) {
	config.ClientID, config.ClientSecret, config.RedirectOrigin = strings.TrimSpace(config.ClientID), strings.TrimSpace(config.ClientSecret), strings.TrimRight(strings.TrimSpace(config.RedirectOrigin), "/")
	origin, err := url.Parse(config.RedirectOrigin)
	if config.ClientID == "" || len(config.ClientSecret) < 16 || err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, errors.New("Google fixture configuration is invalid")
	}
	server := &Server{clientID: config.ClientID, clientSecret: config.ClientSecret, redirectOrigin: config.RedirectOrigin,
		codes: make(map[string]authorizationCode), refresh: make(map[string]bool), access: make(map[string]accessCredential)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/ready", func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /o/oauth2/v2/auth", server.authorize)
	mux.HandleFunc("POST /token", server.token)
	mux.HandleFunc("POST /revoke", server.revoke)
	mux.HandleFunc("GET /drive/v3/changes/startPageToken", server.authorized(server.startPageToken))
	mux.HandleFunc("GET /drive/v3/changes", server.authorized(server.changes))
	mux.HandleFunc("GET /drive/v3/files", server.authorized(server.files))
	mux.HandleFunc("GET /drive/v3/files/{fileID}", server.authorized(server.file))
	server.handler = securityHeaders(mux)
	return server, nil
}

func (server *Server) Handler() http.Handler { return server.handler }

func (server *Server) authorize(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	redirectURI := query.Get("redirect_uri")
	if query.Get("client_id") != server.clientID || query.Get("response_type") != "code" || query.Get("scope") != domain.GoogleDriveReadScope ||
		query.Get("access_type") != "offline" || query.Get("prompt") != "consent" || query.Get("include_granted_scopes") != "false" ||
		query.Get("code_challenge_method") != "S256" || !protocolValue.MatchString(query.Get("code_challenge")) || !protocolValue.MatchString(query.Get("state")) ||
		!server.validRedirect(redirectURI) {
		http.Error(writer, "invalid_request", http.StatusBadRequest)
		return
	}
	code, err := randomValue()
	if err != nil {
		http.Error(writer, "fixture_unavailable", http.StatusServiceUnavailable)
		return
	}
	server.mu.Lock()
	server.codes[code] = authorizationCode{challenge: query.Get("code_challenge"), redirectURI: redirectURI}
	server.mu.Unlock()
	location, _ := url.Parse(redirectURI)
	callback := location.Query()
	callback.Set("code", code)
	callback.Set("state", query.Get("state"))
	location.RawQuery = callback.Encode()
	http.Redirect(writer, request, location.String(), http.StatusSeeOther)
}

func (server *Server) token(writer http.ResponseWriter, request *http.Request) {
	if !parseForm(writer, request) || !constantEqual(request.Form.Get("client_id"), server.clientID) || !constantEqual(request.Form.Get("client_secret"), server.clientSecret) {
		writeOAuthError(writer, http.StatusUnauthorized, "invalid_client")
		return
	}
	switch request.Form.Get("grant_type") {
	case "authorization_code":
		server.exchangeCode(writer, request.Form)
	case "refresh_token":
		server.exchangeRefresh(writer, request.Form)
	default:
		writeOAuthError(writer, http.StatusBadRequest, "unsupported_grant_type")
	}
}

func (server *Server) exchangeCode(writer http.ResponseWriter, form url.Values) {
	code, verifier := form.Get("code"), form.Get("code_verifier")
	server.mu.Lock()
	record, ok := server.codes[code]
	if ok {
		delete(server.codes, code)
	}
	server.mu.Unlock()
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	if !ok || !protocolValue.MatchString(verifier) || !constantEqual(challenge, record.challenge) || form.Get("redirect_uri") != record.redirectURI {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_grant")
		return
	}
	refreshToken, err := randomValue()
	accessToken := ""
	if err == nil {
		accessToken, err = randomValue()
	}
	if err != nil || refreshToken == "" || accessToken == "" {
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	server.mu.Lock()
	server.refresh[refreshToken] = false
	server.access[accessToken] = accessCredential{refreshToken: refreshToken}
	server.mu.Unlock()
	writeJSON(writer, http.StatusOK, map[string]any{"access_token": accessToken, "refresh_token": refreshToken, "token_type": "Bearer", "expires_in": 3600, "scope": domain.GoogleDriveReadScope})
}

func (server *Server) exchangeRefresh(writer http.ResponseWriter, form url.Values) {
	refreshToken := form.Get("refresh_token")
	server.mu.Lock()
	revoked, ok := server.refresh[refreshToken]
	server.mu.Unlock()
	if !ok || revoked {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_grant")
		return
	}
	accessToken, err := randomValue()
	if err != nil {
		writeOAuthError(writer, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	server.mu.Lock()
	server.access[accessToken] = accessCredential{refreshToken: refreshToken}
	server.mu.Unlock()
	writeJSON(writer, http.StatusOK, map[string]any{"access_token": accessToken, "token_type": "Bearer", "expires_in": 3600, "scope": domain.GoogleDriveReadScope})
}

func (server *Server) revoke(writer http.ResponseWriter, request *http.Request) {
	if !parseForm(writer, request) {
		return
	}
	token := request.Form.Get("token")
	server.mu.Lock()
	_, ok := server.refresh[token]
	if ok {
		server.refresh[token] = true
	}
	server.mu.Unlock()
	if !ok {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_token")
		return
	}
	writer.WriteHeader(http.StatusOK)
}

func (server *Server) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		server.mu.Lock()
		credential, ok := server.access[token]
		revoked := server.refresh[credential.refreshToken]
		server.mu.Unlock()
		if !ok || revoked {
			writeJSON(writer, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": 401, "message": "Invalid Credentials"}})
			return
		}
		next(writer, request)
	}
}

func (*Server) startPageToken(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"startPageToken": "fixture-page-1"})
}

func (*Server) changes(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"changes": []any{}, "newStartPageToken": "fixture-page-2"})
}

func (*Server) files(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"files": []any{driveFile("fixture-file-1", "Local operating plan.txt", "text/plain", "folder-a", true)}})
}

func (*Server) file(writer http.ResponseWriter, request *http.Request) {
	switch request.PathValue("fileID") {
	case "folder-a":
		writeJSON(writer, http.StatusOK, driveFile("folder-a", "Local fixture folder", "application/vnd.google-apps.folder", "", false))
	case "fixture-file-1":
		if request.URL.Query().Get("alt") == "media" {
			writer.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(writer, "Local Google Drive fixture document.\n")
			return
		}
		writeJSON(writer, http.StatusOK, driveFile("fixture-file-1", "Local operating plan.txt", "text/plain", "folder-a", true))
	default:
		writeJSON(writer, http.StatusNotFound, map[string]any{"error": map[string]any{"code": 404, "message": "File not found"}})
	}
}

func (server *Server) validRedirect(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme+"://"+parsed.Host == server.redirectOrigin && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && callbackPath.MatchString(parsed.Path)
}

func driveFile(id, name, mediaType, parent string, downloadable bool) map[string]any {
	parents := []string{}
	if parent != "" {
		parents = append(parents, parent)
	}
	return map[string]any{"id": id, "name": name, "mimeType": mediaType, "parents": parents, "trashed": false, "version": "1", "size": "37",
		"capabilities": map[string]any{"canDownload": downloadable, "canListChildren": mediaType == "application/vnd.google-apps.folder"}}
}

func parseForm(writer http.ResponseWriter, request *http.Request) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maximumFormBytes)
	if request.ParseForm() != nil {
		writeOAuthError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func randomValue() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func constantEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func writeOAuthError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"error": code})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(writer, request)
	})
}
