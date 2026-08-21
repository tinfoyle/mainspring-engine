package attention

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumCanonicalPayloadBytes = 256 << 10
	MaximumApprovalLifetime      = 24 * time.Hour
	CanonicalPayloadHashVersion  = uint16(1)
)

func (state ConsequentialApprovalState) Valid() bool {
	return state == ConsequentialApprovalOpen || state == ConsequentialApprovalApproved || state == ConsequentialApprovalRejected || state == ConsequentialApprovalCanceled || state == ConsequentialApprovalInvalidated || state == ConsequentialApprovalExpired
}

var (
	factKeyPattern    = regexp.MustCompile(`^[a-z][a-z0-9._:/-]{0,127}$`)
	capabilityPattern = regexp.MustCompile(`^[a-z][a-z0-9.:/-]{0,127}$`)
)

type ConsequentialApprovalState string
type ApprovalDecision string

const (
	ConsequentialApprovalOpen        ConsequentialApprovalState = "open"
	ConsequentialApprovalApproved    ConsequentialApprovalState = "approved"
	ConsequentialApprovalRejected    ConsequentialApprovalState = "rejected"
	ConsequentialApprovalCanceled    ConsequentialApprovalState = "canceled"
	ConsequentialApprovalInvalidated ConsequentialApprovalState = "invalidated"
	ConsequentialApprovalExpired     ConsequentialApprovalState = "expired"

	DecisionApprove ApprovalDecision = "approve"
	DecisionReject  ApprovalDecision = "reject"
)

type ApprovalDecisionRecord struct {
	Decision  ApprovalDecision
	Reason    string
	DecidedBy ids.UserID
	DecidedAt time.Time
}

type ConsequentialApprovalDraft struct {
	ID                       ids.ConsequentialApprovalID
	AccountID                ids.AccountID
	OperationID              string
	InvocationID             ids.AgentInvocationID
	WorkItemID               ids.WorkItemID
	Capability               string
	CanonicalPayload         json.RawMessage
	EvidenceSHA256           [sha256.Size]byte
	Proposer                 Actor
	PolicyVersion            uint64
	RequireIndependentReview bool
	ExpiresAt                time.Time
}

