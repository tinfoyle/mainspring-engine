package agents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	testAccount   = ids.AccountID("10000000-0000-4000-8000-000000000001")
	testUser      = ids.UserID("20000000-0000-4000-8000-000000000002")
	testBoardroom = ids.BoardroomID("30000000-0000-4000-8000-000000000003")
	testPersona   = ids.PersonaID("40000000-0000-4000-8000-000000000004")
	testVersion   = ids.PersonaVersionID("50000000-0000-4000-8000-000000000005")
	testRequest   = "60000000-0000-4000-8000-000000000006"
)

type serviceClock struct{ now time.Time }

func (c serviceClock) Now() time.Time { return c.now }

type serviceAuthorizer struct {
	role      accounts.MembershipRole
	mode      catalog.PackageMode
	limit     int64
	last      access.Requirement
	authorize error
}

func (a *serviceAuthorizer) Authorize(_ context.Context, actor access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	a.last = requirement
	if a.authorize != nil {
		return access.AccountContext{}, a.authorize
	}
	if actor.UserID != testUser || accountID != testAccount {
		return access.AccountContext{}, errors.New("wrong actor")
	}
	return access.AccountContext{AccountID: accountID, EntitlementVersion: 7, Role: a.role, PackageAccess: &entitlements.PackageAccess{Code: PackageCode, Version: 1, Mode: a.mode, Limits: map[catalog.LimitCode]int64{ConcurrentRuns: a.limit}}}, nil
}

type serviceRepository struct {
	boardroom             agentdomain.Boardroom
	version               agentdomain.PersonaVersion
	runDraft              StartRunDraft
	run                   Run
	conversation          Conversation
	conversationPage      ConversationPage
	conversationQuery     ConversationListQuery
	conversationBoardroom ids.BoardroomID
	messagePage           MessagePage
	messageQuery          MessageListQuery
	messageConversation   ids.ConversationID
	resolutionDraft       ResolveRunDraft
	resolution            RunResolution
	err                   error
}

func (r *serviceRepository) CreateBoardroom(_ context.Context, item agentdomain.Boardroom) (agentdomain.Boardroom, bool, error) {
	r.boardroom = item
	return item, true, r.err
}
func (*serviceRepository) ListBoardrooms(context.Context, ids.AccountID, int) ([]agentdomain.Boardroom, error) {
	return nil, nil
}
func (*serviceRepository) GetBoardroom(context.Context, ids.AccountID, ids.BoardroomID) (agentdomain.Boardroom, error) {
	return agentdomain.Boardroom{}, nil
}
func (r *serviceRepository) PublishPersona(_ context.Context, _ ids.BoardroomID, item agentdomain.PersonaVersion, _ uint64) (PersonaSummary, bool, error) {
	r.version = item
	return PersonaSummary{Published: item}, true, r.err
}
func (*serviceRepository) ListPersonas(context.Context, ids.AccountID, ids.BoardroomID, int) ([]PersonaSummary, error) {
	return nil, nil
}
func (r *serviceRepository) ListConversations(_ context.Context, _ ids.AccountID, boardroomID ids.BoardroomID, query ConversationListQuery) (ConversationPage, error) {
	r.conversationBoardroom, r.conversationQuery = boardroomID, query
	return r.conversationPage, r.err
}
func (r *serviceRepository) GetConversation(_ context.Context, _ ids.AccountID, conversationID ids.ConversationID) (Conversation, error) {
	r.messageConversation = conversationID
	return r.conversation, r.err
}
func (r *serviceRepository) ListMessages(_ context.Context, _ ids.AccountID, conversationID ids.ConversationID, query MessageListQuery) (MessagePage, error) {
	r.messageConversation, r.messageQuery = conversationID, query
	return r.messagePage, r.err
}
func (r *serviceRepository) StartRun(_ context.Context, draft StartRunDraft) (Run, bool, error) {
	r.runDraft = draft
	return r.run, true, r.err
}
func (*serviceRepository) GetRun(context.Context, ids.AccountID, ids.RunID) (Run, error) {
	return Run{}, nil
}
func (r *serviceRepository) ResolveRun(_ context.Context, draft ResolveRunDraft) (RunResolution, bool, error) {
	r.resolutionDraft = draft
	return r.resolution, true, r.err
}

