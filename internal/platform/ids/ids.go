package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

type UserID string
type AccountID string
type MembershipID string
type RegistrationID string
type RecoveryID string
type ContactChangeID string
type RecoveryCodeSetID string
type SessionID string
type InvitationID string
type GrantID string
type CellID string
type WorkItemID string
type InformationRequestID string
type WorkReviewID string
type ConsequentialApprovalID string
type BoardroomID string
type PersonaID string
type PersonaVersionID string
type ConversationID string
type RunID string
type RunResolutionID string
type AgentInvocationID string
type MessageID string
type KnowledgeEvidenceID string
type KnowledgeClaimID string
type KnowledgeFactID string
type KnowledgeDocumentID string
type KnowledgeDocumentRevisionID string
type KnowledgeDocumentChunkID string
type BaselineAssessmentID string
type BaselineRequirementID string
type BaselinePlanID string
type BaselineSourceGrantID string
type ScheduleID string
type FinanceLedgerID string
type FinanceAccountID string
type FinanceEntryID string
type FinanceReconciliationID string
type MarketingCampaignID string
type MarketingAssetID string
type MarketingAssetRevisionID string
type MarketingReleaseID string
type IntegrationConnectionID string
type IntegrationConnectionRevisionID string
type IntegrationCredentialID string
type IntegrationHealthObservationID string
type IntegrationExecutionID string
type IntegrationAttemptID string
type IntegrationResolutionID string
type IntegrationSourceSyncID string
type IntegrationSourceCaptureID string

type Generator interface {
	New() string
}

type RandomGenerator struct{}

func (RandomGenerator) New() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return encode(value)
}

// Derive produces a stable UUIDv8 operation identity from an existing UUID
// and a bounded domain label. It is useful for crash-safe child operations:
// retrying the same parent request cannot accidentally mint new side effects.
func Derive(namespace, label string) (string, error) {
	if Validate(namespace) != nil || label == "" || len(label) > 200 || strings.TrimSpace(label) != label || strings.ContainsRune(label, '\x00') {
		return "", errors.New("invalid identifier derivation")
	}
	digest := sha256.Sum256([]byte("spyglass/id/v1/" + namespace + "/" + label))
	var value [16]byte
	copy(value[:], digest[:16])
	value[6] = (value[6] & 0x0f) | 0x80
	value[8] = (value[8] & 0x3f) | 0x80
	return encode(value), nil
}

func encode(value [16]byte) string {
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded)
}

func Validate(value string) error {
	if len(value) != 36 || strings.Count(value, "-") != 4 {
		return errors.New("invalid identifier")
	}
	compact := strings.ReplaceAll(value, "-", "")
	if len(compact) != 32 {
		return errors.New("invalid identifier")
	}
	_, err := hex.DecodeString(compact)
	if err != nil {
		return errors.New("invalid identifier")
	}
	return nil
}
