package knowledge

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const knowledgeAccount ids.AccountID = "10000000-0000-4000-8000-000000000001"
const knowledgeUser ids.UserID = "20000000-0000-4000-8000-000000000002"
const knowledgeEvidence ids.KnowledgeEvidenceID = "30000000-0000-4000-8000-000000000003"
const knowledgeClaim ids.KnowledgeClaimID = "40000000-0000-4000-8000-000000000004"
const knowledgeFact ids.KnowledgeFactID = "50000000-0000-4000-8000-000000000005"

func claimFixture(t *testing.T, proposer Actor, evidenceKind SourceKind, value json.RawMessage) Claim {
	t.Helper()
	claim, err := NewClaim(ClaimDraft{ID: knowledgeClaim, AccountID: knowledgeAccount, Scope: Scope{Kind: ScopeAccount}, Key: "organization.legal_name", CanonicalValue: value, Confidence: 900, Sensitivity: SensitivityInternal, Citations: []Citation{{EvidenceID: knowledgeEvidence, EvidenceKind: evidenceKind, Relation: EvidenceSupports, Locator: "statement"}}, ProposedBy: proposer}, time.Date(2026, 8, 21, 21, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return claim
}

func TestEvidenceFreezesExactSourceRevisionAndDigest(t *testing.T) {
	now := time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)
	value, err := NewEvidence(Evidence{ID: knowledgeEvidence, AccountID: knowledgeAccount, Kind: SourceOwnerStatement, SourceReference: "membership:" + string(knowledgeUser), SourceRevision: "1", ContentSHA256: sha256.Sum256([]byte("Northstar LLC")), CapturedAt: now, CreatedBy: Actor{Kind: ActorUser, ID: string(knowledgeUser)}, CreatedAt: now})
	if err != nil || value.SourceRevision != "1" {
		t.Fatalf("evidence=%+v err=%v", value, err)
	}
	value.ContentSHA256 = [32]byte{}
	if _, err := RestoreEvidence(value); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero digest err=%v", err)
	}
}

func TestClaimCanonicalizesJSONAndRejectsDuplicateKeys(t *testing.T) {
	claim := claimFixture(t, Actor{Kind: ActorUser, ID: string(knowledgeUser)}, SourceOwnerStatement, json.RawMessage(`{"b":2.00,"a":"Northstar"}`))
	if string(claim.CanonicalValue) != `{"a":"Northstar","b":2.00}` || claim.ValueSHA256 != sha256.Sum256(claim.CanonicalValue) {
		t.Fatalf("canonical=%s", claim.CanonicalValue)
	}
	_, err := NewClaim(ClaimDraft{ID: knowledgeClaim, AccountID: knowledgeAccount, Scope: Scope{Kind: ScopeAccount}, Key: "organization.name", CanonicalValue: json.RawMessage(`{"a":1,"a":2}`), Confidence: 1000, Sensitivity: SensitivityInternal, Citations: []Citation{{EvidenceID: knowledgeEvidence, EvidenceKind: SourceOwnerStatement, Relation: EvidenceSupports, Locator: "statement"}}, ProposedBy: Actor{Kind: ActorUser, ID: string(knowledgeUser)}}, time.Now())
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate key err=%v", err)
	}
}

func TestClaimCanonicalizesCitationOrder(t *testing.T) {
	secondEvidence := ids.KnowledgeEvidenceID("10000000-0000-4000-8000-000000000009")
	claim, err := NewClaim(ClaimDraft{ID: knowledgeClaim, AccountID: knowledgeAccount, Scope: Scope{Kind: ScopeAccount}, Key: "organization.name", CanonicalValue: json.RawMessage(`"Northstar"`), Confidence: 1000, Sensitivity: SensitivityInternal, Citations: []Citation{
		{EvidenceID: knowledgeEvidence, EvidenceKind: SourceOwnerStatement, Relation: EvidenceSupports, Locator: " second "},
		{EvidenceID: secondEvidence, EvidenceKind: SourceDocumentRevision, Relation: EvidenceSupports, Locator: "first"},
	}, ProposedBy: Actor{Kind: ActorUser, ID: string(knowledgeUser)}}, time.Now())
	if err != nil || claim.Citations[0].EvidenceID != secondEvidence || claim.Citations[1].Locator != "second" {
		t.Fatalf("citations=%+v err=%v", claim.Citations, err)
	}
}

