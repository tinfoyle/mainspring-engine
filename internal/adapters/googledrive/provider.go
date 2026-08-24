// Package googledrive implements the bounded Google Drive v3 source boundary.
// It accepts only broker-leased refresh-token material, keeps OAuth and Drive
// origins fixed, rejects redirects, and returns provider content only for
// direct children of the exact folders frozen into a source grant.
package googledrive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationsync"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
)

const (
	productionTokenEndpoint = "https://oauth2.googleapis.com/token"
	productionDriveEndpoint = "https://www.googleapis.com/drive/v3"
	defaultPageSize         = 2
	maximumPageSize         = 4
	maximumDriveFolders     = 50
	maximumJSONBytes        = 1 << 20
	maximumTokenBytes       = 16 << 10
	maximumExportBytes      = 10 << 20
	cursorVersion           = 1
	ProviderCode            = "google_oauth"
)

var (
	ErrConfiguration = errors.New("Google Drive provider configuration is invalid")
	ErrCredential    = errors.New("Google Drive credential is invalid")
	ErrProvider      = errors.New("Google Drive provider is unavailable")
	ErrCursor        = errors.New("Google Drive cursor is invalid")
)

type Config struct {
	Client       *http.Client
	PageSize     int
	ClientID     string
	ClientSecret string
}

type Provider struct {
	client                  *http.Client
	tokenEndpoint, driveAPI string
	clientID, clientSecret  string
	pageSize                int
}

type credential struct {
	RefreshToken string `json:"refresh_token"`
}

type oauthClientCredential struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type syncCursor struct {
	Version    int    `json:"v"`
	Phase      string `json:"phase"`
	StartToken string `json:"start_token,omitempty"`
	Folder     int    `json:"folder,omitempty"`
	PageToken  string `json:"page_token,omitempty"`
}

type driveFile struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	MediaType    string   `json:"mimeType"`
	Parents      []string `json:"parents"`
	Trashed      bool     `json:"trashed"`
	Version      string   `json:"version"`
	Size         string   `json:"size"`
	Capabilities struct {
		CanDownload     bool `json:"canDownload"`
		CanListChildren bool `json:"canListChildren"`
	} `json:"capabilities"`
	ShortcutDetails struct {
		TargetID       string `json:"targetId"`
		TargetMimeType string `json:"targetMimeType"`
	} `json:"shortcutDetails"`
}

type fileList struct {
	Files            []driveFile `json:"files"`
	NextPageToken    string      `json:"nextPageToken"`
	IncompleteSearch bool        `json:"incompleteSearch"`
}

type driveChange struct {
	Removed    bool      `json:"removed"`
	FileID     string    `json:"fileId"`
	ChangeType string    `json:"changeType"`
	File       driveFile `json:"file"`
}

type changeList struct {
	Changes           []driveChange `json:"changes"`
	NextPageToken     string        `json:"nextPageToken"`
	NewStartPageToken string        `json:"newStartPageToken"`
}

func New(config Config) (*Provider, error) {
	return newProvider(config, productionTokenEndpoint, productionDriveEndpoint, false)
}

