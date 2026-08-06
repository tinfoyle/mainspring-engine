package boardroom

import (
	"strings"
	"testing"
)

func TestConversationTitle(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{name: "normalizes whitespace", prompt: "  Review\n open   invoices  ", want: "Review open invoices"},
		{name: "empty", prompt: " \n\t ", want: "New conversation"},
		{name: "preserves unicode safely", prompt: strings.Repeat("é", 81), want: strings.Repeat("é", 77) + "..."},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := conversationTitle(test.prompt); got != test.want {
				t.Fatalf("conversationTitle() = %q, want %q", got, test.want)
			}
		})
	}
}
