package gdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	"github.com/tinfoyle/mainspring-engine/internal/secretbox"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const driveReadOnlyScope = "https://www.googleapis.com/auth/drive.readonly"

var ErrNotConfigured = errors.New("Google Drive OAuth is not configured")

type Folder struct {
	ID   string
	Name string
}

type File struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	MimeType     string    `json:"mimeType"`
	ModifiedTime time.Time `json:"modifiedTime"`
	WebViewLink  string    `json:"webViewLink"`
	Size         int64     `json:"size,string"`
}

type Download struct {
	File      File
	Filename  string
	MediaType string
	Content   []byte
}

type Service struct {
	pool     *pgxpool.Pool
	tenantID domain.TenantID
	box      *secretbox.Box
	oauth    oauth2.Config
	enabled  bool
}

func NewService(pool *pgxpool.Pool, tenantID domain.TenantID, box *secretbox.Box, clientID, clientSecret, redirectURL string) *Service {
	clientID, clientSecret, redirectURL = strings.TrimSpace(clientID), strings.TrimSpace(clientSecret), strings.TrimSpace(redirectURL)
	return &Service{
		pool: pool, tenantID: tenantID, box: box, enabled: clientID != "" && clientSecret != "" && redirectURL != "",
		oauth: oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL, Endpoint: google.Endpoint, Scopes: []string{driveReadOnlyScope}},
	}
}

func (s *Service) Enabled() bool { return s != nil && s.enabled }

func (s *Service) AuthorizationURL(state string) (string, error) {
	if !s.Enabled() {
		return "", ErrNotConfigured
	}
	return s.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce), nil
}

func (s *Service) Exchange(ctx context.Context, code string) error {
	if !s.Enabled() {
		return ErrNotConfigured
	}
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 4096 {
		return errors.New("Google Drive authorization code is invalid")
	}
	token, err := s.oauth.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("exchange Google Drive authorization: %w", err)
	}
	return s.saveToken(ctx, token)
}

