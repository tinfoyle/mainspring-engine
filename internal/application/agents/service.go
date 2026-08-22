// Package agents provides the transport-neutral command and query boundary for
// Boardrooms, immutable Persona versions, Conversations, and Agent Runs.
package agents

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	PackageCode         catalog.PackageCode = "agents"
	ConcurrentRuns      catalog.LimitCode   = "concurrent_runs"
	MaximumPageSize                         = 100
	DefaultRunLifetime                      = 24 * time.Hour
	MaximumContextItems                     = 64
	MaximumContextBytes                     = 48 << 10
)

var (
	ErrInvalidCommand = errors.New("agent command is invalid")
	ErrNotFound       = errors.New("agent resource was not found")
	ErrConflict       = errors.New("agent resource conflicts with durable state")
	ErrConstraint     = errors.New("agent resource constraint failed")
	ErrCorrupt        = errors.New("agent persistence is corrupt")
)

type ConcurrentRunLimitError struct{ Current, Maximum int64 }

func (e *ConcurrentRunLimitError) Error() string { return "agent concurrent run limit reached" }

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Clock interface{ Now() time.Time }

type PersonaSummary struct {
	ID            ids.PersonaID
	BoardroomID   ids.BoardroomID
	State         string
	LatestVersion uint64
	Published     agentdomain.PersonaVersion
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Conversation struct {
	ID           ids.ConversationID
	AccountID    ids.AccountID
	BoardroomID  ids.BoardroomID
	Subject      string
	State        string
	MessageCount uint64
	CreatedBy    ids.UserID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type ConversationCursor struct {
	UpdatedAt time.Time
	ID        ids.ConversationID
}

type ConversationListQuery struct {
	Limit          int
	AfterUpdatedAt *time.Time
	AfterID        ids.ConversationID
}

type ConversationPage struct {
	Items      []Conversation
	NextCursor *ConversationCursor
}

type MessageRole string

const (
	MessageRoleUser    MessageRole = "user"
	MessageRolePersona MessageRole = "persona"
)

type Message struct {
	ID               ids.MessageID
	ConversationID   ids.ConversationID
	Sequence         uint64
	Role             MessageRole
	Body             string
	CreatedBy        ids.UserID
	RunID            ids.RunID
	InvocationID     ids.AgentInvocationID
	PersonaVersionID ids.PersonaVersionID
	Result           *agentdomain.ResultEnvelope
	CreatedAt        time.Time
}

type MessageListQuery struct {
	Limit         int
	AfterSequence uint64
}

type MessagePage struct {
	Items             []Message
	NextAfterSequence *uint64
}

type Run struct {
	Plan          agentdomain.RunPlan
	Mode          RunMode
	State         string
	Subject       string
	Prompt        string
	UserMessageID ids.MessageID
	InvocationIDs []ids.AgentInvocationID
	Invocations   []RunInvocation
	Resolutions   []RunResolution
	Context       []ContextReference
	ContextDigest [32]byte
}

type RunMode string

const (
	RunModeSelected   RunMode = "selected"
	RunModeManagerLed RunMode = "manager_led"
)

type ContextSelection struct {
	WorkItemIDs           []ids.WorkItemID
	KnowledgeFactIDs      []ids.KnowledgeFactID
	KnowledgeDocumentIDs  []ids.KnowledgeDocumentID
	BaselineAssessmentIDs []ids.BaselineAssessmentID
}

type ContextReference struct {
	Kind    string
	ID      string
	Version uint64
	Digest  [32]byte
}

type RunInvocation struct {
	ID               ids.AgentInvocationID
	Turn             uint32
	PersonaVersionID ids.PersonaVersionID
	Status           string
	SelectedModel    string
	FailureCode      string
	StartedAt        *time.Time
	CompletedAt      *time.Time
	Usage            *RunUsage
}

type RunUsage struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	CostMicros   int64
}

type RunResolutionAction string

const (
	RunResolutionRetryFailed  RunResolutionAction = "retry_failed"
	RunResolutionAcceptFailed RunResolutionAction = "accept_failure"
)

type RunResolution struct {
	ID         ids.RunResolutionID
	RunID      ids.RunID
	Action     RunResolutionAction
	Note       string
	ActorID    ids.UserID
	RetryRunID ids.RunID
	CreatedAt  time.Time
}

type ResolveRunDraft struct {
	Actor                access.Actor
	AccountID            ids.AccountID
	RunID                ids.RunID
	ResolutionID         ids.RunResolutionID
	RetryRunID           ids.RunID
	Action               RunResolutionAction
	Note                 string
	EntitlementVersion   uint64
	MaximumConcurrentRun int64
	CreatedAt            time.Time
	RequestExpiresAt     time.Time
}

type Repository interface {
	CreateBoardroom(context.Context, agentdomain.Boardroom) (agentdomain.Boardroom, bool, error)
	ConfigureBoardroomManager(context.Context, ids.AccountID, ids.BoardroomID, ids.PersonaID, uint64, time.Time) (agentdomain.Boardroom, bool, error)
	ListBoardrooms(context.Context, ids.AccountID, int) ([]agentdomain.Boardroom, error)
	GetBoardroom(context.Context, ids.AccountID, ids.BoardroomID) (agentdomain.Boardroom, error)
	PublishPersona(context.Context, ids.BoardroomID, agentdomain.PersonaVersion, uint64) (PersonaSummary, bool, error)
	ListPersonas(context.Context, ids.AccountID, ids.BoardroomID, int) ([]PersonaSummary, error)
	ListConversations(context.Context, ids.AccountID, ids.BoardroomID, ConversationListQuery) (ConversationPage, error)
	GetConversation(context.Context, ids.AccountID, ids.ConversationID) (Conversation, error)
	ListMessages(context.Context, ids.AccountID, ids.ConversationID, MessageListQuery) (MessagePage, error)
	StartRun(context.Context, StartRunDraft) (Run, bool, error)
	GetRun(context.Context, ids.AccountID, ids.RunID) (Run, error)
	ResolveRun(context.Context, ResolveRunDraft) (RunResolution, bool, error)
}

type Service struct {
	authorizer Authorizer
	repository Repository
	clock      Clock
}

func New(authorizer Authorizer, repository Repository, clock Clock) (*Service, error) {
	if authorizer == nil || repository == nil || clock == nil {
		return nil, errors.New("agent service dependencies are required")
	}
	return &Service{authorizer: authorizer, repository: repository, clock: clock}, nil
}

type CreateBoardroomCommand struct {
	Actor     access.Actor
	AccountID ids.AccountID
	RequestID string
	Name      string
	Purpose   string
}

func (s *Service) CreateBoardroom(ctx context.Context, command CreateBoardroomCommand) (agentdomain.Boardroom, bool, error) {
	if ids.Validate(command.RequestID) != nil || !command.Actor.Valid() || ids.Validate(string(command.AccountID)) != nil {
		return agentdomain.Boardroom{}, false, ErrInvalidCommand
	}
	if _, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Roles: configureRoles(), Package: PackageCode, Mutation: true}); err != nil {
		return agentdomain.Boardroom{}, false, err
	}
	boardroom, err := agentdomain.NewBoardroom(ids.BoardroomID(command.RequestID), command.AccountID, command.Name, command.Purpose, s.clock.Now().UTC())
	if err != nil {
		return agentdomain.Boardroom{}, false, ErrInvalidCommand
	}
	return s.repository.CreateBoardroom(ctx, boardroom)
}

