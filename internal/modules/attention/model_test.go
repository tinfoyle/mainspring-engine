package attention

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	attentionAccountID = "10000000-0000-4000-8000-000000000001"
	attentionWorkID    = "20000000-0000-4000-8000-000000000002"
	requesterID        = "30000000-0000-4000-8000-000000000003"
	reviewerID         = "40000000-0000-4000-8000-000000000004"
	otherUserID        = "50000000-0000-4000-8000-000000000005"
	attentionObjectID  = "60000000-0000-4000-8000-000000000006"
	factID             = "70000000-0000-4000-8000-000000000007"
	invocationID       = "80000000-0000-4000-8000-000000000008"
	operationID        = "90000000-0000-4000-8000-000000000009"
)

var attentionNow = time.Date(2026, 8, 21, 15, 0, 0, 0, time.UTC)

func userActor(id string) Actor { return Actor{Kind: ActorUser, ID: id} }

func informationDraft() InformationRequestDraft {
	return InformationRequestDraft{
		ID:               ids.InformationRequestID(attentionObjectID),
		AccountID:        ids.AccountID(attentionAccountID),
		ParentWorkItemID: ids.WorkItemID(attentionWorkID),
		Requirement:      FactRequirement{Key: "company.legal_name", Scope: InformationScopeAccount},
		Question:         "What is the registered company name?",
		RequestedBy:      userActor(requesterID),
	}
}

func TestInformationRequestCompletesOnlyForExactEligibleFact(t *testing.T) {
	request, err := NewInformationRequest(informationDraft(), attentionNow)
	if err != nil {
		t.Fatal(err)
	}
	exact := FactReference{ID: factID, Version: 3, Requirement: request.Requirement}
	if !request.EligibleFor(exact) {
		t.Fatal("exact fact was not eligible")
	}
	wrong := exact
	wrong.Requirement = FactRequirement{Key: "company.legal_name", Scope: InformationScopeWorkItem, ScopeID: attentionWorkID}
	if request.EligibleFor(wrong) {
		t.Fatal("different fact scope became eligible")
	}
	if _, err := request.Answer(AnswerInformationCommand{Fact: wrong, Role: accounts.RoleMember, Actor: userActor(otherUserID), ExpectedVersion: request.Version, At: attentionNow.Add(time.Minute)}); !errors.Is(err, ErrRequirementMismatch) {
		t.Fatalf("mismatched fact error = %v", err)
	}
	answered, err := request.Answer(AnswerInformationCommand{Fact: exact, Role: accounts.RoleMember, Actor: userActor(otherUserID), ExpectedVersion: request.Version, At: attentionNow.Add(time.Minute)})
	if err != nil || answered.State != InformationRequestAnswered || answered.AnswerRecord == nil || answered.AnswerRecord.Fact != exact || answered.ParentWorkItemID != request.ParentWorkItemID || answered.Version != request.Version+1 {
		t.Fatalf("answer = %+v, %v", answered, err)
	}
	if answered.EligibleFor(exact) {
		t.Fatal("answered request remained eligible")
	}
}

func TestInformationRequestAuthorizationAndOptimisticVersion(t *testing.T) {
	for _, role := range []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator, accounts.RoleMember, accounts.RoleViewer, accounts.RoleBillingAdmin} {
		t.Run(string(role), func(t *testing.T) {
			request, err := NewInformationRequest(informationDraft(), attentionNow)
			if err != nil {
				t.Fatal(err)
			}
			_, err = request.Answer(AnswerInformationCommand{Fact: FactReference{ID: factID, Version: 1, Requirement: request.Requirement}, Role: role, Actor: userActor(otherUserID), ExpectedVersion: request.Version, At: attentionNow.Add(time.Minute)})
			want := role == accounts.RoleOwner || role == accounts.RoleAdministrator || role == accounts.RoleMember
			if (err == nil) != want {
				t.Fatalf("answer error = %v, want allowed=%v", err, want)
			}
		})
	}
	request, _ := NewInformationRequest(informationDraft(), attentionNow)
	if _, err := request.Answer(AnswerInformationCommand{Fact: FactReference{ID: factID, Version: 1, Requirement: request.Requirement}, Role: accounts.RoleOwner, Actor: userActor(otherUserID), ExpectedVersion: request.Version + 1, At: attentionNow.Add(time.Minute)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale answer error = %v", err)
	}
	if _, err := request.Cancel(CancelInformationCommand{Role: accounts.RoleMember, Actor: userActor(otherUserID), Reason: "not my request", ExpectedVersion: request.Version, At: attentionNow.Add(time.Minute)}); !errors.Is(err, ErrRole) {
		t.Fatalf("unrelated member cancellation = %v", err)
	}
	if canceled, err := request.Cancel(CancelInformationCommand{Role: accounts.RoleMember, Actor: userActor(requesterID), Reason: "the request is no longer needed", ExpectedVersion: request.Version, At: attentionNow.Add(time.Minute)}); err != nil || canceled.State != InformationRequestCanceled {
		t.Fatalf("requester cancellation = %+v, %v", canceled, err)
	}
}

