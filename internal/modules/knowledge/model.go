// Package knowledge owns immutable evidence, reviewable claims, and accepted
// Account-scoped fact revisions.
package knowledge

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumCanonicalValueBytes = 64 << 10
	CanonicalValueHashVersion  = uint16(1)
	MaximumCitationLocator     = 512
	MaximumDecisionReason      = 1000
	MaximumSourceReference     = 2048
)

var (
	ErrInvalid  = errors.New("knowledge aggregate is invalid")
	ErrConflict = errors.New("knowledge version conflicts with the command")
	ErrState    = errors.New("knowledge aggregate state rejects the command")
	ErrRole     = errors.New("knowledge role is not eligible")
)

var codePattern = regexp.MustCompile(`^[a-z][a-z0-9._:/-]{0,127}$`)

type ActorKind string

const (
	ActorUser     ActorKind = "user"
	ActorWorkload ActorKind = "workload"
)

type Actor struct {
	Kind ActorKind
	ID   string
}

func (actor Actor) Valid() bool {
	return (actor.Kind == ActorUser || actor.Kind == ActorWorkload) && actor.ID != "" && len(actor.ID) <= 256 && strings.TrimSpace(actor.ID) == actor.ID && !strings.ContainsRune(actor.ID, '\x00') && (actor.Kind != ActorUser || ids.Validate(actor.ID) == nil)
}

type ScopeKind string

const (
	ScopeAccount      ScopeKind = "account"
	ScopeWorkItem     ScopeKind = "work_item"
	ScopeConversation ScopeKind = "conversation"
)

type Scope struct {
	Kind ScopeKind
	ID   string
}

func (scope Scope) Valid() bool {
	if scope.Kind == ScopeAccount {
		return scope.ID == ""
	}
	return (scope.Kind == ScopeWorkItem || scope.Kind == ScopeConversation) && ids.Validate(scope.ID) == nil
}

type SourceKind string

const (
	SourceOwnerStatement    SourceKind = "owner_statement"
	SourceDocumentRevision  SourceKind = "document_revision"
	SourceIntegrationRecord SourceKind = "integration_record"
	SourcePublicWebCapture  SourceKind = "public_web_capture"
	SourceAgentDerivation   SourceKind = "agent_derivation"
)

func (kind SourceKind) Valid() bool {
	return kind == SourceOwnerStatement || kind == SourceDocumentRevision || kind == SourceIntegrationRecord || kind == SourcePublicWebCapture || kind == SourceAgentDerivation
}

type Evidence struct {
	ID              ids.KnowledgeEvidenceID
	AccountID       ids.AccountID
	Kind            SourceKind
	SourceReference string
	SourceRevision  string
	ContentSHA256   [sha256.Size]byte
	CapturedAt      time.Time
	CreatedBy       Actor
	CreatedAt       time.Time
}

func NewEvidence(value Evidence) (Evidence, error) {
	value.SourceReference = strings.TrimSpace(value.SourceReference)
	value.SourceRevision = strings.TrimSpace(value.SourceRevision)
	value.CapturedAt, value.CreatedAt = value.CapturedAt.UTC(), value.CreatedAt.UTC()
	return RestoreEvidence(value)
}

func RestoreEvidence(value Evidence) (Evidence, error) {
	value.SourceReference = strings.TrimSpace(value.SourceReference)
	value.SourceRevision = strings.TrimSpace(value.SourceRevision)
	value.CapturedAt, value.CreatedAt = value.CapturedAt.UTC(), value.CreatedAt.UTC()
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || !value.Kind.Valid() || value.SourceReference == "" || len(value.SourceReference) > MaximumSourceReference || value.SourceRevision == "" || len(value.SourceRevision) > 256 || value.ContentSHA256 == ([sha256.Size]byte{}) || value.CapturedAt.IsZero() || value.CreatedAt.IsZero() || value.CapturedAt.After(value.CreatedAt) || !value.CreatedBy.Valid() {
		return Evidence{}, ErrInvalid
	}
	if (value.Kind == SourceOwnerStatement && value.CreatedBy.Kind != ActorUser) || (value.Kind == SourceAgentDerivation && value.CreatedBy.Kind != ActorWorkload) {
		return Evidence{}, ErrInvalid
	}
	return value, nil
}