type ConfigureBoardroomManagerCommand struct {
	Actor            access.Actor
	AccountID        ids.AccountID
	RequestID        string
	BoardroomID      ids.BoardroomID
	ManagerPersonaID ids.PersonaID
	ExpectedVersion  uint64
}

func (s *Service) ConfigureBoardroomManager(ctx context.Context, command ConfigureBoardroomManagerCommand) (agentdomain.Boardroom, bool, error) {
	if !command.Actor.Valid() || command.Actor.UserID == "" || ids.Validate(command.RequestID) != nil || ids.Validate(string(command.AccountID)) != nil ||
		ids.Validate(string(command.BoardroomID)) != nil || ids.Validate(string(command.ManagerPersonaID)) != nil || command.ExpectedVersion == 0 || command.ExpectedVersion == ^uint64(0) {
		return agentdomain.Boardroom{}, false, ErrInvalidCommand
	}
	if _, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Roles: configureRoles(), Package: PackageCode, Mutation: true}); err != nil {
		return agentdomain.Boardroom{}, false, err
	}
	return s.repository.ConfigureBoardroomManager(ctx, command.AccountID, command.BoardroomID, command.ManagerPersonaID, command.ExpectedVersion, s.clock.Now().UTC())
}

type PublishPersonaCommand struct {
	Actor                 access.Actor
	AccountID             ids.AccountID
	BoardroomID           ids.BoardroomID
	PersonaID             ids.PersonaID
	VersionID             ids.PersonaVersionID
	ExpectedLatestVersion uint64
	Name                  string
	Role                  string
	Description           string
	SystemInstructions    string
	Policy                agentdomain.PersonaPolicy
}