func NewFromClientFile(config Config, filename string) (*Provider, error) {
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
		return nil, fmt.Errorf("%w: open OAuth client file", ErrConfiguration)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o037 != 0 {
		return nil, fmt.Errorf("%w: OAuth client file permissions", ErrConfiguration)
	}
	raw, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil || len(raw) == 0 || len(raw) > 16<<10 {
		wipe(raw)
		return nil, fmt.Errorf("%w: OAuth client file size", ErrConfiguration)
	}
	defer wipe(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value oauthClientCredential
	if err := decoder.Decode(&value); err != nil || requireJSONEnd(decoder) != nil || !validSecret(value.ClientID, 4096) || !validSecret(value.ClientSecret, 8192) {
		return nil, fmt.Errorf("%w: OAuth client file content", ErrConfiguration)
	}
	config.ClientID, config.ClientSecret = value.ClientID, value.ClientSecret
	return New(config)
}

func newProvider(config Config, tokenEndpoint, driveEndpoint string, allowHTTP bool) (*Provider, error) {
	if config.PageSize == 0 {
		config.PageSize = defaultPageSize
	}
	if config.PageSize < 1 || config.PageSize > maximumPageSize || !validSecret(config.ClientID, 4096) || !validSecret(config.ClientSecret, 8192) {
		return nil, ErrConfiguration
	}
	for _, endpoint := range []string{tokenEndpoint, driveEndpoint} {
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
	return &Provider{client: &copyClient, tokenEndpoint: tokenEndpoint, driveAPI: strings.TrimRight(driveEndpoint, "/"),
		clientID: config.ClientID, clientSecret: config.ClientSecret, pageSize: config.PageSize}, nil
}

func (provider *Provider) Sync(ctx context.Context, request integrationsync.ProviderRequest) (integrationsync.ProviderPage, error) {
	if provider == nil || provider.client == nil || ctx == nil || ctx.Err() != nil || request.CredentialProvider != ProviderCode || len(request.FolderIDs) == 0 ||
		len(request.FolderIDs) > 50 || len(request.Cursor) > integrationsync.MaximumCursorBytes {
		return integrationsync.ProviderPage{}, ErrProvider
	}
	for index, folderID := range request.FolderIDs {
		if !validProviderID(folderID, 200) || (index > 0 && request.FolderIDs[index-1] >= folderID) {
			return integrationsync.ProviderPage{}, ErrProvider
		}
	}
	secret, err := decodeCredential(request.Credential)
	if err != nil {
		return integrationsync.ProviderPage{}, err
	}
	defer secret.wipe()
	accessToken, err := provider.refresh(ctx, secret)
	if err != nil {
		return integrationsync.ProviderPage{}, err
	}
	defer wipe(accessToken)
	cursor, initial, err := decodeCursor(request.Cursor, len(request.FolderIDs))
	if err != nil {
		return integrationsync.ProviderPage{}, err
	}
	if initial {
		cursor = syncCursor{Version: cursorVersion, Phase: "crawl"}
		cursor.StartToken, err = provider.startPageToken(ctx, accessToken)
		if err != nil {
			return integrationsync.ProviderPage{}, err
		}
	}
	if cursor.Phase == "crawl" {
		return provider.crawl(ctx, accessToken, request.FolderIDs, cursor)
	}
	return provider.changes(ctx, accessToken, request.FolderIDs, cursor)
}

func (provider *Provider) Probe(ctx context.Context, call integrationhealth.ProbeCall) integrationhealth.ProbeResult {
	if provider == nil || provider.client == nil || ctx == nil || ctx.Err() != nil || call.Claim.ConnectorKind != domain.ConnectorGoogleDrive ||
		call.Claim.CredentialProvider != ProviderCode || len(call.Claim.Capabilities) != 1 || call.Claim.Capabilities[0] != domain.CapabilityDriveRead ||
		len(call.Claim.Scope.DriveFolderIDs) == 0 {
		return driveHealthUnavailable("google_drive_health_connector_mismatch")
	}
	secret, err := decodeCredential(call.Credential)
	if err != nil {
		return driveHealthUnavailable("google_drive_health_credential_invalid")
	}
	defer secret.wipe()
	token, err := provider.refresh(ctx, secret)
	if err != nil {
		return driveHealthUnavailable("google_drive_health_oauth_unavailable")
	}
	defer wipe(token)
	if _, err := provider.startPageToken(ctx, token); err != nil {
		return driveHealthUnavailable("google_drive_health_api_unavailable")
	}
	for _, folderID := range call.Claim.Scope.DriveFolderIDs {
		var folder driveFile
		query := url.Values{"supportsAllDrives": {"true"}, "fields": {"id,mimeType,trashed,capabilities(canListChildren)"}}
		if err := provider.getJSON(ctx, token, "/files/"+url.PathEscape(folderID), query, &folder); err != nil || folder.ID != folderID ||
			folder.MediaType != "application/vnd.google-apps.folder" || folder.Trashed || !folder.Capabilities.CanListChildren {
			return driveHealthUnavailable("google_drive_health_scope_unavailable")
		}
	}
	return integrationhealth.ProbeResult{State: domain.HealthHealthy}
}

func driveHealthUnavailable(code string) integrationhealth.ProbeResult {
	return integrationhealth.ProbeResult{State: domain.HealthUnavailable, ErrorCode: code}
}

func decodeCredential(raw []byte) (credential, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return credential{}, ErrCredential
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value credential
	if err := decoder.Decode(&value); err != nil || requireJSONEnd(decoder) != nil || !validSecret(value.RefreshToken, 32<<10) {
		value.wipe()
		return credential{}, ErrCredential
	}
	return value, nil
}

func (value *credential) wipe() {
	wipe([]byte(value.RefreshToken))
	value.RefreshToken = ""
}

func decodeCursor(raw []byte, folderCount int) (syncCursor, bool, error) {
	if len(raw) == 0 {
		return syncCursor{}, true, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value syncCursor
	if err := decoder.Decode(&value); err != nil || requireJSONEnd(decoder) != nil || value.Version != cursorVersion ||
		(value.Phase != "crawl" && value.Phase != "changes") || !validToken(value.PageToken) {
		return syncCursor{}, false, ErrCursor
	}
	if value.Phase == "crawl" {
		if !validTokenRequired(value.StartToken) || value.Folder < 0 || value.Folder >= folderCount {
			return syncCursor{}, false, ErrCursor
		}
	} else if value.StartToken != "" || value.Folder != 0 || !validTokenRequired(value.PageToken) {
		return syncCursor{}, false, ErrCursor
	}
	return value, false, nil
}

func (provider *Provider) refresh(ctx context.Context, value credential) ([]byte, error) {
	form := url.Values{"client_id": {provider.clientID}, "client_secret": {provider.clientSecret}, "refresh_token": {value.RefreshToken}, "grant_type": {"refresh_token"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, ErrProvider
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := provider.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: refresh access token", ErrProvider)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		drain(response.Body)
		return nil, fmt.Errorf("%w: refresh access token status %d", ErrProvider, response.StatusCode)
	}
	var result struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
		Scope       string `json:"scope"`
		IDToken     string `json:"id_token"`
	}
	if err := decodeJSON(response.Body, maximumTokenBytes, &result); err != nil || !validSecret(result.AccessToken, maximumTokenBytes) ||
		(result.TokenType != "" && !strings.EqualFold(result.TokenType, "Bearer")) {
		return nil, fmt.Errorf("%w: invalid access-token response", ErrProvider)
	}
	return []byte(result.AccessToken), nil
}

func (provider *Provider) startPageToken(ctx context.Context, token []byte) (string, error) {
	var result struct {
		StartPageToken string `json:"startPageToken"`
	}
	query := url.Values{"supportsAllDrives": {"true"}}
	if err := provider.getJSON(ctx, token, "/changes/startPageToken", query, &result); err != nil || !validTokenRequired(result.StartPageToken) {
		if err == nil {
			err = ErrProvider
		}
		return "", err
	}
	return result.StartPageToken, nil
}

func (provider *Provider) crawl(ctx context.Context, token []byte, folders []string, cursor syncCursor) (integrationsync.ProviderPage, error) {
	query := url.Values{
		"q":                         {fmt.Sprintf("'%s' in parents and trashed = false", folders[cursor.Folder])},
		"spaces":                    {"drive"},
		"pageSize":                  {strconv.Itoa(provider.pageSize)},
		"includeItemsFromAllDrives": {"true"},
		"supportsAllDrives":         {"true"},
		"fields":                    {fileListFields},
	}
	if cursor.PageToken != "" {
		query.Set("pageToken", cursor.PageToken)
	}
	var result fileList
	if err := provider.getJSON(ctx, token, "/files", query, &result); err != nil {
		return integrationsync.ProviderPage{}, err
	}
	if result.IncompleteSearch || len(result.Files) > provider.pageSize || !validToken(result.NextPageToken) {
		return integrationsync.ProviderPage{}, ErrProvider
	}
	changes := make([]integrationsync.ProviderChange, 0, len(result.Files))
	for index := range result.Files {
		change, admitted, err := provider.captureFile(ctx, token, folders[cursor.Folder], result.Files[index])
		if err != nil {
			wipeChanges(changes)
			return integrationsync.ProviderPage{}, err
		}
		if admitted {
			changes = append(changes, change)
		}
	}
	next := cursor
	if result.NextPageToken != "" {
		next.PageToken = result.NextPageToken
	} else if cursor.Folder+1 < len(folders) {
		next.Folder, next.PageToken = cursor.Folder+1, ""
	} else {
		next = syncCursor{Version: cursorVersion, Phase: "changes", PageToken: cursor.StartToken}
	}
	encoded, err := encodeCursor(next)
	if err != nil {
		wipeChanges(changes)
		return integrationsync.ProviderPage{}, err
	}
	return integrationsync.ProviderPage{Changes: changes, NextCursor: encoded, HasMore: true}, nil
}

func (provider *Provider) changes(ctx context.Context, token []byte, folders []string, cursor syncCursor) (integrationsync.ProviderPage, error) {
	query := url.Values{
		"pageToken":                 {cursor.PageToken},
		"pageSize":                  {strconv.Itoa(provider.pageSize)},
		"spaces":                    {"drive"},
		"includeRemoved":            {"true"},
		"includeCorpusRemovals":     {"true"},
		"restrictToMyDrive":         {"false"},
		"includeItemsFromAllDrives": {"true"},
		"supportsAllDrives":         {"true"},
		"fields":                    {changeListFields},
	}
	var result changeList
	if err := provider.getJSON(ctx, token, "/changes", query, &result); err != nil {
		return integrationsync.ProviderPage{}, err
	}
	if len(result.Changes) > provider.pageSize || !validToken(result.NextPageToken) || !validToken(result.NewStartPageToken) ||
		(result.NextPageToken == "") == (result.NewStartPageToken == "") {
		return integrationsync.ProviderPage{}, ErrProvider
	}
	changes := make([]integrationsync.ProviderChange, 0, len(result.Changes))
	seen := make(map[string]struct{}, len(result.Changes))
	for index := range result.Changes {
		value := result.Changes[index]
		if value.ChangeType != "file" || !validProviderID(value.FileID, integrationsync.MaximumProviderIdentityBytes) {
			continue
		}
		var change integrationsync.ProviderChange
		var admitted bool
		var err error
		if value.Removed || value.File.ID == "" {
			change = removal(value.FileID, cursor.PageToken, index)
			admitted = true
		} else {
			if value.File.ID != value.FileID {
				wipeChanges(changes)
				return integrationsync.ProviderPage{}, ErrProvider
			}
			folder, inScope := exactParent(value.File.Parents, folders)
			if !inScope || value.File.Trashed || !downloadableFile(value.File) {
				change = unavailable(value.FileID, value.File.Version, cursor.PageToken, index)
				admitted = true
			} else {
				change, admitted, err = provider.captureFile(ctx, token, folder, value.File)
				if err == nil && !admitted {
					change = unavailable(value.FileID, value.File.Version, cursor.PageToken, index)
					admitted = true
				}
			}
		}
		if err != nil {
			wipeChanges(changes)
			return integrationsync.ProviderPage{}, err
		}
		if !admitted {
			continue
		}
		key := change.ObjectID + "\x00" + change.RevisionID
		if _, duplicate := seen[key]; duplicate {
			wipe(change.Content)
			continue
		}
		seen[key] = struct{}{}
		changes = append(changes, change)
	}
	nextToken, hasMore := result.NewStartPageToken, false
	if result.NextPageToken != "" {
		nextToken, hasMore = result.NextPageToken, true
	}
	encoded, err := encodeCursor(syncCursor{Version: cursorVersion, Phase: "changes", PageToken: nextToken})
	if err != nil {
		wipeChanges(changes)
		return integrationsync.ProviderPage{}, err
	}
	return integrationsync.ProviderPage{Changes: changes, NextCursor: encoded, HasMore: hasMore}, nil
}

func (provider *Provider) captureFile(ctx context.Context, token []byte, folder string, file driveFile) (integrationsync.ProviderChange, bool, error) {
	if !validProviderID(file.ID, integrationsync.MaximumProviderIdentityBytes) || !validProviderID(file.Version, integrationsync.MaximumProviderIdentityBytes) ||
		file.Trashed || !downloadableFile(file) {
		return integrationsync.ProviderChange{}, false, nil
	}
	parent, exact := exactParent(file.Parents, []string{folder})
	if !exact || parent != folder {
		return integrationsync.ProviderChange{}, false, nil
	}
	filename, mediaType, export, supported := normalizeFile(file)
	if !supported {
		return integrationsync.ProviderChange{}, false, nil
	}
	limit := int64(integrationsync.MaximumChangeBytes)
	resourcePath := "/files/" + url.PathEscape(file.ID)
	query := url.Values{}
	if export {
		resourcePath += "/export"
		query.Set("mimeType", mediaType)
		limit = maximumExportBytes
	} else {
		query.Set("alt", "media")
		query.Set("supportsAllDrives", "true")
		if size, err := strconv.ParseInt(file.Size, 10, 64); file.Size != "" && (err != nil || size <= 0 || size > limit) {
			return integrationsync.ProviderChange{}, false, nil
		}
	}
	body, err := provider.getBytes(ctx, token, resourcePath, query, limit)
	if err != nil {
		return integrationsync.ProviderChange{}, false, err
	}
	return integrationsync.ProviderChange{FolderID: folder, ObjectID: file.ID, RevisionID: "version:" + file.Version,
		Title: normalizeTitle(file.Name), Filename: filename, MediaType: mediaType, Content: body}, true, nil
}

func normalizeFile(file driveFile) (filename, mediaType string, export, ok bool) {
	switch file.MediaType {
	case "application/vnd.google-apps.document", "application/vnd.google-apps.spreadsheet", "application/vnd.google-apps.presentation", "application/vnd.google-apps.drawing":
		return safeFilename(file.Name, ".pdf"), "application/pdf", true, true
	}
	if strings.HasPrefix(file.MediaType, "application/vnd.google-apps.") {
		return "", "", false, false
	}
	filename = safeFilename(file.Name, "")
	expected, accepted := knowledgedomain.ExpectedDocumentMediaType(filename)
	actual := strings.ToLower(strings.TrimSpace(strings.SplitN(file.MediaType, ";", 2)[0]))
	if !accepted || (actual != expected && actual != "application/octet-stream") {
		return "", "", false, false
	}
	return filename, expected, false, true
}

func downloadableFile(file driveFile) bool {
	return file.Capabilities.CanDownload && file.MediaType != "application/vnd.google-apps.folder" &&
		file.MediaType != "application/vnd.google-apps.shortcut"
}

func exactParent(parents, allowed []string) (string, bool) {
	if len(parents) != 1 {
		return "", false
	}
	for _, folder := range allowed {
		if parents[0] == folder {
			return folder, true
		}
	}
	return "", false
}

func removal(objectID, pageToken string, index int) integrationsync.ProviderChange {
	return integrationsync.ProviderChange{ObjectID: objectID, RevisionID: revisionMarker("removed", objectID, pageToken, index), Deleted: true}
}

func unavailable(objectID, version, pageToken string, index int) integrationsync.ProviderChange {
	marker := pageToken
	if validProviderID(version, integrationsync.MaximumProviderIdentityBytes) {
		marker = version
	}
	return integrationsync.ProviderChange{ObjectID: objectID, RevisionID: revisionMarker("unavailable", objectID, marker, index), Deleted: true}
}

func revisionMarker(kind, objectID, token string, index int) string {
	digest := sha256.Sum256([]byte(objectID + "\x00" + token + "\x00" + strconv.Itoa(index)))
	return kind + ":" + hex.EncodeToString(digest[:])
}

func encodeCursor(value syncCursor) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) == 0 || len(raw) > integrationsync.MaximumCursorBytes {
		wipe(raw)
		return nil, ErrCursor
	}
	if _, _, err := decodeCursor(raw, maximumDriveFolders); err != nil {
		wipe(raw)
		return nil, err
	}
	return raw, nil
}

func (provider *Provider) getJSON(ctx context.Context, token []byte, resourcePath string, query url.Values, destination any) error {
	request, err := provider.newDriveRequest(ctx, token, resourcePath, query)
	if err != nil {
		return err
	}
	response, err := provider.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: Drive request", ErrProvider)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		drain(response.Body)
		return fmt.Errorf("%w: Drive status %d", ErrProvider, response.StatusCode)
	}
	if err := decodeJSON(response.Body, maximumJSONBytes, destination); err != nil {
		return fmt.Errorf("%w: invalid Drive response", ErrProvider)
	}
	return nil
}

