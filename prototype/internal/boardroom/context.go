package boardroom

import (
	"unicode/utf8"

	"github.com/tinfoyle/mainspring-engine/internal/agent"
)

type ContextManifest struct {
	Version          int      `json:"version"`
	MessageIDs       []string `json:"message_ids"`
	ExcludedMessages int      `json:"excluded_messages"`
	CharacterBudget  int      `json:"character_budget"`
}

func BuildContext(messages []Message, tokenBudget int) ([]agent.ConversationMessage, ContextManifest) {
	if tokenBudget <= 0 {
		tokenBudget = 12000
	}
	characterBudget := tokenBudget * 4
	manifest := ContextManifest{Version: 1, CharacterBudget: characterBudget}
	start, used := len(messages), 0
	for index := len(messages) - 1; index >= 0; index-- {
		cost := utf8.RuneCountInString(messages[index].Body) + utf8.RuneCountInString(messages[index].PersonaName) + 16
		if used+cost > characterBudget && start < len(messages) {
			break
		}
		used += cost
		start = index
	}
	manifest.ExcludedMessages = start
	conversation := make([]agent.ConversationMessage, 0, len(messages)-start)
	for _, message := range messages[start:] {
		manifest.MessageIDs = append(manifest.MessageIDs, message.ID)
		conversation = append(conversation, agent.ConversationMessage{Role: message.Role, PersonaName: message.PersonaName, Body: message.Body})
	}
	return conversation, manifest
}
