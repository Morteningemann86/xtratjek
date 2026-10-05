package aiprovider

import (
	"fmt"
	"strings"
)

// Prompts are shared across providers so the three implementations only
// differ in how they call their API, never in what they ask it — a provider
// switch in Settings must not change the quality or shape of the output.

func summarizePrompt(transcript string) string {
	return "You are an assistant that summarizes meeting notes concisely for a task-management app. " +
		"Summarize the following meeting transcript in 3 to 6 sentences, focused on decisions made and " +
		"important context. Do not list action items in the summary — those are extracted separately. " +
		"Respond with the summary only, no preamble.\n\nTranscript:\n\"\"\"\n" + transcript + "\n\"\"\""
}

// extractActionItemsPrompt asks for a JSON array response. All three
// providers parse the result the same way (parseSuggestionsJSON); a provider
// whose API supports a native JSON mode (OpenAI) still uses this prompt,
// just with its response-format flag also set.
func extractActionItemsPrompt(transcript, summary string, existingProjects, existingTags []string) string {
	var b strings.Builder
	b.WriteString("You are an assistant that extracts concrete action items from meeting notes for a task " +
		"manager called tjek. Read the meeting summary and transcript below and list every concrete action " +
		"item mentioned — something with a specific outcome someone needs to do, not general discussion points " +
		"or things already finished.\n\n")
	b.WriteString("For each action item, suggest:\n")
	b.WriteString("- title: a short, clear task title in imperative form (e.g. \"Send the proposal to Acme\")\n")
	if len(existingProjects) > 0 {
		b.WriteString("- project: the most relevant project FROM THIS LIST ONLY: " + strings.Join(existingProjects, ", ") +
			" — or \"\" if none fit. Never invent a project name.\n")
	} else {
		b.WriteString("- project: \"\" (no existing projects to choose from)\n")
	}
	if len(existingTags) > 0 {
		b.WriteString("- tags: 0 to 3 relevant tags, preferring these existing ones where they fit: " +
			strings.Join(existingTags, ", ") + ". You may suggest a short new lowercase tag if nothing existing fits.\n")
	} else {
		b.WriteString("- tags: 0 to 3 short, relevant lowercase tags\n")
	}
	b.WriteString("- priority: \"h\", \"m\", or \"l\"\n")
	b.WriteString("- due: a short free-text date hint if a deadline was mentioned (e.g. \"friday\", \"2026-10-20\"), or \"\" if none\n\n")
	b.WriteString("Respond with ONLY a JSON array, no other text, in exactly this shape:\n")
	b.WriteString(`[{"title":"...","project":"...","tags":["..."],"priority":"m","due":""}]` + "\n\n")
	b.WriteString("If there are no clear action items, respond with an empty array: []\n\n")
	if summary != "" {
		b.WriteString("Meeting summary:\n\"\"\"\n" + summary + "\n\"\"\"\n\n")
	}
	b.WriteString("Full transcript:\n\"\"\"\n" + transcript + "\n\"\"\"")
	return b.String()
}

func checkTranscriptLen(transcript string) error {
	if len(transcript) > maxTranscriptChars {
		return fmt.Errorf("%w (got %d)", errTranscriptTooLong, len(transcript))
	}
	return nil
}
