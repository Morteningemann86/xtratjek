package meeting

import (
	"time"

	"github.com/google/uuid"
)

// Suggestion is one AI-proposed action item mined from a meeting's
// transcript, awaiting the human review every suggestion requires before it
// becomes a real task. Resolved suggestions are kept, not deleted, so a
// meeting's review history survives a restart mid-review — reopening the
// meeting shows exactly which items are still undecided.
type Suggestion struct {
	ID        string
	MeetingID string
	Title     string

	// SuggestedProject and SuggestedTags are the AI's best guess at where
	// this action item belongs, chosen from the project/tag names the caller
	// handed the provider — see aiprovider.Suggestion. Both are edited
	// in-place on the review screen before acceptance.
	SuggestedProject string
	SuggestedTags    []string

	// SuggestedPriority is "h", "m" or "l" — the same letters the quick-add
	// grammar and CLI --priority flag use (helpers.go), so accepting a
	// suggestion can go straight through the same parser.
	SuggestedPriority string

	// SuggestedDue is free text in the quick-add due-date grammar ("friday",
	// "+3d", "", ...), resolved with parseDueDate only at accept time. Kept
	// as text rather than a parsed time.Time so an unparseable guess is
	// something the user can see and fix on the review screen instead of
	// silently becoming no due date.
	SuggestedDue string

	// Resolved is true once the user has accepted or rejected this
	// suggestion; Accepted is only meaningful when Resolved is true.
	Resolved bool
	Accepted bool
	// CreatedTaskID is the real task's id once Accepted created one.
	CreatedTaskID string

	CreatedAt time.Time
}

// NewSuggestion creates an unresolved suggestion with medium priority — the
// same default todo.New uses — so an AI pass that has no priority opinion on
// an item doesn't silently need one.
func NewSuggestion(meetingID, title string) Suggestion {
	return Suggestion{
		ID:                uuid.New().String(),
		MeetingID:         meetingID,
		Title:             title,
		SuggestedPriority: "m",
		CreatedAt:         time.Now(),
	}
}
