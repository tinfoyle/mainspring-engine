// Package webresearch owns governed public-web search and read orchestration.
// Provider payloads are untrusted; only validated, source-attributed captures
// enter the ordinary Knowledge document lifecycle.
package webresearch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationcredentials"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumQueryBytes       = 500
	MaximumResults          = 10
	MaximumResultTextBytes  = 1000
	MaximumExcerptBytes     = 12000
	MaximumURLBytes         = 2048
	MaximumProviderTimeout  = 45 * time.Second
	DefaultProviderTimeout  = 30 * time.Second
	ResearchCaptureWorkload = "integration-web-research"
)

var (
	ErrInvalid     = errors.New("web research request is invalid")
	ErrNotFound    = errors.New("web research connection was not found")
	ErrConflict    = errors.New("web research capture conflicts with durable state")
	ErrUnavailable = errors.New("web research is unavailable")
	validMediaType = regexp.MustCompile(`^(text/html|text/plain|application/pdf)$`)
	validProvider  = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`)
)

type Clock interface{ Now() time.Time }

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type ConnectionAuthority struct {
	AccountID                 ids.AccountID
	ConnectionID              ids.IntegrationConnectionID
	ConnectionRevisionID      ids.IntegrationConnectionRevisionID
	ConnectionRevision        uint64
	Scope                     integrationsdomain.ConnectionScope
	CredentialID              ids.IntegrationCredentialID
	CredentialGeneration      uint64
	CredentialProvider        string
	CredentialReferenceSHA256 [sha256.Size]byte
}

func (authority ConnectionAuthority) valid() bool {
	if ids.Validate(string(authority.AccountID)) != nil || ids.Validate(string(authority.ConnectionID)) != nil ||
		ids.Validate(string(authority.ConnectionRevisionID)) != nil || authority.ConnectionRevision == 0 ||
		ids.Validate(string(authority.CredentialID)) != nil || authority.CredentialGeneration == 0 || authority.CredentialReferenceSHA256 == ([sha256.Size]byte{}) {
		return false
	}
	if !validProvider.MatchString(authority.CredentialProvider) {
		return false
	}
	revision, err := integrationsdomain.RestoreConnectionRevision(integrationsdomain.ConnectionRevision{ID: authority.ConnectionRevisionID,
		AccountID: authority.AccountID, ConnectionID: authority.ConnectionID, Revision: authority.ConnectionRevision,
		Capabilities: []integrationsdomain.Capability{integrationsdomain.CapabilityWebResearch}, Scope: authority.Scope,
		CreatedBy: integrationsdomain.Actor{UserID: ids.UserID(authority.ConnectionID)}, CreatedAt: time.Unix(1, 0).UTC()}, integrationsdomain.ConnectorWebResearch)
	return err == nil && revision.Scope.EmailAddress == authority.Scope.EmailAddress && revision.Scope.AudienceReference == authority.Scope.AudienceReference &&
		revision.Scope.HTTPSOrigin == authority.Scope.HTTPSOrigin && revision.Scope.PathPrefix == authority.Scope.PathPrefix &&
		len(revision.Scope.DriveFolderIDs) == 0 && len(authority.Scope.DriveFolderIDs) == 0
}

type Capture struct {
	ID                      ids.WebResearchCaptureID
	AccountID               ids.AccountID
	ConnectionID            ids.IntegrationConnectionID
	ConnectionRevisionID    ids.IntegrationConnectionRevisionID
	ConnectionRevision      uint64
	CredentialID            ids.IntegrationCredentialID
	CredentialGeneration    uint64
	RequestedURLSHA256      [sha256.Size]byte
	CanonicalURL            string
	Title                   string
	Excerpt                 string
	MediaType               string
	ContentSHA256           [sha256.Size]byte
	DocumentID              ids.KnowledgeDocumentID
	DocumentRevisionID      ids.KnowledgeDocumentRevisionID
	RetrievedAt, RecordedAt time.Time
	CreatedBy               access.Actor
}

type Repository interface {
	Resolve(context.Context, ids.AccountID, ids.IntegrationConnectionID, time.Time) (ConnectionAuthority, error)
	GetCapture(context.Context, ids.AccountID, ids.WebResearchCaptureID) (Capture, error)
	RecordCapture(context.Context, Capture) (Capture, error)
}

type SearchRequest struct {
	Query      string
	Limit      int
	Scope      integrationsdomain.ConnectionScope
	Credential []byte
}

type SearchHit struct {
	Title, URL, Description string
	RetrievedAt             time.Time
}

type ReadRequest struct {
	URL        string
	Scope      integrationsdomain.ConnectionScope
	Credential []byte
}

type ReadPage struct {
	CanonicalURL string
	Title        string
	MediaType    string
	Content      []byte
	Excerpt      string
	SHA256       [sha256.Size]byte
	RetrievedAt  time.Time
}

type Provider interface {
	Search(context.Context, SearchRequest) ([]SearchHit, error)
	Read(context.Context, ReadRequest) (ReadPage, error)
}

type KnowledgeDocuments interface {
	GetDetail(context.Context, access.Actor, ids.AccountID, ids.KnowledgeDocumentID) (knowledgeapp.DocumentDetail, error)
}

type KnowledgeAdmission interface {
	Upload(context.Context, knowledgeapp.UploadDocumentCommand) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error)
	UploadRevision(context.Context, knowledgeapp.UploadDocumentRevisionCommand) (knowledgedomain.DocumentRevision, error)
}

type Service struct {
	authorizer Authorizer
	repository Repository
	broker     integrationcredentials.Broker
	provider   Provider
	documents  KnowledgeDocuments
	admission  KnowledgeAdmission
	clock      Clock
	timeout    time.Duration
}

func New(authorizer Authorizer, repository Repository, broker integrationcredentials.Broker, provider Provider,
	documents KnowledgeDocuments, admission KnowledgeAdmission, clock Clock, timeout time.Duration) (*Service, error) {
	if timeout == 0 {
		timeout = DefaultProviderTimeout
	}
	if authorizer == nil || repository == nil || broker == nil || provider == nil || documents == nil || admission == nil || clock == nil ||
		timeout < 100*time.Millisecond || timeout > MaximumProviderTimeout {
		return nil, ErrInvalid
	}
	return &Service{authorizer: authorizer, repository: repository, broker: broker, provider: provider, documents: documents,
		admission: admission, clock: clock, timeout: timeout}, nil
}

type SearchCommand struct {
	Actor        access.Actor
	AccountID    ids.AccountID
	OperationID  string
	ConnectionID ids.IntegrationConnectionID
	Query        string
	Limit        int
}

type SearchResult struct {
	Query string       `json:"query"`
	Items []SearchItem `json:"items"`
}

type SearchItem struct {
	CitationID  string    `json:"citation_id"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Description string    `json:"description,omitempty"`
	RetrievedAt time.Time `json:"retrieved_at"`
}

