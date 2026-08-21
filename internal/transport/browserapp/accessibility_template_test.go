package browserapp

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/recoverycodes"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"golang.org/x/net/html"
)

func TestPrivateBrowserTemplatesHaveAccessiblePageFrames(t *testing.T) {
	t.Parallel()
	pages, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("01J00000000000000000000000")
	choice := accountaccess.Choice{AccountID: accountID, DisplayName: "Northstar Studio"}
	richSecurity := pageData{
		Title:                    "Identity security",
		PasskeysConfigured:       true,
		RecoveryCodesConfigured:  true,
		ContactChangesConfigured: true,
		CurrentEmail:             "owner@example.com",
		Passkeys:                 []passkeys.CredentialSummary{{ID: "credential", Name: "Office passkey"}},
		RecoveryCodeStatus:       recoverycodes.Status{Configured: true, Version: 2, Remaining: 8},
		RecoveryCodes:            []string{"AAAA-BBBB", "CCCC-DDDD"},
	}
	tests := []struct {
		name, page string
		data       pageData
		currentNav bool
	}{
		{name: "login", page: "login", data: pageData{Title: "Sign in", PasskeysConfigured: true}},
		{name: "forgot password", page: "forgot", data: pageData{Title: "Recover your identity"}},
		{name: "reset password", page: "reset", data: pageData{Title: "Set a new password", Token: "token"}},
		{name: "signup", page: "signup", data: pageData{Title: "Create your Account"}},
		{name: "verify", page: "verify", data: pageData{Title: "Choose your password", Token: "token"}},
		{name: "verify contact change", page: "contact-verify", data: pageData{Title: "Verify new email", Token: "token"}},
		{name: "accept invitation", page: "accept", data: pageData{Title: "Join Account", Token: "token"}},
		{name: "identity security", page: "security", data: richSecurity},
		{name: "empty Account shell", page: "app", data: pageData{Title: "Spyglass", Page: "app"}, currentNav: true},
		{name: "owner enrollment shell", page: "app", data: pageData{Title: "Spyglass", Page: "app", Selected: &choice, Choices: []accountaccess.Choice{choice}, OwnerEnrollmentRequired: true}, currentNav: true},
		{name: "active Account shell", page: "app", data: pageData{Title: "Spyglass", Page: "app", Selected: &choice, Choices: []accountaccess.Choice{choice}}, currentNav: true},
		{name: "empty Work", page: "work", data: pageData{Title: "Work", Page: "work"}, currentNav: true},
		{name: "locked Work", page: "work", data: pageData{Title: "Work", Page: "work", Selected: &choice, Choices: []accountaccess.Choice{choice}}, currentNav: true},
		{name: "writable Work", page: "work", data: pageData{Title: "Work", Page: "work", Selected: &choice, Choices: []accountaccess.Choice{choice}, WorkAvailable: true}, currentNav: true},
		{name: "read-only Work", page: "work", data: pageData{Title: "Work", Page: "work", Selected: &choice, Choices: []accountaccess.Choice{choice}, WorkAvailable: true, WorkReadOnly: true}, currentNav: true},
		{name: "empty Agents", page: "agents", data: pageData{Title: "Agents", Page: "agents"}, currentNav: true},
		{name: "locked Agents", page: "agents", data: pageData{Title: "Agents", Page: "agents", Selected: &choice, Choices: []accountaccess.Choice{choice}}, currentNav: true},
		{name: "writable Agents", page: "agents", data: pageData{Title: "Agents", Page: "agents", Selected: &choice, Choices: []accountaccess.Choice{choice}, AgentsAvailable: true}, currentNav: true},
		{name: "read-only Agents", page: "agents", data: pageData{Title: "Agents", Page: "agents", Selected: &choice, Choices: []accountaccess.Choice{choice}, AgentsAvailable: true, AgentsReadOnly: true}, currentNav: true},
		{name: "Account lifecycle", page: "closures", data: pageData{Title: "Account lifecycle", Page: "closures"}, currentNav: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var rendered bytes.Buffer
			if err := pages.ExecuteTemplate(&rendered, test.page, test.data); err != nil {
				t.Fatal(err)
			}
			document, err := html.Parse(strings.NewReader(rendered.String()))
			if err != nil {
				t.Fatal(err)
			}
			assertAccessiblePageFrame(t, document, test.currentNav)
		})
	}
}

func TestPrivateBrowserScriptsPreserveAccessibleInteractionState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		asset string
		want  []string
	}{
		{
			asset: "assets/work.js",
			want: []string{
				`card.setAttribute("aria-pressed", "false")`,
				`current.setAttribute("aria-pressed", String(selected))`,
				`detail.setAttribute("aria-busy", "true")`,
				`detail.setAttribute("aria-busy", "false")`,
				`createDialog.addEventListener("close", () => createOpen.focus())`,
				`transitionDialog.addEventListener("close"`,
				`assignmentDialog.addEventListener("close"`,
				`detail.focus()`,
				`announce(`,
			},
		},
		{
			asset: "assets/agents.js",
			want: []string{
				`window.matchMedia("(prefers-reduced-motion: reduce)")`,
				`behavior: reducedMotion.matches ? "auto" : "smooth"`,
			},
		},
	}
	for _, test := range tests {
		raw, err := assets.ReadFile(test.asset)
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range test.want {
			if !bytes.Contains(raw, []byte(expected)) {
				t.Errorf("%s is missing accessible interaction contract %q", test.asset, expected)
			}
		}
	}
}

