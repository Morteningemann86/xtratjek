// Package meeting is tjek's framework-free domain layer for meeting notes:
// the Meeting type, its processing status, and the action-item Suggestions an
// AI pass proposes from a meeting's transcript. Like todo, it carries no
// Bubble Tea, storage or AI-provider concerns — those are wired in by the app
// (storage_sqlite.go, update_meetings.go) and by aiprovider.
package meeting

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Status is where a meeting sits in its processing pipeline. Every value past
// StatusDraft is reached by an async command, never set directly from a
// keypress, so the UI always shows what the last completed step actually
// produced rather than what the user merely requested.
type Status string

const (
	StatusDraft        Status = "draft"        // notes being captured; nothing run yet
	StatusRecording    Status = "recording"    // audio capture in progress
	StatusTranscribing Status = "transcribing" // audio being sent to the transcription provider
	StatusSummarizing  Status = "summarizing"  // transcript being summarized and mined for action items
	StatusReady        Status = "ready"        // Summary set, Suggestions awaiting review
	StatusReviewed     Status = "reviewed"     // every suggestion for this meeting is resolved
	StatusError        Status = "error"        // see ErrorMsg; retrying re-enters the step that failed
)

// Meeting is one recorded or hand-typed set of meeting notes.
type Meeting struct {
	ID        string
	Title     string
	Date      time.Time
	Attendees []string
	// AudioPath is the recorded wav file, "" if the meeting has no recording
	// (typed or pasted notes only).
	AudioPath string
	// Transcript is the raw text: typed directly, pasted in, or produced by
	// the transcription provider from AudioPath.
	Transcript string
	// Summary is the AI-generated summary of Transcript, set once Status
	// reaches StatusReady.
	Summary  string
	Status   Status
	ErrorMsg string // set when Status == StatusError; cleared on the next attempt

	CreatedAt  time.Time
	ModifiedAt time.Time
	Deleted    bool
	DeletedAt  time.Time
}

// New creates a draft meeting dated now, ready for notes to be typed, pasted
// or recorded into it.
func New(title string) Meeting {
	now := time.Now()
	return Meeting{
		ID:         uuid.New().String(),
		Title:      title,
		Date:       now,
		Status:     StatusDraft,
		CreatedAt:  now,
		ModifiedAt: now,
	}
}

// Touch stamps ModifiedAt to now. Call it before persisting any change.
func (m *Meeting) Touch() {
	m.ModifiedAt = time.Now()
}

// AttendeesText joins Attendees into one comma-separated line for display and
// editing.
func (m Meeting) AttendeesText() string {
	return strings.Join(m.Attendees, ", ")
}

// SetAttendeesText parses a comma-separated line back into Attendees,
// trimming whitespace and dropping empty entries so "Alice,  , Bob" and
// "Alice, Bob" are equivalent.
func (m *Meeting) SetAttendeesText(s string) {
	parts := strings.Split(s, ",")
	attendees := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			attendees = append(attendees, p)
		}
	}
	m.Attendees = attendees
}

// NeedsReview reports whether the meeting has AI suggestions waiting on the
// user before it can be considered done with.
func (m Meeting) NeedsReview() bool { return m.Status == StatusReady }

// CanRunAI reports whether there is a transcript to summarize and mine for
// action items. A meeting mid-recording or mid-transcription is not
// re-enterable: the running step owns the next status transition.
func (m Meeting) CanRunAI() bool {
	return strings.TrimSpace(m.Transcript) != "" &&
		m.Status != StatusRecording && m.Status != StatusTranscribing && m.Status != StatusSummarizing
}

// CanRecord reports whether starting a new recording makes sense: not already
// recording, and not mid-transcription/summarization of a previous one.
func (m Meeting) CanRecord() bool {
	return m.Status != StatusRecording && m.Status != StatusTranscribing && m.Status != StatusSummarizing
}