func TestAgentDerivationCannotBeSoleAcceptedEvidence(t *testing.T) {
	claim := claimFixture(t, Actor{Kind: ActorWorkload, ID: "agent:analyst"}, SourceAgentDerivation, json.RawMessage(`"Northstar LLC"`))
	_, err := claim.Decide(DecideClaimCommand{Accept: true, Reason: "Reviewed the proposed value", Actor: Actor{Kind: ActorUser, ID: string(knowledgeUser)}, Role: accounts.RoleOwner, ExpectedVersion: 1, At: claim.CreatedAt.Add(time.Minute)})
	if !errors.Is(err, ErrState) {
		t.Fatalf("agent-only acceptance err=%v", err)
	}
	rejected, err := claim.Decide(DecideClaimCommand{Accept: false, Reason: "No independent source supports this", Actor: Actor{Kind: ActorUser, ID: string(knowledgeUser)}, Role: accounts.RoleOwner, ExpectedVersion: 1, At: claim.CreatedAt.Add(time.Minute)})
	if err != nil || rejected.State != ClaimRejected {
		t.Fatalf("rejected=%+v err=%v", rejected, err)
	}
}

func TestAcceptedClaimCreatesAndRevisesFactWithoutOverwritingHistory(t *testing.T) {
	first := claimFixture(t, Actor{Kind: ActorUser, ID: string(knowledgeUser)}, SourceOwnerStatement, json.RawMessage(`"Northstar LLC"`))
	first, err := first.Decide(DecideClaimCommand{Accept: true, Reason: "Owner confirmed legal record", Actor: Actor{Kind: ActorUser, ID: string(knowledgeUser)}, Role: accounts.RoleMember, ExpectedVersion: 1, At: first.CreatedAt.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	fact, err := NewFact(knowledgeFact, first)
	if err != nil {
		t.Fatal(err)
	}
	secondID := ids.KnowledgeClaimID("60000000-0000-4000-8000-000000000006")
	second := claimFixture(t, Actor{Kind: ActorUser, ID: string(knowledgeUser)}, SourceOwnerStatement, json.RawMessage(`"Northstar Studio LLC"`))
	second.ID = secondID
	second.ValueSHA256 = sha256.Sum256(second.CanonicalValue)
	second, err = RestoreClaim(second)
	if err != nil {
		t.Fatal(err)
	}
	second, err = second.Decide(DecideClaimCommand{Accept: true, Reason: "Amended registration inspected", Actor: Actor{Kind: ActorUser, ID: string(knowledgeUser)}, Role: accounts.RoleAdministrator, ExpectedVersion: 1, At: first.UpdatedAt.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	revised, err := fact.Revise(second)
	if err != nil || revised.Revision != 2 || revised.CurrentClaimID != secondID || fact.CurrentClaimID != knowledgeClaim {
		t.Fatalf("original=%+v revised=%+v err=%v", fact, revised, err)
	}
}

func TestViewerAndWorkloadCannotDecideClaim(t *testing.T) {
	claim := claimFixture(t, Actor{Kind: ActorUser, ID: string(knowledgeUser)}, SourceOwnerStatement, json.RawMessage(`true`))
	if _, err := claim.Decide(DecideClaimCommand{Accept: true, Reason: "Reviewed source", Actor: Actor{Kind: ActorUser, ID: string(knowledgeUser)}, Role: accounts.RoleViewer, ExpectedVersion: 1, At: claim.CreatedAt.Add(time.Minute)}); !errors.Is(err, ErrRole) {
		t.Fatalf("viewer err=%v", err)
	}
	if _, err := claim.Decide(DecideClaimCommand{Accept: true, Reason: "Reviewed source", Actor: Actor{Kind: ActorWorkload, ID: "agent:reviewer"}, Role: accounts.RoleOwner, ExpectedVersion: 1, At: claim.CreatedAt.Add(time.Minute)}); !errors.Is(err, ErrState) {
		t.Fatalf("workload err=%v", err)
	}
}