func reviewDraft() WorkReviewDraft {
	return WorkReviewDraft{
		ID:             ids.WorkReviewID(attentionObjectID),
		AccountID:      ids.AccountID(attentionAccountID),
		WorkItemID:     ids.WorkItemID(attentionWorkID),
		WorkVersion:    7,
		ProposalSHA256: sha256.Sum256([]byte("proposal-v7")),
		Question:       "Is this result ready to publish?",
		RequestedBy:    userActor(requesterID),
		ReviewerID:     ids.UserID(reviewerID),
	}
}

func TestWorkReviewBindsAssignedReviewerAndProposalVersion(t *testing.T) {
	review, err := NewWorkReview(reviewDraft(), attentionNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := review.Decide(DecideWorkReviewCommand{Decision: ReviewApprove, Reason: "looks complete", Role: accounts.RoleAdministrator, Actor: userActor(otherUserID), ExpectedVersion: review.Version, At: attentionNow.Add(time.Minute)}); !errors.Is(err, ErrReviewer) {
		t.Fatalf("unassigned reviewer error = %v", err)
	}
	approved, err := review.Decide(DecideWorkReviewCommand{Decision: ReviewApprove, Reason: "evidence and result are complete", Role: accounts.RoleMember, Actor: userActor(reviewerID), ExpectedVersion: review.Version, At: attentionNow.Add(time.Minute)})
	if err != nil || approved.State != WorkReviewApproved || approved.Decision == nil || approved.Decision.DecidedBy != userActor(reviewerID) {
		t.Fatalf("approval = %+v, %v", approved, err)
	}
	unchanged, err := approved.ReconcileProposal(ReconcileWorkReviewCommand{WorkVersion: approved.WorkVersion, ProposalSHA256: approved.ProposalSHA256, ExpectedVersion: approved.Version, At: attentionNow.Add(2 * time.Minute)})
	if err != nil || unchanged.Version != approved.Version || unchanged.State != WorkReviewApproved {
		t.Fatalf("unchanged reconciliation = %+v, %v", unchanged, err)
	}
	invalidated, err := approved.ReconcileProposal(ReconcileWorkReviewCommand{WorkVersion: approved.WorkVersion + 1, ProposalSHA256: sha256.Sum256([]byte("proposal-v8")), ExpectedVersion: approved.Version, At: attentionNow.Add(2 * time.Minute)})
	if err != nil || invalidated.State != WorkReviewInvalidated || invalidated.InvalidatedAt == nil || invalidated.Decision == nil || invalidated.Version != approved.Version+1 {
		t.Fatalf("changed reconciliation = %+v, %v", invalidated, err)
	}
}

func TestWorkReviewAuthorizationMatrix(t *testing.T) {
	for _, role := range []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator, accounts.RoleMember, accounts.RoleViewer, accounts.RoleBillingAdmin} {
		t.Run(string(role), func(t *testing.T) {
			review, err := NewWorkReview(reviewDraft(), attentionNow)
			if err != nil {
				t.Fatal(err)
			}
			_, err = review.Decide(DecideWorkReviewCommand{Decision: ReviewRequestChanges, Reason: "add the missing evidence", Role: role, Actor: userActor(reviewerID), ExpectedVersion: review.Version, At: attentionNow.Add(time.Minute)})
			want := role == accounts.RoleOwner || role == accounts.RoleAdministrator || role == accounts.RoleMember
			if (err == nil) != want {
				t.Fatalf("review error = %v, want allowed=%v", err, want)
			}
		})
	}
}