func (service *Service) Search(ctx context.Context, command SearchCommand) (SearchResult, error) {
	query := strings.TrimSpace(command.Query)
	if !validCommand(command.Actor, command.AccountID, command.OperationID, command.ConnectionID) || query == "" || len(query) > MaximumQueryBytes || !utf8.ValidString(query) {
		return SearchResult{}, ErrInvalid
	}
	limit := command.Limit
	if limit == 0 {
		limit = 5
	}
	if limit < 1 || limit > MaximumResults {
		return SearchResult{}, ErrInvalid
	}
	if _, err := service.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Package: catalog.PackageIntegrations}); err != nil {
		return SearchResult{}, err
	}
	authority, credential, closeLease, err := service.authorityAndCredential(ctx, command.AccountID, command.ConnectionID, command.OperationID)
	if err != nil {
		return SearchResult{}, err
	}
	defer closeLease()
	providerContext, cancel := context.WithTimeout(ctx, service.timeout)
	hits, err := service.provider.Search(providerContext, SearchRequest{Query: query, Limit: limit, Scope: authority.Scope, Credential: credential})
	cancel()
	wipe(credential)
	if err != nil || len(hits) > limit {
		return SearchResult{}, errors.Join(ErrUnavailable, err)
	}
	result := SearchResult{Query: query, Items: make([]SearchItem, 0, len(hits))}
	seen := make(map[string]struct{}, len(hits))
	now := service.clock.Now().UTC()
	for _, hit := range hits {
		canonical, valid := validateProviderURL(hit.URL, authority.Scope)
		if !valid || hit.RetrievedAt.IsZero() || hit.RetrievedAt.After(now.Add(time.Minute)) || !bounded(hit.Title, MaximumResultTextBytes, true) || !bounded(hit.Description, MaximumResultTextBytes, false) {
			return SearchResult{}, ErrUnavailable
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		result.Items = append(result.Items, SearchItem{CitationID: citationID(canonical), Title: hit.Title, URL: canonical,
			Description: hit.Description, RetrievedAt: hit.RetrievedAt.UTC()})
	}
	return result, nil
}