func (s *Service) PublishPersona(ctx context.Context, command PublishPersonaCommand) (PersonaSummary, bool, error) {
	if !command.Actor.Valid() || command.Actor.UserID == "" || ids.Validate(string(command.AccountID)) != nil || ids.Validate(string(command.BoardroomID)) != nil || ids.Validate(string(command.PersonaID)) != nil || ids.Validate(string(command.VersionID)) != nil {
		return PersonaSummary{}, false, ErrInvalidCommand
	}
	if _, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Roles: configureRoles(), Package: PackageCode, Mutation: true}); err != nil {
		return PersonaSummary{}, false, err
	}
	for _, tool := range command.Policy.Tools {
		switch tool.Capability {
		case "work.summary.read", "finance.ledgers.read", "finance.accounts.read", "finance.entry.draft":
		default:
			return PersonaSummary{}, false, ErrInvalidCommand
		}
	}
	command.Policy.OutputSchema = agentdomain.ResultSchema()
	version, err := agentdomain.NewPersonaVersion(agentdomain.PersonaVersionDraft{
		ID: command.VersionID, PersonaID: command.PersonaID, AccountID: command.AccountID,
		Version: command.ExpectedLatestVersion + 1, Name: command.Name, Role: command.Role,
		Description: command.Description, SystemInstructions: command.SystemInstructions,
		Policy: command.Policy, CreatedBy: command.Actor.UserID, CreatedAt: s.clock.Now().UTC(),
	})
	if err != nil {
		return PersonaSummary{}, false, ErrInvalidCommand
	}
	return s.repository.PublishPersona(ctx, command.BoardroomID, version, command.ExpectedLatestVersion)
}

type StartRunDraft struct {
	Actor                access.Actor
	AccountID            ids.AccountID
	BoardroomID          ids.BoardroomID
	RunID                ids.RunID
	ConversationID       ids.ConversationID
	CreateConversation   bool
	UserMessageID        ids.MessageID
	Subject              string
	Prompt               string
	Mode                 RunMode
	PersonaIDs           []ids.PersonaID
	Context              ContextSelection
	EntitlementVersion   uint64
	MaximumConcurrentRun int64
	CanReadRestricted    bool
	CreatedAt            time.Time
	RequestExpiresAt     time.Time
}

type StartRunCommand struct {
	Actor          access.Actor
	AccountID      ids.AccountID
	RequestID      string
	BoardroomID    ids.BoardroomID
	ConversationID ids.ConversationID
	Subject        string
	Prompt         string
	Mode           RunMode
	PersonaIDs     []ids.PersonaID
	Context        ContextSelection
}

