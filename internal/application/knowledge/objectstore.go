package knowledge

import (
	"context"
	"crypto/sha256"
	"io"

	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type SourceObjectWrite struct {
	AccountID     ids.AccountID
	DocumentID    ids.KnowledgeDocumentID
	RevisionID    ids.KnowledgeDocumentRevisionID
	MediaType     string
	Size          int64
	ContentSHA256 [sha256.Size]byte
	Body          io.Reader
}

func (value SourceObjectWrite) Key() (string, error) {
	return knowledgedomain.SourceObjectKey(value.AccountID, value.DocumentID, value.RevisionID)
}

type SourceObjectIdentity struct {
	Key           string
	Version       string
	Size          int64
	ContentSHA256 [sha256.Size]byte
}

type SourceObjectWriteResult struct {
	Identity SourceObjectIdentity
	Created  bool
}

type SourceObjectStore interface {
	Verify(context.Context) error
	PutImmutable(context.Context, SourceObjectWrite) (SourceObjectWriteResult, error)
	Open(context.Context, SourceObjectIdentity) (io.ReadCloser, error)
	Delete(context.Context, SourceObjectIdentity) error
}