type EvidenceRelation string

const (
	EvidenceSupports EvidenceRelation = "supports"
	EvidenceRefutes  EvidenceRelation = "refutes"
)

type Citation struct {
	EvidenceID   ids.KnowledgeEvidenceID
	EvidenceKind SourceKind
	Relation     EvidenceRelation
	Locator      string
}

func (citation Citation) valid() bool {
	citation.Locator = strings.TrimSpace(citation.Locator)
	return ids.Validate(string(citation.EvidenceID)) == nil && citation.EvidenceKind.Valid() && (citation.Relation == EvidenceSupports || citation.Relation == EvidenceRefutes) && citation.Locator != "" && len(citation.Locator) <= MaximumCitationLocator
}

type Sensitivity string

const (
	SensitivityPublic       Sensitivity = "public"
	SensitivityInternal     Sensitivity = "internal"
	SensitivityConfidential Sensitivity = "confidential"
	SensitivityRestricted   Sensitivity = "restricted"
)

func (value Sensitivity) Valid() bool {
	return value == SensitivityPublic || value == SensitivityInternal || value == SensitivityConfidential || value == SensitivityRestricted
}

type ClaimState string

const (
	ClaimProposed   ClaimState = "proposed"
	ClaimAccepted   ClaimState = "accepted"
	ClaimRejected   ClaimState = "rejected"
	ClaimSuperseded ClaimState = "superseded"
	ClaimStale      ClaimState = "stale"
)

type ClaimDecision struct {
	Reason    string
	DecidedBy ids.UserID
	DecidedAt time.Time
}

type ClaimDraft struct {
	ID             ids.KnowledgeClaimID
	AccountID      ids.AccountID
	Scope          Scope
	Key            string
	CanonicalValue json.RawMessage
	Confidence     uint16
	Sensitivity    Sensitivity
	Citations      []Citation
	ProposedBy     Actor
}