func (provider *Provider) getBytes(ctx context.Context, token []byte, resourcePath string, query url.Values, limit int64) ([]byte, error) {
	request, err := provider.newDriveRequest(ctx, token, resourcePath, query)
	if err != nil {
		return nil, err
	}
	response, err := provider.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: Drive content request", ErrProvider)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		drain(response.Body)
		return nil, fmt.Errorf("%w: Drive content status %d", ErrProvider, response.StatusCode)
	}
	if response.ContentLength > limit {
		drain(response.Body)
		return nil, fmt.Errorf("%w: Drive content exceeds limit", ErrProvider)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(body) == 0 || int64(len(body)) > limit {
		wipe(body)
		return nil, fmt.Errorf("%w: invalid Drive content", ErrProvider)
	}
	return body, nil
}

func (provider *Provider) newDriveRequest(ctx context.Context, token []byte, resourcePath string, query url.Values) (*http.Request, error) {
	if ctx.Err() != nil || len(token) == 0 || !strings.HasPrefix(resourcePath, "/") || path.Clean(resourcePath) != resourcePath {
		return nil, ErrProvider
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.driveAPI+resourcePath+"?"+query.Encode(), nil)
	if err != nil {
		return nil, ErrProvider
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	request.Header.Set("Accept", "application/json")
	return request, nil
}

func decodeJSON(reader io.Reader, maximum int64, destination any) error {
	raw, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > maximum {
		wipe(raw)
		return ErrProvider
	}
	defer wipe(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return requireJSONEnd(decoder)
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrProvider
	}
	return nil
}

func safeFilename(name, forcedExtension string) string {
	name = strings.Map(func(character rune) rune {
		if character == 0 || character == '/' || character == '\\' || character < 0x20 {
			return '_'
		}
		return character
	}, strings.TrimSpace(name))
	if forcedExtension != "" {
		extension := path.Ext(name)
		name = strings.TrimSuffix(name, extension) + forcedExtension
	}
	if name == "" || name == "." {
		name = "Google Drive document" + forcedExtension
	}
	extension := path.Ext(name)
	base := strings.TrimSuffix(name, extension)
	maximumBase := integrationsync.MaximumFilenameBytes - len(extension)
	base = truncateUTF8(base, maximumBase)
	if base == "" {
		base = "document"
	}
	return base + extension
}

func normalizeTitle(name string) string {
	name = strings.Map(func(character rune) rune {
		if character == 0 || character < 0x20 {
			return ' '
		}
		return character
	}, strings.TrimSpace(name))
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = "Google Drive document"
	}
	return truncateUTF8(name, integrationsync.MaximumTitleBytes)
}

func truncateUTF8(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func validSecret(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func validProviderID(value string, maximum int) bool {
	return validSecret(value, maximum) && !strings.ContainsAny(value, " /\\")
}

func validToken(value string) bool {
	return value == "" || validTokenRequired(value)
}

func validTokenRequired(value string) bool {
	if !validSecret(value, integrationsync.MaximumCursorBytes) || len(value) > 3072 {
		return false
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func drain(reader io.Reader) { _, _ = io.Copy(io.Discard, io.LimitReader(reader, 4096)) }

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func wipeChanges(changes []integrationsync.ProviderChange) {
	for index := range changes {
		wipe(changes[index].Content)
	}
}

const fileFields = "id,name,mimeType,parents,trashed,version,size,capabilities(canDownload),shortcutDetails(targetId,targetMimeType)"
const fileListFields = "nextPageToken,incompleteSearch,files(" + fileFields + ")"
const changeListFields = "nextPageToken,newStartPageToken,changes(removed,fileId,changeType,file(" + fileFields + "))"

var _ integrationsync.Provider = (*Provider)(nil)
var _ integrationhealth.Probe = (*Provider)(nil)
