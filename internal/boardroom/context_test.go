package boardroom

import (
	"strings"
	"testing"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

func TestBuildContextKeepsNewestMessagesDeterministically(t *testing.T) {
	messages := []Message{
		{ID: "one", Role: domain.MessageUser, Body: strings.Repeat("old", 30)},
		{ID: "two", Role: domain.MessageAgent, PersonaName: "Ops", Body: "middle"},
		{ID: "three", Role: domain.MessageUser, Body: "latest"},
	}
	context, manifest := BuildContext(messages, 12)
	if len(context) != 2 || manifest.ExcludedMessages != 1 || manifest.MessageIDs[0] != "two" || manifest.MessageIDs[1] != "three" {
		t.Fatalf("unexpected context: %#v, %#v", context, manifest)
	}
}
