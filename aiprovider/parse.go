package aiprovider

import (
	"encoding/json"
	"fmt"
	"strings"
)

// rawSuggestion is the wire shape every provider is asked to return for one
// action item; jsonToSuggestions converts it to the public Suggestion once,
// validating and normalizing the priority letter along the way.
type rawSuggestion struct {
	Title    string   `json:"title"`
	Project  string   `json:"project"`
	Tags     []string `json:"tags"`
	Priority string   `json:"priority"`
	Due      string   `json:"due"`
}

// parseSuggestionsJSON extracts a JSON array from a model's text response and
// converts it to Suggestions. Models asked for "ONLY a JSON array" sometimes
// wrap it in a markdown code fence or add a stray sentence anyway, so this
// takes the substring between the first '[' and the last ']' rather than
// requiring the whole response to parse as JSON — the same defensive
// extraction the compliance portal's Gemini integration uses
// (AI-comliance/server.ts) for exactly the same reason.
func parseSuggestionsJSON(text string) ([]Suggestion, error) {
	start := strings.IndexByte(text, '[')
	end := strings.LastIndexByte(text, ']')
	if start < 0 || end < start {
		return nil, fmt.Errorf("no JSON array found in model response: %.200s", text)
	}
	var raw []rawSuggestion
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return nil, fmt.Errorf("parsing action items: %w", err)
	}
	out := make([]Suggestion, 0, len(raw))
	for _, r := range raw {
		title := strings.TrimSpace(r.Title)
		if title == "" {
			continue
		}
		out = append(out, Suggestion{
			Title:    title,
			Project:  strings.TrimSpace(r.Project),
			Tags:     cleanTags(r.Tags),
			Priority: normalizePriority(r.Priority),
			Due:      strings.TrimSpace(r.Due),
		})
	}
	return out, nil
}

func cleanTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.TrimSpace(strings.ToLower(t)); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// normalizePriority folds whatever the model sent onto "h"/"m"/"l" — the
// three letters the quick-add grammar and CLI --priority flag accept
// (helpers.go) — defaulting to medium for anything unrecognized rather than
// failing the whole suggestion over one bad field.
func normalizePriority(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "h", "high":
		return "h"
	case "l", "low":
		return "l"
	default:
		return "m"
	}
}