type ConsequentialApproval struct {
	ConsequentialApprovalDraft
	InputSHA256   [sha256.Size]byte
	HashVersion   uint16
	State         ConsequentialApprovalState
	Decision      *ApprovalDecisionRecord
	CanceledBy    *Actor
	Reason        string
	CanceledAt    *time.Time
	InvalidatedAt *time.Time
	ExpiredAt     *time.Time
	Version       uint64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func NewConsequentialApproval(draft ConsequentialApprovalDraft, now time.Time) (ConsequentialApproval, error) {
	canonical, err := canonicalObject(draft.CanonicalPayload)
	if err != nil {
		return ConsequentialApproval{}, ErrInvalid
	}
	draft.CanonicalPayload = canonical
	draft.Capability = strings.TrimSpace(draft.Capability)
	draft.ExpiresAt = draft.ExpiresAt.UTC()
	approval := ConsequentialApproval{
		ConsequentialApprovalDraft: draft,
		InputSHA256:                sha256.Sum256(canonical),
		HashVersion:                CanonicalPayloadHashVersion,
		State:                      ConsequentialApprovalOpen,
		Version:                    1,
		CreatedAt:                  now.UTC(),
		UpdatedAt:                  now.UTC(),
	}
	return RestoreConsequentialApproval(approval)
}

func RestoreConsequentialApproval(approval ConsequentialApproval) (ConsequentialApproval, error) {
	canonical, err := canonicalObject(approval.CanonicalPayload)
	approval.Capability = strings.TrimSpace(approval.Capability)
	approval.Reason = strings.TrimSpace(approval.Reason)
	approval.ExpiresAt, approval.CreatedAt, approval.UpdatedAt = approval.ExpiresAt.UTC(), approval.CreatedAt.UTC(), approval.UpdatedAt.UTC()
	if err != nil || !bytes.Equal(canonical, approval.CanonicalPayload) || !validID(string(approval.ID)) || !validID(string(approval.AccountID)) ||
		!validID(approval.OperationID) || !validID(string(approval.InvocationID)) || (approval.WorkItemID != "" && !validID(string(approval.WorkItemID))) ||
		!validCapability(approval.Capability) || approval.InputSHA256 != sha256.Sum256(canonical) || approval.HashVersion != CanonicalPayloadHashVersion ||
		approval.EvidenceSHA256 == ([sha256.Size]byte{}) || !approval.Proposer.Valid() || approval.PolicyVersion == 0 || approval.Version == 0 ||
		approval.CreatedAt.IsZero() || approval.UpdatedAt.Before(approval.CreatedAt) || approval.ExpiresAt.Sub(approval.CreatedAt) <= 0 || approval.ExpiresAt.Sub(approval.CreatedAt) > MaximumApprovalLifetime {
		return ConsequentialApproval{}, ErrInvalid
	}
	approval.CanonicalPayload = canonical
	if approval.Decision != nil {
		decision := *approval.Decision
		decision.Reason = strings.TrimSpace(decision.Reason)
		decision.DecidedAt = decision.DecidedAt.UTC()
		if (decision.Decision != DecisionApprove && decision.Decision != DecisionReject) || !validReason(decision.Reason) || !validID(string(decision.DecidedBy)) ||
			decision.DecidedAt.Before(approval.CreatedAt) || !decision.DecidedAt.Before(approval.ExpiresAt) {
			return ConsequentialApproval{}, ErrInvalid
		}
		approval.Decision = &decision
	}
	switch approval.State {
	case ConsequentialApprovalOpen:
		if approval.Decision != nil || approval.CanceledBy != nil || approval.Reason != "" || approval.CanceledAt != nil || approval.InvalidatedAt != nil || approval.ExpiredAt != nil {
			return ConsequentialApproval{}, ErrInvalid
		}
	case ConsequentialApprovalApproved:
		if approval.Decision == nil || approval.Decision.Decision != DecisionApprove || approval.CanceledBy != nil || approval.Reason != "" || approval.CanceledAt != nil || approval.InvalidatedAt != nil || approval.ExpiredAt != nil {
			return ConsequentialApproval{}, ErrInvalid
		}
	case ConsequentialApprovalRejected:
		if approval.Decision == nil || approval.Decision.Decision != DecisionReject || approval.CanceledBy != nil || approval.Reason != "" || approval.CanceledAt != nil || approval.InvalidatedAt != nil || approval.ExpiredAt != nil {
			return ConsequentialApproval{}, ErrInvalid
		}
	case ConsequentialApprovalCanceled:
		if approval.Decision != nil || approval.CanceledBy == nil || !approval.CanceledBy.Valid() || !validReason(approval.Reason) || approval.CanceledAt == nil || approval.CanceledAt.Before(approval.CreatedAt) || approval.InvalidatedAt != nil || approval.ExpiredAt != nil {
			return ConsequentialApproval{}, ErrInvalid
		}
		value := approval.CanceledAt.UTC()
		approval.CanceledAt = &value
	case ConsequentialApprovalInvalidated:
		if approval.CanceledBy != nil || approval.Reason != "" || approval.CanceledAt != nil || approval.InvalidatedAt == nil || approval.InvalidatedAt.Before(approval.CreatedAt) || approval.ExpiredAt != nil {
			return ConsequentialApproval{}, ErrInvalid
		}
		value := approval.InvalidatedAt.UTC()
		approval.InvalidatedAt = &value
	case ConsequentialApprovalExpired:
		if approval.CanceledBy != nil || approval.Reason != "" || approval.CanceledAt != nil || approval.InvalidatedAt != nil || approval.ExpiredAt == nil || approval.ExpiredAt.Before(approval.ExpiresAt) {
			return ConsequentialApproval{}, ErrInvalid
		}
		value := approval.ExpiredAt.UTC()
		approval.ExpiredAt = &value
	default:
		return ConsequentialApproval{}, ErrInvalid
	}
	return approval, nil
}

type DecideApprovalCommand struct {
	Decision        ApprovalDecision
	Reason          string
	Role            accounts.MembershipRole
	Actor           Actor
	ExpectedVersion uint64
	At              time.Time
}

func (approval ConsequentialApproval) Decide(command DecideApprovalCommand) (ConsequentialApproval, error) {
	if command.ExpectedVersion != approval.Version {
		return ConsequentialApproval{}, ErrConflict
	}
	if approval.State != ConsequentialApprovalOpen || !command.Actor.Valid() || command.Actor.Kind != ActorUser || command.At.IsZero() ||
		(command.Decision != DecisionApprove && command.Decision != DecisionReject) {
		return ConsequentialApproval{}, ErrState
	}
	if !canManage(command.Role) {
		return ConsequentialApproval{}, ErrRole
	}
	if !command.At.Before(approval.ExpiresAt) {
		return ConsequentialApproval{}, ErrExpired
	}
	if approval.RequireIndependentReview && approval.Proposer.Kind == ActorUser && approval.Proposer.ID == command.Actor.ID {
		return ConsequentialApproval{}, ErrSelfApproval
	}
	if !validReason(command.Reason) {
		return ConsequentialApproval{}, ErrReasonRequired
	}
	result := approval
	if command.Decision == DecisionApprove {
		result.State = ConsequentialApprovalApproved
	} else {
		result.State = ConsequentialApprovalRejected
	}
	result.Decision = &ApprovalDecisionRecord{Decision: command.Decision, Reason: strings.TrimSpace(command.Reason), DecidedBy: ids.UserID(command.Actor.ID), DecidedAt: command.At.UTC()}
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return RestoreConsequentialApproval(result)
}

type CancelApprovalCommand struct {
	Role            accounts.MembershipRole
	Actor           Actor
	Reason          string
	ExpectedVersion uint64
	At              time.Time
}

func (approval ConsequentialApproval) Cancel(command CancelApprovalCommand) (ConsequentialApproval, error) {
	if command.ExpectedVersion != approval.Version {
		return ConsequentialApproval{}, ErrConflict
	}
	if approval.State != ConsequentialApprovalOpen || !command.Actor.Valid() || command.At.IsZero() {
		return ConsequentialApproval{}, ErrState
	}
	if !command.At.Before(approval.ExpiresAt) {
		return ConsequentialApproval{}, ErrExpired
	}
	if !canManage(command.Role) && !(canParticipate(command.Role) && sameActor(command.Actor, approval.Proposer)) {
		return ConsequentialApproval{}, ErrRole
	}
	if !validReason(command.Reason) {
		return ConsequentialApproval{}, ErrReasonRequired
	}
	actor, at := command.Actor, command.At.UTC()
	result := approval
	result.State = ConsequentialApprovalCanceled
	result.CanceledBy = &actor
	result.Reason = strings.TrimSpace(command.Reason)
	result.CanceledAt = &at
	result.Version++
	result.UpdatedAt = at
	return RestoreConsequentialApproval(result)
}

type ReconcileApprovalCommand struct {
	CanonicalPayload json.RawMessage
	EvidenceSHA256   [sha256.Size]byte
	PolicyVersion    uint64
	ExpectedVersion  uint64
	At               time.Time
}

func (approval ConsequentialApproval) ReconcileProposal(command ReconcileApprovalCommand) (ConsequentialApproval, error) {
	if command.ExpectedVersion != approval.Version {
		return ConsequentialApproval{}, ErrConflict
	}
	canonical, err := canonicalObject(command.CanonicalPayload)
	if err != nil || command.EvidenceSHA256 == ([sha256.Size]byte{}) || command.PolicyVersion == 0 || command.At.IsZero() ||
		approval.State == ConsequentialApprovalCanceled || approval.State == ConsequentialApprovalRejected || approval.State == ConsequentialApprovalInvalidated || approval.State == ConsequentialApprovalExpired {
		return ConsequentialApproval{}, ErrState
	}
	if !command.At.Before(approval.ExpiresAt) {
		return ConsequentialApproval{}, ErrExpired
	}
	if sha256.Sum256(canonical) == approval.InputSHA256 && command.EvidenceSHA256 == approval.EvidenceSHA256 && command.PolicyVersion == approval.PolicyVersion {
		return approval, nil
	}
	result := approval
	result.State = ConsequentialApprovalInvalidated
	at := command.At.UTC()
	result.InvalidatedAt = &at
	result.Version++
	result.UpdatedAt = at
	return RestoreConsequentialApproval(result)
}

func (approval ConsequentialApproval) Expire(at time.Time, expectedVersion uint64) (ConsequentialApproval, error) {
	if expectedVersion != approval.Version {
		return ConsequentialApproval{}, ErrConflict
	}
	if at.IsZero() || at.Before(approval.ExpiresAt) || (approval.State != ConsequentialApprovalOpen && approval.State != ConsequentialApprovalApproved) {
		return ConsequentialApproval{}, ErrState
	}
	result := approval
	result.State = ConsequentialApprovalExpired
	value := at.UTC()
	result.ExpiredAt = &value
	result.Version++
	result.UpdatedAt = value
	return RestoreConsequentialApproval(result)
}

type ActionAuthorization struct {
	AccountID      ids.AccountID
	OperationID    string
	InvocationID   ids.AgentInvocationID
	ApprovalID     ids.ConsequentialApprovalID
	Capability     string
	InputSHA256    [sha256.Size]byte
	HashVersion    uint16
	EvidenceSHA256 [sha256.Size]byte
	Proposer       Actor
	ApprovedBy     ids.UserID
	PolicyVersion  uint64
	ApprovedAt     time.Time
	ExpiresAt      time.Time
}

func (approval ConsequentialApproval) Authorization(at time.Time) (ActionAuthorization, error) {
	if approval.State != ConsequentialApprovalApproved || approval.Decision == nil {
		return ActionAuthorization{}, ErrState
	}
	if at.IsZero() || !at.Before(approval.ExpiresAt) {
		return ActionAuthorization{}, ErrExpired
	}
	if at.Before(approval.Decision.DecidedAt) {
		return ActionAuthorization{}, ErrState
	}
	return ActionAuthorization{
		AccountID: approval.AccountID, OperationID: approval.OperationID, InvocationID: approval.InvocationID, ApprovalID: approval.ID,
		Capability: approval.Capability, InputSHA256: approval.InputSHA256, HashVersion: approval.HashVersion, EvidenceSHA256: approval.EvidenceSHA256,
		Proposer: approval.Proposer, ApprovedBy: approval.Decision.DecidedBy, PolicyVersion: approval.PolicyVersion,
		ApprovedAt: approval.Decision.DecidedAt, ExpiresAt: approval.ExpiresAt,
	}, nil
}

func canonicalObject(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > MaximumCanonicalPayloadBytes || !utf8.Valid(raw) {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeJSONValue(decoder, 0)
	object, ok := value.(map[string]any)
	if err != nil || !ok || object == nil {
		return nil, ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrInvalid
	}
	canonical, err := json.Marshal(object)
	if err != nil || len(canonical) > MaximumCanonicalPayloadBytes {
		return nil, ErrInvalid
	}
	return canonical, nil
}

func decodeJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, ErrInvalid
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		switch token.(type) {
		case nil, bool, string, json.Number:
			return token, nil
		default:
			return nil, ErrInvalid
		}
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			name, ok := nameToken.(string)
			if !ok {
				return nil, ErrInvalid
			}
			if _, duplicate := object[name]; duplicate {
				return nil, ErrInvalid
			}
			value, err := decodeJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, ErrInvalid
		}
		return object, nil
	case '[':
		values := make([]any, 0)
		for decoder.More() {
			value, err := decodeJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, ErrInvalid
		}
		return values, nil
	default:
		return nil, ErrInvalid
	}
}

func validCode(value string) bool       { return factKeyPattern.MatchString(value) }
func validCapability(value string) bool { return capabilityPattern.MatchString(value) }