func approvalDraft(payload string) ConsequentialApprovalDraft {
	return ConsequentialApprovalDraft{
		ID:                       ids.ConsequentialApprovalID(attentionObjectID),
		AccountID:                ids.AccountID(attentionAccountID),
		OperationID:              operationID,
		InvocationID:             ids.AgentInvocationID(invocationID),
		WorkItemID:               ids.WorkItemID(attentionWorkID),
		Capability:               "email.send",
		CanonicalPayload:         json.RawMessage(payload),
		EvidenceSHA256:           sha256.Sum256([]byte("customer-approved evidence")),
		Proposer:                 userActor(requesterID),
		PolicyVersion:            4,
		RequireIndependentReview: true,
		ExpiresAt:                attentionNow.Add(30 * time.Minute),
	}
}

func TestConsequentialApprovalCanonicalizesAndProjectsExactAuthority(t *testing.T) {
	approval, err := NewConsequentialApproval(approvalDraft(`{ "to":"owner@example.com", "subject":"Quarter close" }`), attentionNow)
	if err != nil {
		t.Fatal(err)
	}
	same, err := NewConsequentialApproval(approvalDraft(`{"subject":"Quarter close","to":"owner@example.com"}`), attentionNow)
	if err != nil || same.InputSHA256 != approval.InputSHA256 || string(same.CanonicalPayload) != string(approval.CanonicalPayload) {
		t.Fatalf("canonical proposal = %s/%x, %v", same.CanonicalPayload, same.InputSHA256, err)
	}
	approved, err := approval.Decide(DecideApprovalCommand{Decision: DecisionApprove, Reason: "recipient and content are authorized", Role: accounts.RoleOwner, Actor: userActor(reviewerID), ExpectedVersion: approval.Version, At: attentionNow.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := approved.Authorization(attentionNow.Add(2 * time.Minute))
	if err != nil || authorization.ApprovalID != approved.ID || authorization.OperationID != operationID || authorization.InputSHA256 != approved.InputSHA256 || authorization.HashVersion != CanonicalPayloadHashVersion || authorization.ApprovedBy != ids.UserID(reviewerID) {
		t.Fatalf("authorization = %+v, %v", authorization, err)
	}
	if _, err := approved.Authorization(approved.ExpiresAt); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired projection error = %v", err)
	}
	if _, err := approved.Authorization(attentionNow); !errors.Is(err, ErrState) {
		t.Fatalf("pre-decision projection error = %v", err)
	}
}

func TestConsequentialApprovalAuthorizationAndIndependentReview(t *testing.T) {
	for _, role := range []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator, accounts.RoleMember, accounts.RoleViewer, accounts.RoleBillingAdmin} {
		t.Run(string(role), func(t *testing.T) {
			approval, err := NewConsequentialApproval(approvalDraft(`{"message":"approved content"}`), attentionNow)
			if err != nil {
				t.Fatal(err)
			}
			_, err = approval.Decide(DecideApprovalCommand{Decision: DecisionApprove, Reason: "policy and evidence verified", Role: role, Actor: userActor(reviewerID), ExpectedVersion: approval.Version, At: attentionNow.Add(time.Minute)})
			want := role == accounts.RoleOwner || role == accounts.RoleAdministrator
			if (err == nil) != want {
				t.Fatalf("decision error = %v, want allowed=%v", err, want)
			}
		})
	}
	approval, _ := NewConsequentialApproval(approvalDraft(`{"message":"approved content"}`), attentionNow)
	if _, err := approval.Decide(DecideApprovalCommand{Decision: DecisionApprove, Reason: "attempt self approval", Role: accounts.RoleOwner, Actor: userActor(requesterID), ExpectedVersion: approval.Version, At: attentionNow.Add(time.Minute)}); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("self approval error = %v", err)
	}
	if _, err := approval.Decide(DecideApprovalCommand{Decision: DecisionApprove, Reason: "late approval", Role: accounts.RoleOwner, Actor: userActor(reviewerID), ExpectedVersion: approval.Version, At: approval.ExpiresAt}); !errors.Is(err, ErrExpired) {
		t.Fatalf("late approval error = %v", err)
	}
}

