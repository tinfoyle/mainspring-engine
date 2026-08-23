package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// KnowledgeExportObjectReader is deliberately narrower than the Knowledge
// object store. The export worker may read an exact immutable version, but it
// cannot create, replace, enumerate, or delete customer objects.
type KnowledgeExportObjectReader interface {
	Open(context.Context, knowledgeapp.DocumentObjectIdentity) (io.ReadCloser, error)
}

// MarketingExportObjectReader is deliberately narrower than the Marketing
// object store. The candidate revision binds the exact immutable object
// version, length, digest, Account, and media type before any bytes are read.
type MarketingExportObjectReader interface {
	OpenContent(context.Context, marketingdomain.AssetRevision) (io.ReadCloser, error)
}

// LaunchAccountExportSourceFactory composes the reviewed global, cell, and
// exact-version object projections for the launch portability registry.
type LaunchAccountExportSourceFactory struct {
	knowledge KnowledgeExportObjectReader
	marketing MarketingExportObjectReader
}

func NewLaunchAccountExportSourceFactory(knowledge KnowledgeExportObjectReader, marketing MarketingExportObjectReader) (*LaunchAccountExportSourceFactory, error) {
	if knowledge == nil || marketing == nil {
		return nil, accountexport.ErrInvalid
	}
	return &LaunchAccountExportSourceFactory{knowledge: knowledge, marketing: marketing}, nil
}

func (factory *LaunchAccountExportSourceFactory) Sources(_ context.Context, global, cell pgx.Tx, work accountexport.Work) ([]accountexport.SectionSource, []accountexport.ObjectSource, error) {
	if factory == nil || global == nil || cell == nil || ids.Validate(string(work.AccountID)) != nil {
		return nil, nil, accountexport.ErrInvalid
	}
	registry, err := accountexport.LaunchRegistry()
	if err != nil {
		return nil, nil, err
	}
	globalTables := AccountExportGlobalProjectionTables(global)
	cellTables := AccountExportCellProjectionTables(cell)
	sections := make([]accountexport.SectionSource, 0, len(registry.Sections())-2)
	for _, descriptor := range registry.Sections() {
		if descriptor.Code == "knowledge_objects" || descriptor.Code == "marketing_objects" {
			continue
		}
		tables := append([]AccountExportProjectionTable(nil), globalTables[descriptor.Code]...)
		tables = append(tables, cellTables[descriptor.Code]...)
		slices.SortFunc(tables, func(left, right AccountExportProjectionTable) int {
			return strings.Compare(left.Schema+"."+left.Table, right.Schema+"."+right.Table)
		})
		source, sourceErr := NewAccountExportSectionSource(descriptor, work.AccountID, tables)
		if sourceErr != nil {
			return nil, nil, sourceErr
		}
		sections = append(sections, source)
	}
	objects := []accountexport.ObjectSource{
		&knowledgeExportObjectSource{tx: cell, accountID: work.AccountID, reader: factory.knowledge},
		&marketingExportObjectSource{tx: cell, accountID: work.AccountID, reader: factory.marketing},
	}
	return sections, objects, nil
}

type knowledgeExportObjectSource struct {
	tx        pgx.Tx
	accountID ids.AccountID
	reader    KnowledgeExportObjectReader
}

func (source *knowledgeExportObjectSource) Descriptor() accountexport.Descriptor {
	return accountexport.Descriptor{Code: "knowledge_objects", SchemaVersion: 1, Stores: []string{"versioned-object-store"}}
}

func (source *knowledgeExportObjectSource) OpenObjects(ctx context.Context, request accountexport.BuildRequest) (accountexport.ObjectCursor, error) {
	if source == nil || source.tx == nil || source.reader == nil || request.AccountID != source.accountID {
		return nil, accountexport.ErrInvalid
	}
	rows, err := source.tx.Query(ctx, `
		SELECT revision.id,revision.document_id,revision.verified_media_type,revision.byte_size,
			revision.content_sha256,revision.object_key,revision.object_version
		FROM spyglass.knowledge_document_revisions revision
		JOIN spyglass.knowledge_documents document
		  ON document.account_id=revision.account_id AND document.id=revision.document_id
		WHERE revision.account_id=$1 AND revision.state<>'deleted' AND document.state<>'deleted'
		ORDER BY revision.document_id::text COLLATE "C",revision.id::text COLLATE "C"`, source.accountID)
	if err != nil {
		return nil, errors.Join(accountexport.ErrUnavailable, err)
	}
	return &knowledgeExportObjectCursor{rows: rows, reader: source.reader}, nil
}

type knowledgeExportObjectCursor struct {
	rows   pgx.Rows
	reader KnowledgeExportObjectReader
	closed bool
}

func (cursor *knowledgeExportObjectCursor) Next(ctx context.Context) (accountexport.Object, bool, error) {
	if cursor == nil || cursor.closed || cursor.rows == nil {
		return accountexport.Object{}, false, accountexport.ErrInvalid
	}
	if !cursor.rows.Next() {
		if err := cursor.rows.Err(); err != nil {
			return accountexport.Object{}, false, errors.Join(accountexport.ErrUnavailable, err)
		}
		return accountexport.Object{}, false, nil
	}
	var revisionID ids.KnowledgeDocumentRevisionID
	var documentID ids.KnowledgeDocumentID
	var mediaType, objectKey, objectVersion string
	var size int64
	var digestBytes []byte
	if err := cursor.rows.Scan(&revisionID, &documentID, &mediaType, &size, &digestBytes, &objectKey, &objectVersion); err != nil {
		return accountexport.Object{}, false, errors.Join(accountexport.ErrUnavailable, err)
	}
	if ids.Validate(string(revisionID)) != nil || ids.Validate(string(documentID)) != nil || size <= 0 || size > accountexport.MaximumObjectBytes || len(digestBytes) != sha256.Size {
		return accountexport.Object{}, false, accountexport.ErrInvalid
	}
	var digest [sha256.Size]byte
	copy(digest[:], digestBytes)
	identity := knowledgeapp.DocumentObjectIdentity{Key: objectKey, Version: objectVersion, Size: size, ContentSHA256: digest}
	body, err := cursor.reader.Open(ctx, identity)
	if err != nil || body == nil {
		return accountexport.Object{}, false, errors.Join(accountexport.ErrUnavailable, err)
	}
	return accountexport.Object{Path: fmt.Sprintf("documents/%s/revisions/%s/source", documentID, revisionID), MediaType: mediaType, Size: size, SHA256: digest, Body: body}, true, nil
}

