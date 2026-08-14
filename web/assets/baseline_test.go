package assets

import (
	"strings"
	"testing"
)

func TestBaselineTranscriptScrollsToLatestMessage(t *testing.T) {
	script, err := Files.ReadFile("baseline.js")
	if err != nil {
		t.Fatal(err)
	}
	contents := string(script)
	for _, contract := range []string{
		`[data-baseline-transcript]`,
		`transcript.scrollTop = transcript.scrollHeight`,
		`event.key !== "Enter"`,
		`event.shiftKey`,
		`event.isComposing`,
		`event.preventDefault()`,
		`composer.form?.requestSubmit()`,
		`#evidence-interview-`,
		`target?.scrollIntoView`,
		`DOMContentLoaded`,
		`pageshow`,
	} {
		if !strings.Contains(contents, contract) {
			t.Fatalf("baseline.js does not contain %q", contract)
		}
	}
}