func assertAccessiblePageFrame(t *testing.T, document *html.Node, currentNav bool) {
	t.Helper()
	root := firstElement(document, "html")
	if root == nil || attribute(root, "lang") != "en" {
		t.Fatal("document must declare English language")
	}
	if title := firstElement(document, "title"); title == nil || strings.TrimSpace(textContent(title)) == "" {
		t.Fatal("document must have a non-empty title")
	}
	body := firstElement(document, "body")
	first := firstElementChild(body)
	if first == nil || first.Data != "a" || !hasClass(first, "skip-link") || attribute(first, "href") != "#main-content" {
		t.Fatal("skip link must be the first body element and target main content")
	}
	mains := elements(document, "main")
	if len(mains) != 1 || attribute(mains[0], "id") != "main-content" || attribute(mains[0], "tabindex") != "-1" {
		t.Fatalf("page must have one focusable main landmark, got %d", len(mains))
	}
	if headings := elements(document, "h1"); len(headings) != 1 {
		t.Fatalf("page must have exactly one h1, got %d", len(headings))
	}
	assertHeadingOrder(t, document)

	ids := map[string]bool{}
	walkElements(document, func(node *html.Node) {
		if id := attribute(node, "id"); id != "" {
			if ids[id] {
				t.Errorf("duplicate id %q", id)
			}
			ids[id] = true
		}
	})
	walkElements(document, func(node *html.Node) {
		if hasAttribute(node, "autofocus") {
			t.Errorf("autofocus must not bypass the skip link: %s", elementSummary(node))
		}
		for _, referenceAttribute := range []string{"aria-describedby", "aria-labelledby"} {
			for _, id := range strings.Fields(attribute(node, referenceAttribute)) {
				if !ids[id] {
					t.Errorf("%s references missing id %q", referenceAttribute, id)
				}
			}
		}
		switch node.Data {
		case "a":
			if attribute(node, "href") == "" || accessibleName(node) == "" {
				t.Errorf("link must have an href and accessible name: %s", elementSummary(node))
			}
		case "button":
			if accessibleName(node) == "" {
				t.Errorf("button must have an accessible name: %s", elementSummary(node))
			}
		case "input", "select", "textarea":
			if node.Data == "input" && attribute(node, "type") == "hidden" {
				return
			}
			if !hasAccessibleLabel(document, node) {
				t.Errorf("form control must have an accessible label: %s", elementSummary(node))
			}
		case "nav":
			if accessibleName(node) == "" {
				t.Errorf("navigation landmark must have an accessible name: %s", elementSummary(node))
			}
		}
	})
	current := 0
	walkElements(document, func(node *html.Node) {
		if attribute(node, "aria-current") == "page" {
			current++
		}
	})
	if currentNav && current != 1 {
		t.Errorf("private shell must identify exactly one current page, got %d", current)
	}
}

func assertHeadingOrder(t *testing.T, document *html.Node) {
	t.Helper()
	previous := 0
	walkElements(document, func(node *html.Node) {
		if len(node.Data) != 2 || node.Data[0] != 'h' || node.Data[1] < '1' || node.Data[1] > '6' {
			return
		}
		level := int(node.Data[1] - '0')
		if previous != 0 && level > previous+1 {
			t.Errorf("heading level jumps from h%d to h%d at %q", previous, level, strings.TrimSpace(textContent(node)))
		}
		previous = level
	})
}

func hasAccessibleLabel(document, node *html.Node) bool {
	if accessibleName(node) != "" {
		return true
	}
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Type == html.ElementNode && parent.Data == "label" && strings.TrimSpace(textContent(parent)) != "" {
			return true
		}
	}
	id := attribute(node, "id")
	if id == "" {
		return false
	}
	for _, label := range elements(document, "label") {
		if attribute(label, "for") == id && strings.TrimSpace(textContent(label)) != "" {
			return true
		}
	}
	return false
}

func accessibleName(node *html.Node) string {
	if label := strings.TrimSpace(attribute(node, "aria-label")); label != "" {
		return label
	}
	return strings.TrimSpace(textContent(node))
}

func elements(root *html.Node, name string) []*html.Node {
	var found []*html.Node
	walkElements(root, func(node *html.Node) {
		if node.Data == name {
			found = append(found, node)
		}
	})
	return found
}

func firstElement(root *html.Node, name string) *html.Node {
	for _, node := range elements(root, name) {
		return node
	}
	return nil
}

func firstElementChild(node *html.Node) *html.Node {
	if node == nil {
		return nil
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode {
			return child
		}
	}
	return nil
}

func walkElements(root *html.Node, visit func(*html.Node)) {
	if root == nil {
		return
	}
	if root.Type == html.ElementNode {
		visit(root)
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		walkElements(child, visit)
	}
}

func attribute(node *html.Node, name string) string {
	if node == nil {
		return ""
	}
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val
		}
	}
	return ""
}

func hasAttribute(node *html.Node, name string) bool {
	if node == nil {
		return false
	}
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return true
		}
	}
	return false
}

func hasClass(node *html.Node, class string) bool {
	for _, candidate := range strings.Fields(attribute(node, "class")) {
		if candidate == class {
			return true
		}
	}
	return false
}

func textContent(node *html.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == html.TextNode {
		return node.Data
	}
	var text strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		text.WriteString(textContent(child))
		text.WriteByte(' ')
	}
	return text.String()
}

func elementSummary(node *html.Node) string {
	return fmt.Sprintf("<%s id=%q name=%q>", node.Data, attribute(node, "id"), attribute(node, "name"))
}