func newAgentService(t *testing.T) (*Service, *serviceAuthorizer, *serviceRepository, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 19, 1, 0, 0, 0, time.UTC)
	authorizer := &serviceAuthorizer{role: accounts.RoleOwner, mode: catalog.ModeEnabled, limit: 2}
	repository := &serviceRepository{}
	service, err := New(authorizer, repository, serviceClock{now})
	if err != nil {
		t.Fatal(err)
	}
	return service, authorizer, repository, now
}

func TestCreateBoardroomRequiresConfigureRoleAndEnabledPackage(t *testing.T) {
	service, authorizer, repository, now := newAgentService(t)
	created, fresh, err := service.CreateBoardroom(context.Background(), CreateBoardroomCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, RequestID: testRequest, Name: "Operations", Purpose: "Coordinate accountable work."})
	if err != nil || !fresh || created.ID != ids.BoardroomID(testRequest) || repository.boardroom.CreatedAt != now || !authorizer.last.Mutation || authorizer.last.Package != PackageCode {
		t.Fatalf("created=%+v fresh=%v requirement=%+v err=%v", created, fresh, authorizer.last, err)
	}
	authorizer.authorize = &access.DeniedError{Code: access.DenialRole, Package: PackageCode}
	if _, _, err := service.CreateBoardroom(context.Background(), CreateBoardroomCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, RequestID: testRequest, Name: "Operations", Purpose: "Coordinate accountable work."}); !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("role denial=%v", err)
	}
}

func TestPublishPersonaPinsOwnedResultSchemaAndCapabilityAllowlist(t *testing.T) {
	service, _, repository, _ := newAgentService(t)
	policy := agentdomain.PersonaPolicy{Provider: "openai", Model: "gpt-5.6", ReasoningEffort: "medium", MaximumInputTokens: 100000, MaximumOutputTokens: 4000, MaximumCostMicros: 100000, MaximumToolSteps: 1, CitationPolicy: "best_effort", ActionPolicy: "propose", Tools: []agentdomain.ToolGrant{{Name: "read_work", Capability: "work.summary.read", Description: "Read the Work summary.", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)}}}
	_, created, err := service.PublishPersona(context.Background(), PublishPersonaCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, BoardroomID: testBoardroom, PersonaID: testPersona, VersionID: testVersion, Name: "Operations Lead", Role: "Operations", Description: "Coordinates work.", SystemInstructions: "Coordinate operational work and report evidence clearly.", Policy: policy})
	if err != nil || !created || string(repository.version.Policy.OutputSchema) == "" || repository.version.Version != 1 {
		t.Fatalf("version=%+v created=%v err=%v", repository.version, created, err)
	}
	policy.Tools[0].Capability = "email.send"
	if _, _, err := service.PublishPersona(context.Background(), PublishPersonaCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, BoardroomID: testBoardroom, PersonaID: testPersona, VersionID: testVersion, Name: "Operations Lead", Role: "Operations", SystemInstructions: "Coordinate operational work and report evidence clearly.", Policy: policy}); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("unregistered capability=%v", err)
	}
}

func TestStartRunDerivesStableChildrenAndFreezesEntitlement(t *testing.T) {
	service, _, repository, now := newAgentService(t)
	documentID := ids.KnowledgeDocumentID("66000000-0000-4000-8000-000000000006")
	command := StartRunCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, RequestID: testRequest, BoardroomID: testBoardroom, Subject: "Weekly operating review", Prompt: "What should we prioritize this week?", PersonaIDs: []ids.PersonaID{testPersona}, Context: ContextSelection{KnowledgeDocumentIDs: []ids.KnowledgeDocumentID{documentID}}}
	_, created, err := service.StartRun(context.Background(), command)
	if err != nil || !created {
		t.Fatal(err)
	}
	draft := repository.runDraft
	if !draft.CreateConversation || ids.Validate(string(draft.ConversationID)) != nil || ids.Validate(string(draft.UserMessageID)) != nil || draft.EntitlementVersion != 7 || draft.MaximumConcurrentRun != 2 || draft.RequestExpiresAt != now.Add(DefaultRunLifetime) || len(draft.Context.KnowledgeDocumentIDs) != 1 || draft.Context.KnowledgeDocumentIDs[0] != documentID {
		t.Fatalf("unexpected run draft: %+v", draft)
	}
	firstConversation, firstMessage := draft.ConversationID, draft.UserMessageID
	if _, _, err := service.StartRun(context.Background(), command); err != nil || repository.runDraft.ConversationID != firstConversation || repository.runDraft.UserMessageID != firstMessage {
		t.Fatalf("unstable retry draft=%+v err=%v", repository.runDraft, err)
	}
}

