package agents

import (
	"context"
	"encoding/json"
	"errors"
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
	boardroom agentdomain.Boardroom
	version   agentdomain.PersonaVersion
	runDraft  StartRunDraft
	run       Run
	err       error
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
func (r *serviceRepository) StartRun(_ context.Context, draft StartRunDraft) (Run, bool, error) {
	r.runDraft = draft
	return r.run, true, r.err
}
func (*serviceRepository) GetRun(context.Context, ids.AccountID, ids.RunID) (Run, error) {
	return Run{}, nil
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
	command := StartRunCommand{Actor: access.Actor{UserID: testUser}, AccountID: testAccount, RequestID: testRequest, BoardroomID: testBoardroom, Subject: "Weekly operating review", Prompt: "What should we prioritize this week?", PersonaIDs: []ids.PersonaID{testPersona}}
	_, created, err := service.StartRun(context.Background(), command)
	if err != nil || !created {
		t.Fatal(err)
	}
	draft := repository.runDraft
	if !draft.CreateConversation || ids.Validate(string(draft.ConversationID)) != nil || ids.Validate(string(draft.UserMessageID)) != nil || draft.EntitlementVersion != 7 || draft.MaximumConcurrentRun != 2 || draft.RequestExpiresAt != now.Add(DefaultRunLifetime) {
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
