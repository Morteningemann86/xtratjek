// Package aiprovider wraps the external AI calls the Meetings feature needs:
// summarizing a transcript, mining it for action items, and (OpenAI only)
// transcribing audio to text. It is the one place in tjek that talks to a
// third-party AI API — the rest of the app has no network calls beyond sync
// and self-update — so every call here is opt-in, keyed by a user-supplied
// API key, and never runs unless the user asks for it from the Meetings tab.
package aiprovider

import (
	"context"
	"errors"
)

// ErrNoAPIKey is returned by New when the requested provider has no key
// configured, so callers can show one consistent message ("add an API key in
// Settings") instead of each provider inventing its own wording.
var ErrNoAPIKey = errors.New("no API key configured")

// maxTranscriptChars caps what is sent to a text provider. Meeting
// transcripts are normally far under this; the cap exists so a pathological
// input (an accidental audio-book-length recording) fails fast with a clear
// error instead of a slow, expensive request. ~60k characters is comfortably
// inside every provider's context window at today's models while still being
// a real safety margin, not a tight budget like Claes's 1024-token window in
// the Byggesagsbehandling PoC — these are frontier chat models, not a small
// decision model, so headroom is cheap.
const maxTranscriptChars = 60000

var errTranscriptTooLong = errors.New("transcript is too long to summarize (over 60,000 characters) — trim it or split the meeting")

// Suggestion is one AI-proposed action item, before it becomes a
// meeting.Suggestion with an ID and persistence. Kept distinct from
// meeting.Suggestion so this package has no dependency on storage or the
// domain package's identity/resolution fields — it only ever produces fresh,
// unresolved candidates.
type Suggestion struct {
	Title    string
	Project  string
	Tags     []string
	Priority string // "h" | "m" | "l" — see todo's quick-add grammar (helpers.go)
	Due      string // free text for parseDueDate ("friday", "+3d", ""), not a parsed date
}

// TextProvider summarizes a transcript and mines it for action items. All
// three of Anthropic, OpenAI and Gemini implement it, so the provider the
// user picks in Settings is a drop-in swap.
type TextProvider interface {
	Name() string
	// Summarize returns a short prose summary of transcript.
	Summarize(ctx context.Context, transcript string) (string, error)
	// ExtractActionItems reads transcript and summary and proposes action
	// items, each with its best guess at project/tags from the caller's
	// existing names — never inventing a project, only picking from
	// existingProjects or leaving it blank. Tags may include a short new
	// one when nothing existing fits, the same way an existing project
	// grouping is also unassigned when nothing fits.
	ExtractActionItems(ctx context.Context, transcript, summary string, existingProjects, existingTags []string) ([]Suggestion, error)
}

// TranscriptionProvider turns a recorded audio file into text. Only OpenAI
// implements this today: Anthropic and Gemini's APIs have no speech-to-text
// endpoint, so a transcription request always needs an OpenAI key configured
// even when the text provider is set to one of the others — see
// NewTranscriber.
type TranscriptionProvider interface {
	Name() string
	Transcribe(ctx context.Context, audioPath string) (string, error)
}
