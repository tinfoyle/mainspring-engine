package cellapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type knowledgeDocumentServiceStub struct {
	upload  func(context.Context, knowledgeapp.UploadDocumentCommand) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error)
	list    func(context.Context, access.Actor, ids.AccountID, knowledgeapp.DocumentListQuery) (knowledgeapp.DocumentPage, error)
	detail  func(context.Context, access.Actor, ids.AccountID, ids.KnowledgeDocumentID) (knowledgeapp.DocumentDetail, error)
	publish func(context.Context, knowledgeapp.PublishDocumentCommand) (knowledgedomain.Document, error)
	delete  func(context.Context, knowledgeapp.DeleteDocumentCommand) (knowledgedomain.Document, error)
}

func (stub knowledgeDocumentServiceStub) Upload(ctx context.Context, command knowledgeapp.UploadDocumentCommand) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error) {
	return stub.upload(ctx, command)
}
func (stub knowledgeDocumentServiceStub) List(ctx context.Context, actor access.Actor, accountID ids.AccountID, query knowledgeapp.DocumentListQuery) (knowledgeapp.DocumentPage, error) {
	return stub.list(ctx, actor, accountID, query)
}
func (stub knowledgeDocumentServiceStub) GetDetail(ctx context.Context, actor access.Actor, accountID ids.AccountID, documentID ids.KnowledgeDocumentID) (knowledgeapp.DocumentDetail, error) {
	return stub.detail(ctx, actor, accountID, documentID)
}
func (stub knowledgeDocumentServiceStub) Publish(ctx context.Context, command knowledgeapp.PublishDocumentCommand) (knowledgedomain.Document, error) {
	return stub.publish(ctx, command)
}
func (stub knowledgeDocumentServiceStub) Delete(ctx context.Context, command knowledgeapp.DeleteDocumentCommand) (knowledgedomain.Document, error) {
	return stub.delete(ctx, command)
}