func TestConsequentialProposalChangeInvalidatesApproval(t *testing.T) {
	base, err := NewConsequentialApproval(approvalDraft(`{"message":"approved content"}`), attentionNow)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := base.Decide(DecideApprovalCommand{Decision: DecisionApprove, Reason: "exact content is authorized", Role: accounts.RoleAdministrator, Actor: userActor(reviewerID), ExpectedVersion: base.Version, At: attentionNow.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	commands := map[string]ReconcileApprovalCommand{
		"payload":  {CanonicalPayload: json.RawMessage(`{"message":"different content"}`), EvidenceSHA256: approved.EvidenceSHA256, PolicyVersion: approved.PolicyVersion},
		"evidence": {CanonicalPayload: approved.CanonicalPayload, EvidenceSHA256: sha256.Sum256([]byte("new evidence")), PolicyVersion: approved.PolicyVersion},
		"policy":   {CanonicalPayload: approved.CanonicalPayload, EvidenceSHA256: approved.EvidenceSHA256, PolicyVersion: approved.PolicyVersion + 1},
	}
	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			command.ExpectedVersion = approved.Version
			command.At = attentionNow.Add(2 * time.Minute)
			invalidated, err := approved.ReconcileProposal(command)
			if err != nil || invalidated.State != ConsequentialApprovalInvalidated || invalidated.InvalidatedAt == nil || invalidated.Decision == nil || invalidated.Version != approved.Version+1 {
				t.Fatalf("invalidation = %+v, %v", invalidated, err)
			}
			if _, err := invalidated.Authorization(attentionNow.Add(3 * time.Minute)); !errors.Is(err, ErrState) {
				t.Fatalf("invalidated authorization error = %v", err)
			}
		})
	}
}

func TestAttentionConstructorsRejectCorruptBindings(t *testing.T) {
	information := informationDraft()
	information.Requirement = FactRequirement{Key: "company.legal_name", Scope: InformationScopeAccount, ScopeID: attentionWorkID}
	if _, err := NewInformationRequest(information, attentionNow); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid information scope error = %v", err)
	}
	review := reviewDraft()
	review.ProposalSHA256 = [sha256.Size]byte{}
	if _, err := NewWorkReview(review, attentionNow); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid review digest error = %v", err)
	}
	approval := approvalDraft(`[]`)
	if _, err := NewConsequentialApproval(approval, attentionNow); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-object approval payload error = %v", err)
	}
	approval = approvalDraft(`{"message":"ok"}`)
	approval.Capability = "email_send"
	if _, err := NewConsequentialApproval(approval, attentionNow); !errors.Is(err, ErrInvalid) {
		t.Fatalf("runner-incompatible capability error = %v", err)
	}
	approval = approvalDraft(`{"message":"first","message":"second"}`)
	if _, err := NewConsequentialApproval(approval, attentionNow); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate-key approval payload error = %v", err)
	}
	approval = approvalDraft(string([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}))
	if _, err := NewConsequentialApproval(approval, attentionNow); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid UTF-8 approval payload error = %v", err)
	}
	approval = approvalDraft(`{"message":"ok"}`)
	approval.ExpiresAt = attentionNow.Add(MaximumApprovalLifetime + time.Second)
	if _, err := NewConsequentialApproval(approval, attentionNow); !errors.Is(err, ErrInvalid) {
		t.Fatalf("overlong approval error = %v", err)
	}
	approval = approvalDraft(`{"message":"ok"}`)
	valid, err := NewConsequentialApproval(approval, attentionNow)
	if err != nil {
		t.Fatal(err)
	}
	valid.State = ConsequentialApprovalApproved
	valid.Decision = &ApprovalDecisionRecord{Decision: DecisionApprove, Reason: "approved after review", DecidedBy: ids.UserID(reviewerID), DecidedAt: attentionNow.Add(time.Minute)}
	valid.Reason = "impossible cancellation residue"
	if _, err := RestoreConsequentialApproval(valid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-state approval residue error = %v", err)
	}
}