func (cursor *knowledgeExportObjectCursor) Close() error {
	if cursor == nil || cursor.closed {
		return nil
	}
	cursor.closed = true
	cursor.rows.Close()
	if err := cursor.rows.Err(); err != nil {
		return errors.Join(accountexport.ErrUnavailable, err)
	}
	return nil
}

type marketingExportObjectSource struct {
	tx        pgx.Tx
	accountID ids.AccountID
	reader    MarketingExportObjectReader
}

func (source *marketingExportObjectSource) Descriptor() accountexport.Descriptor {
	return accountexport.Descriptor{Code: "marketing_objects", SchemaVersion: 1, Stores: []string{"versioned-object-store"}}
}

func (source *marketingExportObjectSource) OpenObjects(ctx context.Context, request accountexport.BuildRequest) (accountexport.ObjectCursor, error) {
	if source == nil || source.tx == nil || source.reader == nil || request.AccountID != source.accountID {
		return nil, accountexport.ErrInvalid
	}
	rows, err := source.tx.Query(ctx, `
		SELECT id,account_id,campaign_id,asset_id,revision,kind,title,media_type,content_reference,
			content_sha256,content_bytes,alternative_text,created_by_kind,created_by_id,origin,
			COALESCE(run_id::text,''),COALESCE(invocation_id::text,''),created_at
		FROM spyglass.marketing_asset_revisions
		WHERE account_id=$1
		ORDER BY campaign_id::text COLLATE "C",asset_id::text COLLATE "C",id::text COLLATE "C"`, source.accountID)
	if err != nil {
		return nil, errors.Join(accountexport.ErrUnavailable, err)
	}
	return &marketingExportObjectCursor{rows: rows, reader: source.reader}, nil
}

type marketingExportObjectCursor struct {
	rows   pgx.Rows
	reader MarketingExportObjectReader
	closed bool
}

func (cursor *marketingExportObjectCursor) Next(ctx context.Context) (accountexport.Object, bool, error) {
	if cursor == nil || cursor.closed || cursor.rows == nil {
		return accountexport.Object{}, false, accountexport.ErrInvalid
	}
	if !cursor.rows.Next() {
		if err := cursor.rows.Err(); err != nil {
			return accountexport.Object{}, false, errors.Join(accountexport.ErrUnavailable, err)
		}
		return accountexport.Object{}, false, nil
	}
	var revision marketingdomain.AssetRevision
	var digestBytes []byte
	if err := cursor.rows.Scan(&revision.ID, &revision.AccountID, &revision.CampaignID, &revision.AssetID, &revision.Revision,
		&revision.Kind, &revision.Title, &revision.MediaType, &revision.ContentReference, &digestBytes, &revision.ContentBytes,
		&revision.AlternativeText, &revision.CreatedBy.Kind, &revision.CreatedBy.ID, &revision.Provenance.Origin,
		&revision.Provenance.RunID, &revision.Provenance.InvocationID, &revision.CreatedAt); err != nil {
		return accountexport.Object{}, false, errors.Join(accountexport.ErrUnavailable, err)
	}
	if len(digestBytes) != sha256.Size {
		return accountexport.Object{}, false, accountexport.ErrInvalid
	}
	copy(revision.ContentSHA256[:], digestBytes)
	revision, err := marketingdomain.RestoreAssetRevision(revision)
	if err != nil || revision.ContentBytes > uint64(accountexport.MaximumObjectBytes) {
		return accountexport.Object{}, false, accountexport.ErrInvalid
	}
	body, err := cursor.reader.OpenContent(ctx, revision)
	if err != nil || body == nil {
		return accountexport.Object{}, false, errors.Join(accountexport.ErrUnavailable, err)
	}
	return accountexport.Object{Path: fmt.Sprintf("campaigns/%s/assets/%s/revisions/%s/content", revision.CampaignID, revision.AssetID, revision.ID),
		MediaType: revision.MediaType, Size: int64(revision.ContentBytes), SHA256: revision.ContentSHA256, Body: body}, true, nil
}

func (cursor *marketingExportObjectCursor) Close() error {
	if cursor == nil || cursor.closed {
		return nil
	}
	cursor.closed = true
	cursor.rows.Close()
	if err := cursor.rows.Err(); err != nil {
		return errors.Join(accountexport.ErrUnavailable, err)
	}
	return nil
}

var _ AccountExportSourceFactory = (*LaunchAccountExportSourceFactory)(nil)
var _ accountexport.ObjectSource = (*knowledgeExportObjectSource)(nil)
var _ accountexport.ObjectSource = (*marketingExportObjectSource)(nil)
var _ accountexport.ObjectCursor = (*knowledgeExportObjectCursor)(nil)
var _ accountexport.ObjectCursor = (*marketingExportObjectCursor)(nil)