type ReadCommand struct {
	Actor        access.Actor
	AccountID    ids.AccountID
	OperationID  string
	ConnectionID ids.IntegrationConnectionID
	URL          string
}

type ReadResult struct {
	CaptureID          ids.WebResearchCaptureID        `json:"capture_id"`
	CitationID         string                          `json:"citation_id"`
	URL                string                          `json:"url"`
	Title              string                          `json:"title"`
	MediaType          string                          `json:"media_type"`
	ContentSHA256      string                          `json:"content_sha256"`
	DocumentID         ids.KnowledgeDocumentID         `json:"document_id"`
	DocumentRevisionID ids.KnowledgeDocumentRevisionID `json:"document_revision_id"`
	Excerpt            string                          `json:"excerpt,omitempty"`
	RetrievedAt        time.Time                       `json:"retrieved_at"`
}

func (service *Service) Read(ctx context.Context, command ReadCommand) (ReadResult, error) {
	if !validCommand(command.Actor, command.AccountID, command.OperationID, command.ConnectionID) || len(command.URL) == 0 || len(command.URL) > MaximumURLBytes {
		return ReadResult{}, ErrInvalid
	}
	integrationAccess, err := service.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Package: catalog.PackageIntegrations, Mutation: true})
	if err != nil {
		return ReadResult{}, err
	}
	knowledgeAccess, err := service.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Package: catalog.PackageKnowledge, Mutation: true})
	if err != nil {
		return ReadResult{}, err
	}
	if integrationAccess.AccountID != knowledgeAccess.AccountID || integrationAccess.CellID != knowledgeAccess.CellID ||
		integrationAccess.PlacementGeneration != knowledgeAccess.PlacementGeneration || integrationAccess.EntitlementVersion != knowledgeAccess.EntitlementVersion {
		return ReadResult{}, ErrUnavailable
	}
	if existing, existingErr := service.repository.GetCapture(ctx, command.AccountID, ids.WebResearchCaptureID(command.OperationID)); existingErr == nil {
		if existing.ConnectionID != command.ConnectionID || existing.RequestedURLSHA256 != sha256.Sum256([]byte(strings.TrimSpace(command.URL))) {
			return ReadResult{}, ErrConflict
		}
		return captureResult(existing), nil
	} else if !errors.Is(existingErr, ErrNotFound) {
		return ReadResult{}, errors.Join(ErrUnavailable, existingErr)
	}
	authority, credential, closeLease, err := service.authorityAndCredential(ctx, command.AccountID, command.ConnectionID, command.OperationID)
	if err != nil {
		return ReadResult{}, err
	}
	defer closeLease()
	requested, valid := validateProviderURL(command.URL, authority.Scope)
	if !valid {
		wipe(credential)
		return ReadResult{}, ErrInvalid
	}
	providerContext, cancel := context.WithTimeout(ctx, service.timeout)
	page, err := service.provider.Read(providerContext, ReadRequest{URL: requested, Scope: authority.Scope, Credential: credential})
	cancel()
	wipe(credential)
	defer wipe(page.Content)
	canonical, valid := validateProviderURL(page.CanonicalURL, authority.Scope)
	if err != nil || !valid || !validReadPage(page) {
		return ReadResult{}, errors.Join(ErrUnavailable, err)
	}
	documentID, revisionID, err := captureDocumentIDs(command.ConnectionID, canonical, page.SHA256)
	if err != nil {
		return ReadResult{}, ErrInvalid
	}
	actor := command.Actor
	admissionActor := access.Actor{WorkloadID: knowledgeapp.WebResearchCaptureWorkloadID}
	detail, detailErr := service.documents.GetDetail(ctx, actor, command.AccountID, documentID)
	filename := captureFilename(page.MediaType, page.SHA256)
	title := strings.TrimSpace(page.Title)
	if title == "" {
		title = canonical
	}
	title = truncateUTF8Bytes(title, 200)
	if errors.Is(detailErr, knowledgeapp.ErrNotFound) {
		_, revision, uploadErr := service.admission.Upload(ctx, knowledgeapp.UploadDocumentCommand{Actor: admissionActor, AccountID: command.AccountID,
			DocumentID: documentID, RevisionID: revisionID, Title: title, Sensitivity: knowledgedomain.SensitivityInternal,
			Filename: filename, DeclaredType: page.MediaType, Body: bytes.NewReader(page.Content), ChangeSummary: "Initial public web capture",
			CorrelationID: command.OperationID})
		if uploadErr != nil || revision.ID != revisionID || revision.ContentSHA256 != page.SHA256 {
			return ReadResult{}, errors.Join(ErrUnavailable, uploadErr)
		}
	} else if detailErr != nil {
		return ReadResult{}, errors.Join(ErrUnavailable, detailErr)
	} else if detail.LatestRevision.ID != revisionID {
		revision, uploadErr := service.admission.UploadRevision(ctx, knowledgeapp.UploadDocumentRevisionCommand{Actor: admissionActor, AccountID: command.AccountID,
			DocumentID: documentID, RevisionID: revisionID, Filename: filename, DeclaredType: page.MediaType, Body: bytes.NewReader(page.Content),
			ChangeSummary: "Public web source changed", CorrelationID: command.OperationID})
		if uploadErr != nil || revision.ID != revisionID || revision.ContentSHA256 != page.SHA256 {
			return ReadResult{}, errors.Join(ErrUnavailable, uploadErr)
		}
	}
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	capture := Capture{ID: ids.WebResearchCaptureID(command.OperationID), AccountID: command.AccountID, ConnectionID: command.ConnectionID,
		ConnectionRevisionID: authority.ConnectionRevisionID, ConnectionRevision: authority.ConnectionRevision, CredentialID: authority.CredentialID,
		CredentialGeneration: authority.CredentialGeneration, RequestedURLSHA256: sha256.Sum256([]byte(strings.TrimSpace(command.URL))), CanonicalURL: canonical,
		Title: title, Excerpt: page.Excerpt, MediaType: page.MediaType, ContentSHA256: page.SHA256, DocumentID: documentID,
		DocumentRevisionID: revisionID, RetrievedAt: page.RetrievedAt.UTC().Truncate(time.Microsecond), RecordedAt: now, CreatedBy: actor}
	recorded, err := service.repository.RecordCapture(ctx, capture)
	if err != nil || recorded != capture {
		return ReadResult{}, errors.Join(ErrUnavailable, err)
	}
	return captureResult(capture), nil
}