func TestStartRunRejectsMissingCommercialLimit(t *testing.T) {
	service, authorizer, _, _ := newAgentService(t)
	authorizer.limit = 0
	_, _, err := service.StartRun(context.Background(), StartRunCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, RequestID: testRequest, BoardroomID: testBoardroom, Subject: "Weekly operating review", Prompt: "What should we prioritize?", PersonaIDs: []ids.PersonaID{testPersona}})
	if !access.IsDenied(err, access.DenialLimitNotDefined) {
		t.Fatalf("missing limit=%v", err)
	}
}

func TestResolveRunCreatesStableRetryAndManualResolutionDrafts(t *testing.T) {
	service, authorizer, repository, now := newAgentService(t)
	runID := ids.RunID("70000000-0000-4000-8000-000000000007")
	repository.resolution = RunResolution{ID: ids.RunResolutionID(testRequest), RunID: runID, Action: RunResolutionRetryFailed, RetryRunID: "80000000-0000-4000-8000-000000000008", CreatedAt: now}
	resolution, created, err := service.ResolveRun(context.Background(), ResolveRunCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, RequestID: testRequest, RunID: runID, Action: RunResolutionRetryFailed, Note: " Retry the failed specialist turns. "})
	if err != nil || !created || resolution.Action != RunResolutionRetryFailed || ids.Validate(string(repository.resolutionDraft.RetryRunID)) != nil || repository.resolutionDraft.EntitlementVersion != 7 || repository.resolutionDraft.MaximumConcurrentRun != 2 || repository.resolutionDraft.RequestExpiresAt != now.Add(DefaultRunLifetime) || repository.resolutionDraft.Note != "Retry the failed specialist turns." || !authorizer.last.Mutation {
		t.Fatalf("resolution=%+v draft=%+v requirement=%+v created=%v err=%v", resolution, repository.resolutionDraft, authorizer.last, created, err)
	}
	firstRetryID := repository.resolutionDraft.RetryRunID
	if _, _, err := service.ResolveRun(context.Background(), ResolveRunCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, RequestID: testRequest, RunID: runID, Action: RunResolutionRetryFailed, Note: "Retry the failed specialist turns."}); err != nil || repository.resolutionDraft.RetryRunID != firstRetryID {
		t.Fatalf("unstable retry draft=%+v err=%v", repository.resolutionDraft, err)
	}
	if _, _, err := service.ResolveRun(context.Background(), ResolveRunCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, RequestID: "81000000-0000-4000-8000-000000000008", RunID: runID, Action: RunResolutionAcceptFailed, Note: "Resolved outside Spyglass."}); err != nil || repository.resolutionDraft.RetryRunID != "" || repository.resolutionDraft.MaximumConcurrentRun != 0 || !repository.resolutionDraft.RequestExpiresAt.IsZero() {
		t.Fatalf("manual resolution draft=%+v err=%v", repository.resolutionDraft, err)
	}
}

func TestResolveRunRejectsInvalidActionNoteAndMissingRetryLimit(t *testing.T) {
	service, authorizer, _, _ := newAgentService(t)
	runID := ids.RunID("70000000-0000-4000-8000-000000000007")
	base := ResolveRunCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, RequestID: testRequest, RunID: runID, Action: RunResolutionRetryFailed, Note: "Retry failed turns."}
	for _, command := range []ResolveRunCommand{
		func() ResolveRunCommand { value := base; value.Action = "replay"; return value }(),
		func() ResolveRunCommand { value := base; value.Note = "x"; return value }(),
		func() ResolveRunCommand { value := base; value.Note = strings.Repeat("界", 1001); return value }(),
	} {
		if _, _, err := service.ResolveRun(context.Background(), command); !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("invalid resolution command=%+v err=%v", command, err)
		}
	}
	authorizer.limit = 0
	if _, _, err := service.ResolveRun(context.Background(), base); !access.IsDenied(err, access.DenialLimitNotDefined) {
		t.Fatalf("missing retry limit=%v", err)
	}
	if _, _, err := service.ResolveRun(context.Background(), ResolveRunCommand{Actor: base.Actor, AccountID: base.AccountID, RequestID: base.RequestID, RunID: base.RunID, Action: RunResolutionAcceptFailed, Note: "Accept failure."}); err != nil {
		t.Fatalf("manual acceptance must not require capacity: %v", err)
	}
}

