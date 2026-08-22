package cellapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type knowledgeServiceStub struct {
	register func(context.Context, knowledgeapp.RegisterEvidenceCommand) (knowledgedomain.Evidence, error)
	propose  func(context.Context, knowledgeapp.ProposeClaimCommand) (knowledgedomain.Claim, error)
	decide   func(context.Context, knowledgeapp.DecideClaimCommand) (knowledgedomain.Claim, *knowledgedomain.Fact, error)
	get      func(context.Context, access.Actor, ids.AccountID, ids.KnowledgeClaimID) (knowledgedomain.Claim, error)
	list     func(context.Context, access.Actor, ids.AccountID, knowledgeapp.FactListQuery) (knowledgeapp.FactPage, error)
}

func (s knowledgeServiceStub) RegisterEvidence(ctx context.Context, command knowledgeapp.RegisterEvidenceCommand) (knowledgedomain.Evidence, error) {
	return s.register(ctx, command)
}
func (s knowledgeServiceStub) ProposeClaim(ctx context.Context, command knowledgeapp.ProposeClaimCommand) (knowledgedomain.Claim, error) {
	return s.propose(ctx, command)
}
func (s knowledgeServiceStub) DecideClaim(ctx context.Context, command knowledgeapp.DecideClaimCommand) (knowledgedomain.Claim, *knowledgedomain.Fact, error) {
	return s.decide(ctx, command)
}
func (s knowledgeServiceStub) GetClaim(ctx context.Context, actor access.Actor, accountID ids.AccountID, claimID ids.KnowledgeClaimID) (knowledgedomain.Claim, error) {
	return s.get(ctx, actor, accountID, claimID)
}
func (s knowledgeServiceStub) ListFacts(ctx context.Context, actor access.Actor, accountID ids.AccountID, query knowledgeapp.FactListQuery) (knowledgeapp.FactPage, error) {
	return s.list(ctx, actor, accountID, query)
}

