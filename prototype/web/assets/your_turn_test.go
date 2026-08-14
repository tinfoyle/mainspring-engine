package assets

import (
	"strings"
	"testing"
)

func TestYourTurnConversationKeyboardAndScrollContract(t *testing.T) {
	script, err := Files.ReadFile("your_turn.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, contract := range []string{"data-input-transcript", "data-coordinator-form", "data-current-prompt", "current.Prompt", "scrollHeight", "event.shiftKey", "requestSubmit", "fetch(form.action", "/your-turn/coordinator/state", "window.setInterval(refreshCoordinator, 5000)", "visibilitychange", "Accept: \"application/json\"", "appendMessages", "messages.slice", "if (newMessages.length)", "replaceChildren", "preventScroll: true", "behavior: \"instant\"", "is-ready"} {
		if !strings.Contains(text, contract) {
			t.Fatalf("your_turn.js does not contain %q", contract)
		}
	}
	for _, forbidden := range []string{"htmx:beforeRequest", "htmx:afterSwap", "window.location.reload"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("your_turn.js must not refresh the chat via %q", forbidden)
		}
	}
}