func TestStartRunOwnsConversationSubjectInvariant(t *testing.T) {
	service, _, _, _ := newAgentService(t)
	base := StartRunCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, RequestID: testRequest, BoardroomID: testBoardroom, Prompt: "Review priorities", PersonaIDs: []ids.PersonaID{testPersona}}
	for _, command := range []StartRunCommand{
		base,
		func() StartRunCommand { value := base; value.Subject = "x"; return value }(),
		func() StartRunCommand { value := base; value.Subject = strings.Repeat("界", 241); return value }(),
		func() StartRunCommand {
			value := base
			value.Subject = "Ignored"
			value.ConversationID = "70000000-0000-4000-8000-000000000007"
			return value
		}(),
	} {
		if _, _, err := service.StartRun(context.Background(), command); !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("invalid subject command=%+v err=%v", command, err)
		}
	}
	valid := base
	valid.Subject = strings.Repeat("界", 240)
	if _, _, err := service.StartRun(context.Background(), valid); err != nil {
		t.Fatalf("valid Unicode subject=%v", err)
	}
}

func TestConversationAndMessageQueriesAreBoundedAndAuthorized(t *testing.T) {
	service, authorizer, repository, now := newAgentService(t)
	conversationID := ids.ConversationID("70000000-0000-4000-8000-000000000007")
	messageID := ids.MessageID("80000000-0000-4000-8000-000000000008")
	repository.conversation = Conversation{ID: conversationID, AccountID: testAccount, BoardroomID: testBoardroom, Subject: "Weekly review", State: "open", MessageCount: 1, CreatedBy: testUser, CreatedAt: now, UpdatedAt: now}
	repository.conversationPage = ConversationPage{Items: []Conversation{repository.conversation}}
	repository.messagePage = MessagePage{Items: []Message{{ID: messageID, ConversationID: conversationID, Sequence: 1, Role: MessageRoleUser, Body: "What changed?", CreatedBy: testUser, CreatedAt: now}}}

	page, err := service.ListConversations(context.Background(), access.Actor{UserID: testUser}, testAccount, testBoardroom, ConversationListQuery{Limit: 50})
	if err != nil || len(page.Items) != 1 || repository.conversationBoardroom != testBoardroom || repository.conversationQuery.Limit != 50 || authorizer.last.Package != PackageCode || authorizer.last.Mutation {
		t.Fatalf("conversation page=%+v query=%+v requirement=%+v err=%v", page, repository.conversationQuery, authorizer.last, err)
	}
	loaded, err := service.GetConversation(context.Background(), access.Actor{UserID: testUser}, testAccount, conversationID)
	if err != nil || loaded.ID != conversationID || repository.messageConversation != conversationID {
		t.Fatalf("conversation=%+v err=%v", loaded, err)
	}
	messages, err := service.ListMessages(context.Background(), access.Actor{UserID: testUser}, testAccount, conversationID, MessageListQuery{Limit: 25, AfterSequence: 4})
	if err != nil || len(messages.Items) != 1 || repository.messageConversation != conversationID || repository.messageQuery.AfterSequence != 4 {
		t.Fatalf("messages=%+v query=%+v err=%v", messages, repository.messageQuery, err)
	}

	invalidCursorTime := now
	for _, query := range []ConversationListQuery{{Limit: 0}, {Limit: 50, AfterUpdatedAt: &invalidCursorTime}, {Limit: 50, AfterUpdatedAt: &invalidCursorTime, AfterID: "bad"}} {
		if _, err := service.ListConversations(context.Background(), access.Actor{UserID: testUser}, testAccount, testBoardroom, query); !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("invalid conversation query %+v = %v", query, err)
		}
	}
	if _, err := service.ListMessages(context.Background(), access.Actor{UserID: testUser}, testAccount, "bad", MessageListQuery{Limit: 25}); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("invalid message query = %v", err)
	}
}
