package knowledge

import (
	"context"
	"crypto/sha256"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumDocumentRetrievalQuery = 512
	MaximumDocumentRetrievalLimit = 20
)

type DocumentRetrievalQuery struct {
	DocumentID        ids.KnowledgeDocumentID
	RevisionID        ids.KnowledgeDocumentRevisionID
	AfterChunkIndex   *uint32
	Text              string
	Limit             int
	IncludeRestricted bool
}

type DocumentCitation struct {
	AccountID       ids.AccountID
	DocumentID      ids.KnowledgeDocumentID
	RevisionID      ids.KnowledgeDocumentRevisionID
	ChunkID         ids.KnowledgeDocumentChunkID
	DocumentTitle   string
	Sensitivity     knowledgedomain.Sensitivity
	Revision        uint64
	ChunkIndex      uint32
	StartByte       int64
	EndByte         int64
	Content         string
	ContentSHA256   [sha256.Size]byte
	IndexGeneration string
	Rank            float64
}

type DocumentRetrievalRepository interface {
	RetrieveDocumentChunks(context.Context, ids.AccountID, DocumentRetrievalQuery) ([]DocumentCitation, error)
	GetDocumentCitation(context.Context, ids.AccountID, ids.KnowledgeDocumentID, ids.KnowledgeDocumentRevisionID, ids.KnowledgeDocumentChunkID, bool) (DocumentCitation, error)
}

func (s *DocumentService) Retrieve(ctx context.Context, actor access.Actor, accountID ids.AccountID, query DocumentRetrievalQuery) ([]DocumentCitation, error) {
	if _, ok := domainActor(actor); !ok || ids.Validate(string(accountID)) != nil || !validDocumentRetrievalQuery(query) {
		return nil, ErrInvalid
	}
	accountContext, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: catalog.PackageKnowledge})
	if err != nil {
		return nil, err
	}
	if query.Limit == 0 {
		query.Limit = MaximumDocumentRetrievalLimit
	}
	query.Text = strings.TrimSpace(query.Text)
	query.IncludeRestricted = accountContext.Role == accounts.RoleOwner || accountContext.Role == accounts.RoleAdministrator
	return s.retrieval.RetrieveDocumentChunks(ctx, accountID, query)
}

func (s *DocumentService) GetCitation(ctx context.Context, actor access.Actor, accountID ids.AccountID, documentID ids.KnowledgeDocumentID, revisionID ids.KnowledgeDocumentRevisionID, chunkID ids.KnowledgeDocumentChunkID) (DocumentCitation, error) {
	if _, ok := domainActor(actor); !ok || ids.Validate(string(accountID)) != nil || ids.Validate(string(documentID)) != nil || ids.Validate(string(revisionID)) != nil || ids.Validate(string(chunkID)) != nil {
		return DocumentCitation{}, ErrInvalid
	}
	accountContext, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: catalog.PackageKnowledge})
	if err != nil {
		return DocumentCitation{}, err
	}
	includeRestricted := accountContext.Role == accounts.RoleOwner || accountContext.Role == accounts.RoleAdministrator
	return s.retrieval.GetDocumentCitation(ctx, accountID, documentID, revisionID, chunkID, includeRestricted)
}

func validDocumentRetrievalQuery(query DocumentRetrievalQuery) bool {
	text := strings.TrimSpace(query.Text)
	if query.DocumentID != "" {
		return ids.Validate(string(query.DocumentID)) == nil && ids.Validate(string(query.RevisionID)) == nil &&
			text == "" && query.Limit >= 0 && query.Limit <= MaximumDocumentRetrievalLimit
	}
	if query.RevisionID != "" || query.AfterChunkIndex != nil {
		return false
	}
	return text != "" && len(text) <= MaximumDocumentRetrievalQuery && !strings.ContainsRune(text, '\x00') && query.Limit >= 0 && query.Limit <= MaximumDocumentRetrievalLimit
}