type Claim struct {
	ClaimDraft
	ValueSHA256 [sha256.Size]byte
	HashVersion uint16
	State       ClaimState
	Decision    *ClaimDecision
	Version     uint64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func NewClaim(draft ClaimDraft, now time.Time) (Claim, error) {
	canonical, err := canonicalValue(draft.CanonicalValue)
	if err != nil {
		return Claim{}, err
	}
	draft.Key = strings.TrimSpace(draft.Key)
	draft.CanonicalValue = canonical
	draft.Citations = normalizeCitations(draft.Citations)
	claim := Claim{ClaimDraft: draft, ValueSHA256: sha256.Sum256(canonical), HashVersion: CanonicalValueHashVersion, State: ClaimProposed, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	return RestoreClaim(claim)
}

func RestoreClaim(claim Claim) (Claim, error) {
	canonical, err := canonicalValue(claim.CanonicalValue)
	claim.Key = strings.TrimSpace(claim.Key)
	claim.Citations = normalizeCitations(claim.Citations)
	claim.CreatedAt, claim.UpdatedAt = claim.CreatedAt.UTC(), claim.UpdatedAt.UTC()
	if err != nil || !bytes.Equal(canonical, claim.CanonicalValue) || ids.Validate(string(claim.ID)) != nil || ids.Validate(string(claim.AccountID)) != nil || !claim.Scope.Valid() || !codePattern.MatchString(claim.Key) || claim.Confidence > 1000 || !claim.Sensitivity.Valid() || !claim.ProposedBy.Valid() || claim.ValueSHA256 != sha256.Sum256(canonical) || claim.HashVersion != CanonicalValueHashVersion || claim.Version == 0 || claim.CreatedAt.IsZero() || claim.UpdatedAt.Before(claim.CreatedAt) || !validCitations(claim.Citations) {
		return Claim{}, ErrInvalid
	}
	claim.CanonicalValue = canonical
	if claim.Decision != nil {
		decision := *claim.Decision
		decision.Reason = strings.TrimSpace(decision.Reason)
		decision.DecidedAt = decision.DecidedAt.UTC()
		if !validReason(decision.Reason) || ids.Validate(string(decision.DecidedBy)) != nil || decision.DecidedAt.Before(claim.CreatedAt) {
			return Claim{}, ErrInvalid
		}
		claim.Decision = &decision
	}
	switch claim.State {
	case ClaimProposed:
		if claim.Decision != nil {
			return Claim{}, ErrInvalid
		}
	case ClaimAccepted, ClaimRejected:
		if claim.Decision == nil {
			return Claim{}, ErrInvalid
		}
	case ClaimSuperseded, ClaimStale:
		if claim.Decision == nil {
			return Claim{}, ErrInvalid
		}
	default:
		return Claim{}, ErrInvalid
	}
	return claim, nil
}

type DecideClaimCommand struct {
	Accept          bool
	Reason          string
	Actor           Actor
	Role            accounts.MembershipRole
	ExpectedVersion uint64
	At              time.Time
}

func (claim Claim) Decide(command DecideClaimCommand) (Claim, error) {
	if command.ExpectedVersion != claim.Version {
		return Claim{}, ErrConflict
	}
	if claim.State != ClaimProposed || command.Actor.Kind != ActorUser || !command.Actor.Valid() || command.At.IsZero() {
		return Claim{}, ErrState
	}
	if command.Role != accounts.RoleOwner && command.Role != accounts.RoleAdministrator && command.Role != accounts.RoleMember {
		return Claim{}, ErrRole
	}
	if !validReason(command.Reason) {
		return Claim{}, ErrInvalid
	}
	if command.Accept && !hasIndependentSupport(claim.Citations) {
		return Claim{}, ErrState
	}
	result := claim
	if command.Accept {
		result.State = ClaimAccepted
	} else {
		result.State = ClaimRejected
	}
	result.Decision = &ClaimDecision{Reason: strings.TrimSpace(command.Reason), DecidedBy: ids.UserID(command.Actor.ID), DecidedAt: command.At.UTC()}
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return RestoreClaim(result)
}

func (claim Claim) Supersede(at time.Time) (Claim, error) {
	if claim.State != ClaimAccepted || claim.Decision == nil || at.IsZero() || at.Before(claim.UpdatedAt) {
		return Claim{}, ErrState
	}
	result := claim
	result.State = ClaimSuperseded
	result.Version++
	result.UpdatedAt = at.UTC()
	return RestoreClaim(result)
}

func validCitations(values []Citation) bool {
	if len(values) == 0 || len(values) > 64 {
		return false
	}
	seen := make(map[ids.KnowledgeEvidenceID]struct{}, len(values))
	for _, citation := range values {
		if !citation.valid() {
			return false
		}
		if _, exists := seen[citation.EvidenceID]; exists {
			return false
		}
		seen[citation.EvidenceID] = struct{}{}
	}
	return true
}

func normalizeCitations(values []Citation) []Citation {
	result := append([]Citation(nil), values...)
	for index := range result {
		result[index].Locator = strings.TrimSpace(result[index].Locator)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].EvidenceID < result[right].EvidenceID
	})
	return result
}

func hasIndependentSupport(values []Citation) bool {
	for _, citation := range values {
		if citation.Relation == EvidenceSupports && citation.EvidenceKind != SourceAgentDerivation {
			return true
		}
	}
	return false
}

func validReason(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 3 && len(value) <= MaximumDecisionReason && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func canonicalValue(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > MaximumCanonicalValueBytes || !utf8.Valid(raw) {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeJSONValue(decoder, 0)
	if err != nil {
		return nil, ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrInvalid
	}
	canonical, err := json.Marshal(value)
	if err != nil || len(canonical) > MaximumCanonicalValueBytes {
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