func (s *Service) Disconnect(ctx context.Context) error {
	if s == nil {
		return ErrNotConfigured
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE baseline_data_sources SET status='disconnected',credential_ciphertext=NULL,scope='{"read_only":true,"folders":[]}'::jsonb,
		       cursor='',last_error=NULL,updated_at=now()
		WHERE tenant_id=$1 AND source_type='google_drive'
	`, s.tenantID.String())
	return err
}

func (s *Service) Folders(ctx context.Context) ([]Folder, error) {
	client, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	values := url.Values{
		"q":        {"mimeType='application/vnd.google-apps.folder' and trashed=false"},
		"pageSize": {"1000"},
		"orderBy":  {"name_natural"},
		"fields":   {"files(id,name),nextPageToken"},
	}
	var response struct {
		Files         []Folder `json:"files"`
		NextPageToken string   `json:"nextPageToken"`
	}
	if err := driveJSON(ctx, client, "https://www.googleapis.com/drive/v3/files?"+values.Encode(), &response); err != nil {
		return nil, err
	}
	return response.Files, nil
}

func (s *Service) Files(ctx context.Context, folderIDs []string) ([]File, error) {
	folderIDs = canonicalIDs(folderIDs, 50)
	if len(folderIDs) == 0 {
		return nil, errors.New("select at least one Google Drive folder")
	}
	client, err := s.client(ctx)
	if err != nil {
		return nil, err
	}
	var result []File
	for _, folderID := range folderIDs {
		pageToken := ""
		for len(result) < 500 {
			values := url.Values{
				"q":        {fmt.Sprintf("'%s' in parents and trashed=false", strings.ReplaceAll(folderID, "'", "\\'"))},
				"pageSize": {"100"},
				"orderBy":  {"modifiedTime desc"},
				"fields":   {"files(id,name,mimeType,modifiedTime,webViewLink,size),nextPageToken"},
			}
			if pageToken != "" {
				values.Set("pageToken", pageToken)
			}
			var response struct {
				Files         []File `json:"files"`
				NextPageToken string `json:"nextPageToken"`
			}
			if err := driveJSON(ctx, client, "https://www.googleapis.com/drive/v3/files?"+values.Encode(), &response); err != nil {
				return nil, err
			}
			for _, file := range response.Files {
				if file.MimeType != "application/vnd.google-apps.folder" {
					result = append(result, file)
				}
			}
			pageToken = response.NextPageToken
			if pageToken == "" {
				break
			}
		}
	}
	return result, nil
}

func (s *Service) Download(ctx context.Context, file File) (Download, error) {
	if strings.TrimSpace(file.ID) == "" {
		return Download{}, errors.New("Google Drive file ID is required")
	}
	if file.Size > 15<<20 {
		return Download{}, errors.New("Google Drive file exceeds the 15 MB ingestion limit")
	}
	client, err := s.client(ctx)
	if err != nil {
		return Download{}, err
	}
	mediaType, filename, endpoint := file.MimeType, file.Name, ""
	switch file.MimeType {
	case "application/vnd.google-apps.document":
		mediaType, filename = "text/plain", file.Name+".txt"
		endpoint = "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(file.ID) + "/export?mimeType=" + url.QueryEscape(mediaType)
	case "application/vnd.google-apps.spreadsheet":
		mediaType, filename = "text/csv", file.Name+".csv"
		endpoint = "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(file.ID) + "/export?mimeType=" + url.QueryEscape(mediaType)
	case "application/vnd.google-apps.presentation":
		mediaType, filename = "application/pdf", file.Name+".pdf"
		endpoint = "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(file.ID) + "/export?mimeType=" + url.QueryEscape(mediaType)
	default:
		endpoint = "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(file.ID) + "?alt=media"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Download{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return Download{}, fmt.Errorf("download Google Drive file: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return Download{}, fmt.Errorf("Google Drive download returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, (15<<20)+1))
	if err != nil || len(content) == 0 || len(content) > 15<<20 {
		return Download{}, errors.New("Google Drive file is empty or exceeds the 15 MB ingestion limit")
	}
	return Download{File: file, Filename: filename, MediaType: mediaType, Content: content}, nil
}

func (s *Service) client(ctx context.Context) (*http.Client, error) {
	if !s.Enabled() {
		return nil, ErrNotConfigured
	}
	token, err := s.loadToken(ctx)
	if err != nil {
		return nil, err
	}
	refreshed, err := s.oauth.TokenSource(ctx, token).Token()
	if err != nil {
		return nil, fmt.Errorf("refresh Google Drive authorization: %w", err)
	}
	if refreshed.AccessToken != token.AccessToken || refreshed.RefreshToken != token.RefreshToken || !refreshed.Expiry.Equal(token.Expiry) {
		if refreshed.RefreshToken == "" {
			refreshed.RefreshToken = token.RefreshToken
		}
		if err := s.saveToken(ctx, refreshed); err != nil {
			return nil, err
		}
	}
	return s.oauth.Client(ctx, refreshed), nil
}

func (s *Service) loadToken(ctx context.Context) (*oauth2.Token, error) {
	var ciphertext []byte
	err := s.pool.QueryRow(ctx, `
		SELECT credential_ciphertext FROM baseline_data_sources
		WHERE tenant_id=$1 AND source_type='google_drive' AND status='connected' AND credential_ciphertext IS NOT NULL
	`, s.tenantID.String()).Scan(&ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, err
	}
	plaintext, err := s.box.Decrypt(string(ciphertext))
	if err != nil {
		return nil, err
	}
	var token oauth2.Token
	if err := json.Unmarshal([]byte(plaintext), &token); err != nil {
		return nil, errors.New("decode Google Drive authorization token")
	}
	return &token, nil
}

func (s *Service) saveToken(ctx context.Context, token *oauth2.Token) error {
	encoded, err := json.Marshal(token)
	if err != nil {
		return err
	}
	ciphertext, err := s.box.Encrypt(string(encoded))
	if err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE baseline_data_sources
		SET credential_ciphertext=$2,status='connected',last_error=NULL,updated_at=now()
		WHERE tenant_id=$1 AND source_type='google_drive'
	`, s.tenantID.String(), []byte(ciphertext))
	if err != nil {
		return fmt.Errorf("save Google Drive authorization: %w", err)
	}
	if command.RowsAffected() == 0 {
		return errors.New("Google Drive baseline source is unavailable")
	}
	return nil
}

func driveJSON(ctx context.Context, client *http.Client, endpoint string, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("call Google Drive: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Google Drive returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode Google Drive response: %w", err)
	}
	return nil
}

func canonicalIDs(values []string, maximum int) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 500 || strings.ContainsAny(value, "\r\n\x00") || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
		if len(result) == maximum {
			break
		}
	}
	slices.Sort(result)
	return result
}
