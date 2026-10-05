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

// ChatSystemPrompt is the Chat tab's system prompt (chatops.go, main
// package). Unlike summarizePrompt/extractActionItemsPrompt it carries no
// data of its own — the model has no built-in knowledge of the user's
// tasks/projects/meetings and is told to always use a tool rather than
// guess, which is the one piece of prompt engineering the tool-calling
// design (as opposed to stuffing a snapshot into every turn) actually
// depends on. today lets it reason about relative dates ("what's due this
// week") without a tool round trip just to learn what day it is.
func ChatSystemPrompt(today string) string {
	return "You are the assistant inside tjek, a terminal task manager. The user will ask about their " +
		"tasks, projects, and meetings. You have NO built-in knowledge of any of it — every fact about a " +
		"specific task, project, or meeting must come from calling a tool first. Never invent or guess a " +
		"task title, project name, meeting, or ID; if a tool returns nothing relevant, say so plainly instead " +
		"of making something up.\n\n" +
		"Keep answers short and to the point — this is a terminal UI, not a chat app with room to scroll.\n\n" +
		"If the user asks you to create, complete, or edit a task, call the matching tool directly — the app " +
		"itself will show the user a confirmation before anything actually happens, so you do not need to ask " +
		"for confirmation yourself in text, and you should not add extra explanation alongside that call; a " +
		"follow-up question can wait for their answer.\n\n" +
		"Today's date is " + today + "."
}

func checkTranscriptLen(transcript string) error {
	if len(transcript) > maxTranscriptChars {
		return fmt.Errorf("%w (got %d)", errTranscriptTooLong, len(transcript))
	}
	return nil
}