func (s *Service) StartRun(ctx context.Context, command StartRunCommand) (Run, bool, error) {
	subject := strings.TrimSpace(command.Subject)
	mode := command.Mode
	if mode == "" {
		mode = RunModeSelected
	}
	maximumPersonas := agentdomain.MaximumPersonasPerRun
	if mode == RunModeManagerLed {
		maximumPersonas--
	}
	if !command.Actor.Valid() || command.Actor.UserID == "" || ids.Validate(command.RequestID) != nil || ids.Validate(string(command.AccountID)) != nil || ids.Validate(string(command.BoardroomID)) != nil ||
		(command.ConversationID != "" && ids.Validate(string(command.ConversationID)) != nil) || (command.ConversationID == "" && (utf8.RuneCountInString(subject) < 2 || utf8.RuneCountInString(subject) > 240)) || (command.ConversationID != "" && subject != "") ||
		(mode != RunModeSelected && mode != RunModeManagerLed) || len(command.PersonaIDs) == 0 || len(command.PersonaIDs) > maximumPersonas || len(strings.TrimSpace(command.Prompt)) == 0 || len(strings.TrimSpace(command.Prompt)) > 65536 {
		return Run{}, false, ErrInvalidCommand
	}
	personas := append([]ids.PersonaID(nil), command.PersonaIDs...)
	for index, personaID := range personas {
		if ids.Validate(string(personaID)) != nil || slices.Contains(personas[:index], personaID) {
			return Run{}, false, ErrInvalidCommand
		}
	}
	if !validContextSelection(command.Context) {
		return Run{}, false, ErrInvalidCommand
	}
	accountContext, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Roles: runRoles(), Package: PackageCode, Mutation: true})
	if err != nil {
		return Run{}, false, err
	}
	maximum, exists := accountContext.PackageAccess.Limits[ConcurrentRuns]
	if !exists || maximum < 1 {
		return Run{}, false, &access.DeniedError{Code: access.DenialLimitNotDefined, Package: PackageCode, Limit: ConcurrentRuns}
	}
	if len(command.Context.WorkItemIDs) != 0 {
		if _, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Package: catalog.PackageWork}); err != nil {
			return Run{}, false, err
		}
	}
	if len(command.Context.KnowledgeFactIDs)+len(command.Context.KnowledgeDocumentIDs)+len(command.Context.BaselineAssessmentIDs) != 0 {
		if _, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Package: catalog.PackageKnowledge}); err != nil {
			return Run{}, false, err
		}
	}
	conversationID := command.ConversationID
	createConversation := conversationID == ""
	if createConversation {
		derived, err := ids.Derive(command.RequestID, "conversation")
		if err != nil {
			return Run{}, false, ErrInvalidCommand
		}
		conversationID = ids.ConversationID(derived)
	}
	messageID, err := ids.Derive(command.RequestID, "user-message")
	if err != nil {
		return Run{}, false, ErrInvalidCommand
	}
	now := s.clock.Now().UTC()
	draft := StartRunDraft{
		Actor: command.Actor, AccountID: command.AccountID, BoardroomID: command.BoardroomID,
		RunID: ids.RunID(command.RequestID), ConversationID: conversationID, CreateConversation: createConversation,
		UserMessageID: ids.MessageID(messageID), Subject: subject, Prompt: strings.TrimSpace(command.Prompt), Mode: mode,
		PersonaIDs: personas, Context: cloneContextSelection(command.Context), EntitlementVersion: accountContext.EntitlementVersion, MaximumConcurrentRun: maximum,
		CanReadRestricted: accountContext.Role == accounts.RoleOwner || accountContext.Role == accounts.RoleAdministrator,
		CreatedAt:         now, RequestExpiresAt: now.Add(DefaultRunLifetime),
	}
	run, created, err := s.repository.StartRun(ctx, draft)
	var limit *ConcurrentRunLimitError
	if errors.As(err, &limit) {
		return Run{}, false, &access.DeniedError{Code: access.DenialLimitExceeded, Package: PackageCode, Limit: ConcurrentRuns, Current: limit.Current, Maximum: limit.Maximum}
	}
	return run, created, err
}