func captureResult(capture Capture) ReadResult {
	return ReadResult{CaptureID: capture.ID, CitationID: citationID(capture.CanonicalURL), URL: capture.CanonicalURL, Title: capture.Title,
		MediaType: capture.MediaType, ContentSHA256: hex.EncodeToString(capture.ContentSHA256[:]), DocumentID: capture.DocumentID,
		DocumentRevisionID: capture.DocumentRevisionID, Excerpt: capture.Excerpt, RetrievedAt: capture.RetrievedAt.UTC()}
}

func (service *Service) authorityAndCredential(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID, operationID string) (ConnectionAuthority, []byte, func(), error) {
	now := service.clock.Now().UTC()
	authority, err := service.repository.Resolve(ctx, accountID, connectionID, now)
	if err != nil || !authority.valid() || authority.AccountID != accountID || authority.ConnectionID != connectionID {
		if errors.Is(err, ErrNotFound) {
			return ConnectionAuthority{}, nil, func() {}, ErrNotFound
		}
		return ConnectionAuthority{}, nil, func() {}, errors.Join(ErrUnavailable, err)
	}
	lease, err := service.broker.Acquire(ctx, integrationcredentials.Request{AccountID: accountID, OperationID: operationID,
		Purpose: integrationcredentials.PurposeResearch, Capability: integrationsdomain.CapabilityWebResearch, ConnectionID: connectionID,
		CredentialID: authority.CredentialID, CredentialGeneration: authority.CredentialGeneration, CredentialProvider: authority.CredentialProvider,
		ReferenceSHA256: authority.CredentialReferenceSHA256, ExpiresAt: now.Add(service.timeout)})
	if err != nil || lease == nil {
		return ConnectionAuthority{}, nil, func() {}, errors.Join(ErrUnavailable, err)
	}
	material := lease.Material()
	if len(material) == 0 || len(material) > 64<<10 {
		_ = lease.Close()
		return ConnectionAuthority{}, nil, func() {}, ErrUnavailable
	}
	credential := append([]byte(nil), material...)
	return authority, credential, func() { wipe(credential); _ = lease.Close() }, nil
}