func newKnowledgeServer(t *testing.T, service KnowledgeService) *Server {
	t.Helper()
	server, err := New(claimAcceptor{claims: attentionClaims("knowledge")}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithKnowledge(service))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestKnowledgeEvidenceRegistrationBindsRoutedIdentity(t *testing.T) {
	digest := sha256.Sum256([]byte("owner statement"))
	service := knowledgeServiceStub{register: func(ctx context.Context, command knowledgeapp.RegisterEvidenceCommand) (knowledgedomain.Evidence, error) {
		claims, ok := routecontext.FromContext(ctx)
		if !ok || claims.Authority.AccountID != attentionAccount || command.Actor.UserID != attentionUser || command.AccountID != attentionAccount || string(command.EvidenceID) != attentionOperation || command.CorrelationID != attentionOperation || command.ContentSHA256 != digest {
			t.Fatalf("claims=%+v command=%+v", claims, command)
		}
		return knowledgedomain.Evidence{ID: command.EvidenceID, AccountID: command.AccountID, Kind: command.Kind, SourceReference: command.SourceReference, SourceRevision: command.SourceRevision, ContentSHA256: command.ContentSHA256, CapturedAt: command.CapturedAt, CreatedBy: knowledgedomain.Actor{Kind: knowledgedomain.ActorUser, ID: string(attentionUser)}, CreatedAt: command.CapturedAt}, nil
	}}
	body := `{"source_kind":"owner_statement","source_reference":"membership:` + string(attentionUser) + `","source_revision":"1","content_sha256":"` + fmtDigest(digest) + `","captured_at":"2026-08-21T23:00:00Z"}`
	response := attentionMutation(t, newKnowledgeServer(t, service).Handler(), http.MethodPost, "/api/v1/accounts/"+attentionAccount+"/knowledge/evidence", body, attentionOperation, "")
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"source_kind":"owner_statement"`) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestKnowledgeClaimProposalPreservesCanonicalValueAndCitations(t *testing.T) {
	service := knowledgeServiceStub{propose: func(_ context.Context, command knowledgeapp.ProposeClaimCommand) (knowledgedomain.Claim, error) {
		if command.Scope.Kind != knowledgedomain.ScopeAccount || command.Key != "organization.name" || string(command.CanonicalValue) != `{"name":"Northstar"}` || len(command.Citations) != 1 {
			t.Fatalf("command=%+v value=%s", command, command.CanonicalValue)
		}
		return knowledgedomain.NewClaim(knowledgedomain.ClaimDraft{ID: command.ClaimID, AccountID: command.AccountID, Scope: command.Scope, Key: command.Key, CanonicalValue: command.CanonicalValue, Confidence: command.Confidence, Sensitivity: command.Sensitivity, Citations: command.Citations, ProposedBy: knowledgedomain.Actor{Kind: knowledgedomain.ActorUser, ID: string(attentionUser)}}, time.Date(2026, 8, 21, 23, 0, 0, 0, time.UTC))
	}}
	body := `{"scope":{"kind":"account"},"key":"organization.name","value":{"name":"Northstar"},"confidence":950,"sensitivity":"internal","citations":[{"evidence_id":"` + attentionFact + `","evidence_kind":"owner_statement","relation":"supports","locator":"statement"}]}`
	response := attentionMutation(t, newKnowledgeServer(t, service).Handler(), http.MethodPost, "/api/v1/accounts/"+attentionAccount+"/knowledge/claims", body, attentionOperation, "")
	if response.Code != http.StatusCreated || response.Header().Get("ETag") != `W/"1"` || !strings.Contains(response.Body.String(), `"scope":{"kind":"account"}`) || strings.Contains(response.Body.String(), `"Kind"`) {
		t.Fatalf("response=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestKnowledgeDecisionAndFactListUseVersionAndOpaqueCursor(t *testing.T) {
	now := time.Date(2026, 8, 21, 23, 0, 0, 0, time.UTC)
	claimID := ids.KnowledgeClaimID(attentionFact)
	claim := knowledgeClaimFixture(t, claimID, now)
	accepted, err := claim.Decide(knowledgedomain.DecideClaimCommand{Accept: true, Reason: "Owner evidence reviewed", Actor: knowledgedomain.Actor{Kind: knowledgedomain.ActorUser, ID: string(attentionUser)}, Role: "owner", ExpectedVersion: 1, At: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	fact, err := knowledgedomain.NewFact(ids.KnowledgeFactID(attentionWork), accepted)
	if err != nil {
		t.Fatal(err)
	}
	service := knowledgeServiceStub{
		decide: func(_ context.Context, command knowledgeapp.DecideClaimCommand) (knowledgedomain.Claim, *knowledgedomain.Fact, error) {
			if command.ClaimID != claimID || command.ExpectedVersion != 1 || !command.Accept || command.CorrelationID != attentionOperation {
				t.Fatalf("command=%+v", command)
			}
			return accepted, &fact, nil
		},
		list: func(_ context.Context, _ access.Actor, _ ids.AccountID, query knowledgeapp.FactListQuery) (knowledgeapp.FactPage, error) {
			if query.Scope == nil || query.Scope.Kind != knowledgedomain.ScopeAccount || query.KeyPrefix != "organization." || query.Limit != 5 {
				t.Fatalf("query=%+v", query)
			}
			return knowledgeapp.FactPage{Items: []knowledgeapp.FactSummary{{ID: fact.ID, CurrentClaimID: accepted.ID, Scope: fact.Scope, Key: fact.Key, Sensitivity: accepted.Sensitivity, State: fact.State, Revision: fact.Revision, AcceptedAt: fact.AcceptedAt, UpdatedAt: fact.UpdatedAt}}, NextCursor: &knowledgeapp.FactCursor{UpdatedAt: fact.UpdatedAt, ID: fact.ID}}, nil
		},
	}
	server := newKnowledgeServer(t, service).Handler()
	decision := attentionMutation(t, server, http.MethodPost, "/api/v1/accounts/"+attentionAccount+"/knowledge/claims/"+string(claimID)+"/decisions", `{"accept":true,"reason":"Owner evidence reviewed"}`, attentionOperation, `W/"1"`)
	if decision.Code != http.StatusOK || decision.Header().Get("ETag") != `W/"2"` || !strings.Contains(decision.Body.String(), `"fact"`) {
		t.Fatalf("decision=%d %s", decision.Code, decision.Body.String())
	}
	page := attentionRead(t, server, "/api/v1/accounts/"+attentionAccount+"/knowledge/facts?scope_kind=account&key_prefix=organization.&limit=5")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"next_cursor"`) || !strings.Contains(page.Body.String(), `"current_claim_id"`) {
		t.Fatalf("page=%d %s", page.Code, page.Body.String())
	}
}

func knowledgeClaimFixture(t *testing.T, claimID ids.KnowledgeClaimID, now time.Time) knowledgedomain.Claim {
	t.Helper()
	claim, err := knowledgedomain.NewClaim(knowledgedomain.ClaimDraft{ID: claimID, AccountID: attentionAccount, Scope: knowledgedomain.Scope{Kind: knowledgedomain.ScopeAccount}, Key: "organization.name", CanonicalValue: json.RawMessage(`"Northstar"`), Confidence: 950, Sensitivity: knowledgedomain.SensitivityInternal, Citations: []knowledgedomain.Citation{{EvidenceID: ids.KnowledgeEvidenceID(attentionInvocation), EvidenceKind: knowledgedomain.SourceOwnerStatement, Relation: knowledgedomain.EvidenceSupports, Locator: "statement"}}, ProposedBy: knowledgedomain.Actor{Kind: knowledgedomain.ActorUser, ID: string(attentionUser)}}, now)
	if err != nil {
		t.Fatal(err)
	}
	return claim
}

func fmtDigest(value [sha256.Size]byte) string {
	const hexadecimal = "0123456789abcdef"
	result := make([]byte, sha256.Size*2)
	for index, item := range value {
		result[index*2], result[index*2+1] = hexadecimal[item>>4], hexadecimal[item&15]
	}
	return string(result)
}