func TestKnowledgeDocumentUploadUsesMultipartFileAndRoutedIdentity(t *testing.T) {
	content := []byte("trusted upload")
	service := knowledgeDocumentServiceStub{upload: func(_ context.Context, command knowledgeapp.UploadDocumentCommand) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error) {
		actual, err := io.ReadAll(command.Body)
		documentID, _ := ids.Derive(attentionOperation, "knowledge-document")
		revisionID, _ := ids.Derive(attentionOperation, "knowledge-document-revision-1")
		if err != nil || !bytes.Equal(actual, content) || command.AccountID != attentionAccount || command.Actor.UserID != attentionUser || string(command.DocumentID) != documentID || string(command.RevisionID) != revisionID || command.Filename != "plan.txt" || command.CorrelationID != attentionOperation {
			t.Fatalf("command=%+v actual=%q err=%v", command, actual, err)
		}
		document, revision := knowledgeDocumentFixture(t, command.DocumentID, command.RevisionID, content)
		return document, revision, nil
	}}
	server := newKnowledgeDocumentServer(t, service)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("title", "Operating plan")
	_ = writer.WriteField("sensitivity", "internal")
	_ = writer.WriteField("change_summary", "Initial upload")
	part, err := writer.CreateFormFile("file", "plan.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(content)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+attentionAccount+"/knowledge/documents", &body)
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	request.Header.Set("Idempotency-Key", attentionOperation)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("ETag") != `W/"1"` || !strings.Contains(response.Header().Get("Location"), "/knowledge/documents/") || strings.Contains(response.Body.String(), "object_key") {
		t.Fatalf("response=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestKnowledgeDocumentListDetailPublishAndDeleteContracts(t *testing.T) {
	documentID := ids.KnowledgeDocumentID(attentionFact)
	revisionID := ids.KnowledgeDocumentRevisionID(attentionInvocation)
	document, revision := knowledgeDocumentFixture(t, documentID, revisionID, []byte("source"))
	service := knowledgeDocumentServiceStub{
		list: func(_ context.Context, actor access.Actor, accountID ids.AccountID, query knowledgeapp.DocumentListQuery) (knowledgeapp.DocumentPage, error) {
			if actor.UserID != attentionUser || accountID != attentionAccount || query.State != knowledgedomain.DocumentProcessing || query.Limit != 5 {
				t.Fatalf("actor=%+v account=%s query=%+v", actor, accountID, query)
			}
			return knowledgeapp.DocumentPage{Items: []knowledgeapp.DocumentSummary{{ID: document.ID, Title: document.Title, Sensitivity: document.Sensitivity, State: document.State, Version: document.Version, CreatedAt: document.CreatedAt, UpdatedAt: document.UpdatedAt}}}, nil
		},
		detail: func(context.Context, access.Actor, ids.AccountID, ids.KnowledgeDocumentID) (knowledgeapp.DocumentDetail, error) {
			return knowledgeapp.DocumentDetail{Document: document, LatestRevision: revision}, nil
		},
		publish: func(_ context.Context, command knowledgeapp.PublishDocumentCommand) (knowledgedomain.Document, error) {
			if command.DocumentID != document.ID || command.RevisionID != revision.ID || command.ExpectedVersion != 1 || command.CorrelationID != attentionOperation {
				t.Fatalf("publish=%+v", command)
			}
			return document, nil
		},
		delete: func(_ context.Context, command knowledgeapp.DeleteDocumentCommand) (knowledgedomain.Document, error) {
			if command.DocumentID != document.ID || command.ExpectedVersion != 1 || command.CorrelationID != attentionOperation {
				t.Fatalf("delete=%+v", command)
			}
			return document, nil
		},
	}
	handler := newKnowledgeDocumentServer(t, service).Handler()
	list := attentionRead(t, handler, "/api/v1/accounts/"+attentionAccount+"/knowledge/documents?state=processing&limit=5")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Operating plan") || strings.Contains(list.Body.String(), "content_sha256") {
		t.Fatalf("list=%d %s", list.Code, list.Body.String())
	}
	detail := attentionRead(t, handler, "/api/v1/accounts/"+attentionAccount+"/knowledge/documents/"+string(document.ID))
	if detail.Code != http.StatusOK || detail.Header().Get("ETag") != `W/"1"` || !strings.Contains(detail.Body.String(), `"content_sha256"`) || strings.Contains(detail.Body.String(), "object_version") {
		t.Fatalf("detail=%d headers=%v body=%s", detail.Code, detail.Header(), detail.Body.String())
	}
	publish := attentionMutation(t, handler, http.MethodPost, "/api/v1/accounts/"+attentionAccount+"/knowledge/documents/"+string(document.ID)+"/publications", `{"revision_id":"`+string(revision.ID)+`"}`, attentionOperation, `W/"1"`)
	if publish.Code != http.StatusOK {
		t.Fatalf("publish=%d %s", publish.Code, publish.Body.String())
	}
	deleted := attentionMutation(t, handler, http.MethodDelete, "/api/v1/accounts/"+attentionAccount+"/knowledge/documents/"+string(document.ID), "", attentionOperation, `W/"1"`)
	if deleted.Code != http.StatusAccepted {
		t.Fatalf("delete=%d %s", deleted.Code, deleted.Body.String())
	}
}

func newKnowledgeDocumentServer(t *testing.T, service KnowledgeDocumentService) *Server {
	t.Helper()
	server, err := New(claimAcceptor{claims: attentionClaims("knowledge")}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithKnowledgeDocuments(service))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func knowledgeDocumentFixture(t *testing.T, documentID ids.KnowledgeDocumentID, revisionID ids.KnowledgeDocumentRevisionID, body []byte) (knowledgedomain.Document, knowledgedomain.DocumentRevision) {
	t.Helper()
	now := time.Date(2026, 8, 21, 23, 0, 0, 0, time.UTC)
	actor := knowledgedomain.Actor{Kind: knowledgedomain.ActorUser, ID: attentionUser}
	document, err := knowledgedomain.NewDocument(documentID, attentionAccount, "Operating plan", knowledgedomain.SensitivityInternal, nil, actor, now)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := knowledgedomain.SourceObjectKey(attentionAccount, documentID, revisionID)
	revision, err := knowledgedomain.NewDocumentRevision(knowledgedomain.DocumentRevisionDraft{ID: revisionID, DocumentID: documentID, AccountID: attentionAccount, Number: 1, Filename: "plan.txt", DeclaredType: "application/octet-stream", VerifiedType: "text/plain", ByteSize: int64(len(body)), ContentSHA256: sha256.Sum256(body), ObjectKey: key, ObjectVersion: "version-1", ChangeSummary: "Initial upload", CreatedBy: actor}, now)
	if err != nil {
		t.Fatal(err)
	}
	return document, revision
}