func validContextSelection(selection ContextSelection) bool {
	total := len(selection.WorkItemIDs) + len(selection.KnowledgeFactIDs) + len(selection.KnowledgeDocumentIDs) + len(selection.BaselineAssessmentIDs)
	if total > MaximumContextItems {
		return false
	}
	seen := make(map[string]struct{}, total)
	validate := func(values []string) bool {
		for _, value := range values {
			if ids.Validate(value) != nil {
				return false
			}
			if _, duplicate := seen[value]; duplicate {
				return false
			}
			seen[value] = struct{}{}
		}
		return true
	}
	work := make([]string, len(selection.WorkItemIDs))
	for index, value := range selection.WorkItemIDs {
		work[index] = string(value)
	}
	facts := make([]string, len(selection.KnowledgeFactIDs))
	for index, value := range selection.KnowledgeFactIDs {
		facts[index] = string(value)
	}
	documents := make([]string, len(selection.KnowledgeDocumentIDs))
	for index, value := range selection.KnowledgeDocumentIDs {
		documents[index] = string(value)
	}
	baselines := make([]string, len(selection.BaselineAssessmentIDs))
	for index, value := range selection.BaselineAssessmentIDs {
		baselines[index] = string(value)
	}
	return validate(work) && validate(facts) && validate(documents) && validate(baselines)
}

func cloneContextSelection(selection ContextSelection) ContextSelection {
	return ContextSelection{WorkItemIDs: slices.Clone(selection.WorkItemIDs), KnowledgeFactIDs: slices.Clone(selection.KnowledgeFactIDs), KnowledgeDocumentIDs: slices.Clone(selection.KnowledgeDocumentIDs), BaselineAssessmentIDs: slices.Clone(selection.BaselineAssessmentIDs)}
}

func (s *Service) ListBoardrooms(ctx context.Context, actor access.Actor, accountID ids.AccountID, limit int) ([]agentdomain.Boardroom, error) {
	if limit < 1 || limit > MaximumPageSize {
		return nil, ErrInvalidCommand
	}
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: PackageCode}); err != nil {
		return nil, err
	}
	return s.repository.ListBoardrooms(ctx, accountID, limit)
}

func (s *Service) ListPersonas(ctx context.Context, actor access.Actor, accountID ids.AccountID, boardroomID ids.BoardroomID, limit int) ([]PersonaSummary, error) {
	if limit < 1 || limit > MaximumPageSize || ids.Validate(string(boardroomID)) != nil {
		return nil, ErrInvalidCommand
	}
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: PackageCode}); err != nil {
		return nil, err
	}
	return s.repository.ListPersonas(ctx, accountID, boardroomID, limit)
}

func (s *Service) ListConversations(ctx context.Context, actor access.Actor, accountID ids.AccountID, boardroomID ids.BoardroomID, query ConversationListQuery) (ConversationPage, error) {
	if ids.Validate(string(boardroomID)) != nil || query.Limit < 1 || query.Limit > MaximumPageSize ||
		(query.AfterUpdatedAt == nil) != (query.AfterID == "") || (query.AfterUpdatedAt != nil && (query.AfterUpdatedAt.IsZero() || ids.Validate(string(query.AfterID)) != nil)) {
		return ConversationPage{}, ErrInvalidCommand
	}
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: PackageCode}); err != nil {
		return ConversationPage{}, err
	}
	return s.repository.ListConversations(ctx, accountID, boardroomID, query)
}

