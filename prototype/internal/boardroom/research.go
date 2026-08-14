package boardroom

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// ResearchActivity is the user-visible account of a document or web lookup.
// It intentionally contains only bounded evidence metadata, never raw tool
// payloads or complete scraped page content.
type ResearchActivity struct {
	Tool    string
	Query   string
	URL     string
	Status  string
	Error   string
	Results []ResearchResult
}

type ResearchResult struct {
	Title   string
	URL     string
	Excerpt string
}

type researchEvent struct {
	Type    string          `json:"event_type"`
	Payload json.RawMessage `json:"payload"`
}

type researchRequest struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func parseResearchEvents(encoded json.RawMessage) []ResearchActivity {
	var events []researchEvent
	if len(encoded) == 0 || json.Unmarshal(encoded, &events) != nil {
		return nil
	}

	activities := make([]ResearchActivity, 0)
	indexes := make(map[string]int)
	for _, event := range events {
		switch event.Type {
		case "tool.requested":
			var request researchRequest
			if json.Unmarshal(event.Payload, &request) != nil || !isVisibleResearchTool(request.Name) {
				continue
			}
			activity := ResearchActivity{Tool: request.Name, Status: "running"}
			var arguments struct {
				Query string `json:"query"`
				URL   string `json:"url"`
			}
			_ = json.Unmarshal(request.Arguments, &arguments)
			activity.Query = strings.TrimSpace(arguments.Query)
			activity.URL = strings.TrimSpace(arguments.URL)
			indexes[request.ID] = len(activities)
			activities = append(activities, activity)

		case "tool.completed":
			var completed struct {
				RequestID string          `json:"request_id"`
				Name      string          `json:"name"`
				Result    json.RawMessage `json:"result"`
			}
			if json.Unmarshal(event.Payload, &completed) != nil || !isVisibleResearchTool(completed.Name) {
				continue
			}
			index, ok := indexes[completed.RequestID]
			if !ok {
				continue
			}
			activities[index].Status = "completed"
			activities[index].Results = parseResearchResults(completed.Name, completed.Result)

		case "tool.failed", "tool.skipped":
			var failed struct {
				RequestID string `json:"request_id"`
				Name      string `json:"name"`
				Error     string `json:"error"`
				Reason    string `json:"reason"`
			}
			if json.Unmarshal(event.Payload, &failed) != nil || !isVisibleResearchTool(failed.Name) {
				continue
			}
			index, ok := indexes[failed.RequestID]
			if !ok {
				continue
			}
			activities[index].Status = strings.TrimPrefix(event.Type, "tool.")
			activities[index].Error = boundedResearchText(firstNonEmpty(failed.Error, failed.Reason), 240)
		}
	}
	return activities
}

func isVisibleResearchTool(name string) bool {
	return name == "documents.search" || name == "web.search" || name == "web.read"
}

func parseResearchResults(tool string, encoded json.RawMessage) []ResearchResult {
	var result struct {
		Query   string `json:"query"`
		Results []struct {
			Title        string `json:"title"`
			URL          string `json:"url"`
			Description  string `json:"description"`
			DocumentName string `json:"document_name"`
			Excerpt      string `json:"excerpt"`
			Content      string `json:"content"`
		} `json:"results"`
	}
	if json.Unmarshal(encoded, &result) != nil {
		return nil
	}
	items := make([]ResearchResult, 0, len(result.Results))
	for _, item := range result.Results {
		title := firstNonEmpty(item.Title, item.DocumentName, item.URL, "Untitled result")
		excerpt := firstNonEmpty(item.Description, item.Excerpt)
		if tool == "web.read" && excerpt == "" && item.Content != "" {
			excerpt = item.Content
		}
		items = append(items, ResearchResult{
			Title: boundedResearchText(title, 180), URL: strings.TrimSpace(item.URL),
			Excerpt: boundedResearchText(excerpt, 280),
		})
	}
	return items
}

func boundedResearchText(value string, maximum int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maximum-1])) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