func validCommand(actor access.Actor, accountID ids.AccountID, operationID string, connectionID ids.IntegrationConnectionID) bool {
	return actor.Valid() && ids.Validate(string(accountID)) == nil && ids.Validate(operationID) == nil && ids.Validate(string(connectionID)) == nil
}

func validateProviderURL(raw string, scope integrationsdomain.ConnectionScope) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	origin, originErr := url.Parse(scope.HTTPSOrigin)
	if err != nil || originErr != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" || len(raw) > MaximumURLBytes {
		return "", false
	}
	parsed.Host = strings.ToLower(strings.TrimSuffix(parsed.Host, "."))
	if parsed.Host != origin.Host {
		return "", false
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	if path.Clean(parsed.Path) != parsed.Path || strings.Contains(parsed.EscapedPath(), "%2f") || strings.Contains(parsed.EscapedPath(), "%2F") || strings.Contains(parsed.EscapedPath(), "%5c") || strings.Contains(parsed.EscapedPath(), "%5C") {
		return "", false
	}
	prefix := scope.PathPrefix
	if prefix != "/" && parsed.Path != prefix && !strings.HasPrefix(parsed.Path, strings.TrimSuffix(prefix, "/")+"/") {
		return "", false
	}
	parsed.Fragment, parsed.RawFragment = "", ""
	return parsed.String(), true
}

func validReadPage(page ReadPage) bool {
	return page.RetrievedAt.After(time.Unix(0, 0)) && validMediaType.MatchString(page.MediaType) && len(page.Content) > 0 && len(page.Content) <= 16<<20 &&
		page.SHA256 == sha256.Sum256(page.Content) && bounded(page.Title, MaximumResultTextBytes, false) && bounded(page.Excerpt, MaximumExcerptBytes, false)
}

func captureDocumentIDs(connectionID ids.IntegrationConnectionID, canonical string, digest [sha256.Size]byte) (ids.KnowledgeDocumentID, ids.KnowledgeDocumentRevisionID, error) {
	urlDigest := sha256.Sum256([]byte(canonical))
	document, err := ids.Derive(string(connectionID), "web-object:"+hex.EncodeToString(urlDigest[:]))
	if err != nil {
		return "", "", err
	}
	revision, err := ids.Derive(document, "web-revision:"+hex.EncodeToString(digest[:]))
	return ids.KnowledgeDocumentID(document), ids.KnowledgeDocumentRevisionID(revision), err
}

func captureFilename(mediaType string, digest [sha256.Size]byte) string {
	extension := ".txt"
	if mediaType == "text/html" {
		extension = ".html"
	} else if mediaType == "application/pdf" {
		extension = ".pdf"
	}
	return "web-capture-" + hex.EncodeToString(digest[:6]) + extension
}

func citationID(canonical string) string {
	digest := sha256.Sum256([]byte(canonical))
	return "web:" + hex.EncodeToString(digest[:8])
}

func bounded(value string, maximum int, required bool) bool {
	return value == strings.TrimSpace(value) && (!required || value != "") && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func truncateUTF8Bytes(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