func (s *Service) GetConversation(ctx context.Context, actor access.Actor, accountID ids.AccountID, conversationID ids.ConversationID) (Conversation, error) {
	if ids.Validate(string(conversationID)) != nil {
		return Conversation{}, ErrInvalidCommand
	}
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: PackageCode}); err != nil {
		return Conversation{}, err
	}
	return s.repository.GetConversation(ctx, accountID, conversationID)
}

func (s *Service) ListMessages(ctx context.Context, actor access.Actor, accountID ids.AccountID, conversationID ids.ConversationID, query MessageListQuery) (MessagePage, error) {
	if ids.Validate(string(conversationID)) != nil || query.Limit < 1 || query.Limit > MaximumPageSize || query.AfterSequence > uint64(1<<63-1) {
		return MessagePage{}, ErrInvalidCommand
	}
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: PackageCode}); err != nil {
		return MessagePage{}, err
	}
	return s.repository.ListMessages(ctx, accountID, conversationID, query)
}

func (s *Service) GetRun(ctx context.Context, actor access.Actor, accountID ids.AccountID, runID ids.RunID) (Run, error) {
	if ids.Validate(string(runID)) != nil {
		return Run{}, ErrInvalidCommand
	}
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: PackageCode}); err != nil {
		return Run{}, err
	}
	return s.repository.GetRun(ctx, accountID, runID)
}

type ResolveRunCommand struct {
	Actor     access.Actor
	AccountID ids.AccountID
	RequestID string
	RunID     ids.RunID
	Action    RunResolutionAction
	Note      string
}

func (s *Service) ResolveRun(ctx context.Context, command ResolveRunCommand) (RunResolution, bool, error) {
	note := strings.TrimSpace(command.Note)
	if !command.Actor.Valid() || command.Actor.UserID == "" || ids.Validate(command.RequestID) != nil || ids.Validate(string(command.AccountID)) != nil || ids.Validate(string(command.RunID)) != nil ||
		(command.Action != RunResolutionRetryFailed && command.Action != RunResolutionAcceptFailed) || utf8.RuneCountInString(note) < 3 || utf8.RuneCountInString(note) > 1000 {
		return RunResolution{}, false, ErrInvalidCommand
	}
	accountContext, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Roles: runRoles(), Package: PackageCode, Mutation: true})
	if err != nil {
		return RunResolution{}, false, err
	}
	draft := ResolveRunDraft{Actor: command.Actor, AccountID: command.AccountID, RunID: command.RunID, ResolutionID: ids.RunResolutionID(command.RequestID), Action: command.Action, Note: note,
		EntitlementVersion: accountContext.EntitlementVersion, CreatedAt: s.clock.Now().UTC()}
	if command.Action == RunResolutionRetryFailed {
		maximum, exists := accountContext.PackageAccess.Limits[ConcurrentRuns]
		if !exists || maximum < 1 {
			return RunResolution{}, false, &access.DeniedError{Code: access.DenialLimitNotDefined, Package: PackageCode, Limit: ConcurrentRuns}
		}
		retryID, err := ids.Derive(command.RequestID, "retry-run")
		if err != nil {
			return RunResolution{}, false, ErrInvalidCommand
		}
		draft.RetryRunID = ids.RunID(retryID)
		draft.MaximumConcurrentRun = maximum
		draft.RequestExpiresAt = draft.CreatedAt.Add(DefaultRunLifetime)
	}
	resolution, created, err := s.repository.ResolveRun(ctx, draft)
	var limit *ConcurrentRunLimitError
	if errors.As(err, &limit) {
		return RunResolution{}, false, &access.DeniedError{Code: access.DenialLimitExceeded, Package: PackageCode, Limit: ConcurrentRuns, Current: limit.Current, Maximum: limit.Maximum}
	}
	return resolution, created, err
}

func configureRoles() []accounts.MembershipRole {
	return []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator}
}

func runRoles() []accounts.MembershipRole {
	return []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator, accounts.RoleMember}
}
