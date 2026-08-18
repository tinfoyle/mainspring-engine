package postgres

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	agentapp "github.com/tinfoyle/spyglass-engine/internal/application/agents"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type agentScanFunc func(...any) error

func (scan agentScanFunc) Scan(destinations ...any) error { return scan(destinations...) }

func TestScanAgentMessageValidatesRoleShapeAndResultBinding(t *testing.T) {
	now := time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC)
	messageID := ids.MessageID("81000000-0000-4000-8000-000000000001")
	conversationID := ids.ConversationID("71000000-0000-4000-8000-000000000001")
	userID := "21000000-0000-4000-8000-000000000001"
	user, err := scanAgentMessage(agentScanFunc(func(destinations ...any) error {
		*destinations[0].(*ids.MessageID) = messageID
		*destinations[1].(*ids.ConversationID) = conversationID
		*destinations[2].(*int64) = 1
		*destinations[3].(*agentapp.MessageRole) = agentapp.MessageRoleUser
		*destinations[4].(*string) = "What changed?"
		*destinations[5].(**string) = &userID
		*destinations[10].(*time.Time) = now
		return nil
	}))
	if err != nil || user.CreatedBy != ids.UserID(userID) || user.Result != nil || user.Sequence != 1 {
		t.Fatalf("user=%+v err=%v", user, err)
	}

	result := agentdomain.ResultEnvelope{Contribution: "Focus on the overdue review.", Findings: []string{}, Recommendations: []string{}, Questions: []string{}, Citations: []agentdomain.Citation{}, ProposedActions: []agentdomain.ProposedAction{}, Delegations: []agentdomain.Delegation{}, Confidence: agentdomain.ConfidenceHigh}
	raw, _ := json.Marshal(result)
	runID := "61000000-0000-4000-8000-000000000001"
	invocationID := "91000000-0000-4000-8000-000000000001"
	versionID := "31000000-0000-4000-8000-000000000001"
	personaRow := func(body string) agentScanFunc {
		return func(destinations ...any) error {
			*destinations[0].(*ids.MessageID) = messageID
			*destinations[1].(*ids.ConversationID) = conversationID
			*destinations[2].(*int64) = 2
			*destinations[3].(*agentapp.MessageRole) = agentapp.MessageRolePersona
			*destinations[4].(*string) = body
			*destinations[6].(**string) = &runID
			*destinations[7].(**string) = &invocationID
			*destinations[8].(**string) = &versionID
			*destinations[9].(*[]byte) = append([]byte(nil), raw...)
			*destinations[10].(*time.Time) = now
			return nil
		}
	}
	persona, err := scanAgentMessage(personaRow(result.Contribution))
	if err != nil || persona.Result == nil || persona.Result.Confidence != agentdomain.ConfidenceHigh || persona.RunID != ids.RunID(runID) {
		t.Fatalf("persona=%+v err=%v", persona, err)
	}
	if _, err := scanAgentMessage(personaRow("different unbound body")); !errors.Is(err, agentapp.ErrCorrupt) {
		t.Fatalf("unbound body error=%v", err)
	}
}

func TestScanConversationRejectsInvalidDurableState(t *testing.T) {
	now := time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC)
	row := func(state string, count int64) agentScanFunc {
		return func(destinations ...any) error {
			*destinations[0].(*ids.AccountID) = "11000000-0000-4000-8000-000000000001"
			*destinations[1].(*ids.ConversationID) = "71000000-0000-4000-8000-000000000001"
			*destinations[2].(*ids.BoardroomID) = "41000000-0000-4000-8000-000000000001"
			*destinations[3].(*string) = "Weekly review"
			*destinations[4].(*string) = state
			*destinations[5].(*int64) = count
			*destinations[6].(*ids.UserID) = "21000000-0000-4000-8000-000000000001"
			*destinations[7].(*time.Time) = now
			*destinations[8].(*time.Time) = now
			return nil
		}
	}
	conversation, err := scanConversation(row("open", 2))
	if err != nil || conversation.MessageCount != 2 {
		t.Fatalf("conversation=%+v err=%v", conversation, err)
	}
	if _, err := scanConversation(row("invented", 2)); !errors.Is(err, agentapp.ErrCorrupt) {
		t.Fatalf("invalid state error=%v", err)
	}
	if _, err := scanConversation(row("open", -1)); !errors.Is(err, agentapp.ErrCorrupt) {
		t.Fatalf("negative message count error=%v", err)
	}
}
